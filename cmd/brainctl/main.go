// Command brainctl is an ops CLI for the CaBrain brain plugin: apply the schema
// and BM25 layer, inspect the live database, and smoke-test the multilingual BM25
// path — the one hybrid-recall half that needs no embeddings, so it is verifiable
// before TEI is reachable on stack_stacknet.
//
// It connects with DATABASE_URL (pgx). Usage:
//
//	brainctl inspect     — extensions, tables, tokenizer, bm25 column/index, counts
//	brainctl migrate     — apply schema.sql + bm25.sql (idempotent)
//	brainctl bm25         — apply just the BM25 layer (idempotent)
//	brainctl bm25-test    — seed a few multilingual rows and run a BM25 ranking query
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/togo-framework/brain"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: brainctl <inspect|migrate|bm25|bm25-test>")
		os.Exit(2)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatal("DATABASE_URL is empty — source the env first (set -a; . ./.env; set +a)")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fatal("open: " + err.Error())
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fatal("ping: " + err.Error())
	}

	switch os.Args[1] {
	case "inspect":
		inspect(ctx, db)
	case "migrate":
		if err := brain.Migrate(ctx, db); err != nil {
			if errors.Is(err, brain.ErrBM25Skipped) {
				fmt.Println("✓ schema applied")
				fmt.Println("⚠ BM25 layer skipped — recall falls back to vector-only until granted:")
				fmt.Println("  " + err.Error())
				fmt.Println("  fix: run infra/grant-bm25.sql as a superuser (see that file)")
				return
			}
			fatal("migrate: " + err.Error())
		}
		fmt.Println("✓ schema + BM25 layer applied")
	case "bm25":
		must(brain.ApplyBM25(ctx, db), "apply bm25")
		fmt.Println("✓ BM25 layer applied (tokenizer + content_bm25 + index)")
	case "bm25-test":
		bm25Test(ctx, db)
	default:
		fatal("unknown command: " + os.Args[1])
	}
}

func inspect(ctx context.Context, db *sql.DB) {
	fmt.Println("── extensions ──")
	q(ctx, db, `SELECT extname, extversion FROM pg_extension
	            WHERE extname IN ('vchord','vchord_bm25','pg_tokenizer','vector','pg_partman')
	            ORDER BY extname`)
	fmt.Println("── core tables ──")
	q(ctx, db, `SELECT tablename FROM pg_tables
	            WHERE schemaname='public'
	              AND tablename IN ('memories','memories_default','entities','memory_entities','memory_events','namespace_grants')
	            ORDER BY tablename`)
	fmt.Println("── content_bm25 column ──")
	q(ctx, db, `SELECT column_name, udt_name FROM information_schema.columns
	            WHERE table_name='memories' AND column_name='content_bm25'`)
	fmt.Println("── bm25 index ──")
	q(ctx, db, `SELECT indexname FROM pg_indexes WHERE tablename='memories' AND indexname='memories_bm25'`)
	fmt.Println("── row counts ──")
	q(ctx, db, `SELECT
	              (SELECT count(*) FROM memories)                          AS memories,
	              (SELECT count(*) FROM memories WHERE content_bm25 IS NOT NULL) AS bm25_populated,
	              (SELECT count(*) FROM memories WHERE embedding IS NOT NULL)    AS embedded`)
}

// bm25Test proves the vchord_bm25 API end-to-end without any embeddings: seed
// multilingual rows into a throwaway namespace, populate content_bm25 via
// tokenize(), then rank with content_bm25 <&> to_bm25query(...) (lower = better).
func bm25Test(ctx context.Context, db *sql.DB) {
	must(brain.ApplyBM25(ctx, db), "apply bm25")
	const ns = "__bm25_smoke__"
	_, _ = db.ExecContext(ctx, `DELETE FROM memories WHERE namespace=$1`, ns)
	rows := []string{
		"The quick brown fox jumps over the lazy dog",
		"Kubernetes deployments roll out pods across the cluster",
		"القط العربي يجلس على السجادة الحمراء", // "the Arabic cat sits on the red rug"
		"البرمجة باللغة العربية ممتعة ومفيدة",  // "programming in Arabic is fun and useful"
	}
	for _, c := range rows {
		_, err := db.ExecContext(ctx, `
			INSERT INTO memories (namespace, network, memory_type, content, tier, content_bm25)
			VALUES ($1,'fact','semantic',$2,'hot', tokenize($2,'cabrain_ml'))`, ns, c)
		must(err, "seed insert")
	}
	fmt.Printf("✓ seeded %d multilingual rows into %s\n\n", len(rows), ns)

	for _, query := range []string{"cluster pods", "العربي"} {
		fmt.Printf("── BM25 query: %q ──\n", query)
		q(ctx, db, `
			SELECT left(content,48) AS content,
			       round((content_bm25 <&> to_bm25query('memories_bm25', tokenize($2,'cabrain_ml')))::numeric, 4) AS score
			FROM memories
			WHERE namespace=$1 AND content_bm25 IS NOT NULL
			ORDER BY content_bm25 <&> to_bm25query('memories_bm25', tokenize($2,'cabrain_ml'))
			LIMIT 4`, ns, query)
		fmt.Println()
	}
	_, _ = db.ExecContext(ctx, `DELETE FROM memories WHERE namespace=$1`, ns)
	fmt.Println("✓ cleaned up smoke namespace")
}

// --- tiny query printer -------------------------------------------------------

func q(ctx context.Context, db *sql.DB, query string, args ...any) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		fmt.Printf("  ! %s\n", err.Error())
		return
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	fmt.Println("  " + strings.Join(cols, " | "))
	n := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			fmt.Printf("  ! scan: %s\n", err.Error())
			return
		}
		cells := make([]string, len(cols))
		for i, v := range vals {
			cells[i] = fmt.Sprintf("%v", deref(v))
		}
		fmt.Println("  " + strings.Join(cells, " | "))
		n++
	}
	if n == 0 {
		fmt.Println("  (no rows)")
	}
}

func deref(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

func must(err error, what string) {
	if err != nil {
		fatal(what + ": " + err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "brainctl: "+msg)
	os.Exit(1)
}
