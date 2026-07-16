package brain

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

// schemaSQL is the canonical CaBrain data model (SPEC §3), shipped with the
// plugin and applied by Migrate. Postgres-specific (vector/BM25/partitioning);
// requires togo-postgres with the vchord stack. See the file for the
// version-sensitive bits (bm25 tokenizer wiring, pg_partman signature).
//
//go:embed schema.sql
var schemaSQL string

// SchemaSQL exposes the embedded DDL (for inspection / external migration).
func SchemaSQL() string { return schemaSQL }

// Migrate applies the brain schema. Idempotent for the CREATE ... IF NOT EXISTS
// parts; the extension and pg_partman.create_parent statements need appropriate
// privileges and are guarded/annotated in schema.sql. Intended to run against the
// provisioned cabrain DB (Blocker B) — either at boot or via an explicit migrate.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("brain.Migrate: nil db")
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("brain.Migrate: applying schema: %w", err)
	}
	return nil
}

// recallSQL is the hot-tier hybrid retrieval query (SPEC §4.2): dense vector +
// BM25 fused with RRF plus a salience nudge, scoped to namespace, hot tier,
// non-invalidated rows. Positional args: $1 query embedding, $2 namespace,
// $3 query text. Reranking + 1-hop entity expansion happen in Go after this.
const recallSQL = `
WITH vec AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY embedding <=> $1) AS r
  FROM memories
  WHERE namespace = $2 AND invalid_at IS NULL AND tier = 'hot'
  ORDER BY embedding <=> $1 LIMIT 40
),
txt AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY score DESC) AS r
  FROM (
    SELECT id, bm25_score(memories_bm25, $3) AS score
    FROM memories
    WHERE content @@@ $3 AND namespace = $2 AND invalid_at IS NULL AND tier = 'hot'
    LIMIT 40
  ) t
)
SELECT m.id, m.content, m.network, m.memory_type, m.source_kind, m.source_ref,
       m.importance, m.valid_at,
       COALESCE(1.0/(60+vec.r),0) + COALESCE(1.0/(60+txt.r),0) + 0.15 * m.importance AS score
FROM memories m
LEFT JOIN vec ON vec.id = m.id
LEFT JOIN txt ON txt.id = m.id
WHERE vec.id IS NOT NULL OR txt.id IS NOT NULL
ORDER BY score DESC LIMIT $4;`
