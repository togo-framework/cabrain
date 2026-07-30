package brain

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// bm25Tokenizer is the pg_tokenizer tokenizer recall/retain use. It is created by
// infra (the app role cannot), so the name is configurable; the default matches
// the multilingual tokenizer provisioned on the live cabrain DB.
//
// Default is `cabrain_ml` (llmlingua2, a pre-trained multilingual subword model).
// The previous default `cabrain_bm25_tok` is a FIXED-VOCAB PROBE built from a tiny
// sample table: it returns ZERO tokens for essentially all real input — verified
// empty for English, Arabic, and acronyms alike — which silently disabled the whole
// BM25 half of hybrid retrieval. With cabrain_ml the lexical layer works and is
// exactly what rescues short/acronym queries that dense vectors are weak on:
// "BIV portfolio Turif" now ranks the BIV=Turif alias at -50.9, "CPTO role
// responsibilities" ranks the CPTO position spec at -17.2 (both had no signal at all
// before). Arabic queries legitimately get no lexical hit against English documents —
// cross-lingual matching is the multilingual embedder's job, not BM25's.
//
// If the tokenizer is missing on some deployment, recallSQL simply errors and the
// store transparently falls back to the vector-only query, so this default is safe.
func bm25Tokenizer() string {
	if t := os.Getenv("BRAIN_BM25_TOKENIZER"); t != "" {
		return t
	}
	return "cabrain_ml"
}

// hnswEFSearch is the HNSW candidate-list size (hnsw.ef_search) applied to every
// ANN query in recall/search.
//
// Why this is NOT optional: the ANN index contains EVERY row, including
// superseded ones, but the queries post-filter with `invalid_at IS NULL AND
// tier='hot'`. On the live brain ~85% of rows are superseded (the write-decision
// invalidates on UPDATE), so an ef_search of 40 returns ~40 raw candidates of
// which only a handful survive the filter — measured 12/40 on flowos, and for
// some query vectors ZERO, which is why questions whose answer demonstrably
// exists ("BIV", short/Arabic turns) recalled nothing at all while longer
// queries worked. Raising the candidate list restores full results:
//
//	ef_search=40 → 12/40 rows (8ms) · 100 → 32/40 · 200 → 40/40 (12ms)
//
// 200 is the default: full recall at negligible cost. Tune with
// BRAIN_HNSW_EF_SEARCH. The structural fix is a partial index on the live rows
// (see docs) — this makes recall correct regardless.
func hnswEFSearch() int {
	if v := os.Getenv("BRAIN_HNSW_EF_SEARCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 10000 {
			return n
		}
	}
	return 200
}

// ErrBM25Skipped wraps a non-fatal BM25-layer failure during Migrate: the core
// schema applied, but the vchord_bm25/pg_tokenizer objects could not be created
// (commonly because the app role lacks USAGE on the bm25_catalog / tokenizer_catalog
// schemas — an infra GRANT). Recall degrades to vector-only until it's granted.
var ErrBM25Skipped = errors.New("brain: BM25 layer skipped (schema applied)")

// schemaSQL is the canonical CaBrain data model (SPEC §3), shipped with the
// plugin and applied by Migrate. Postgres-specific (vector/BM25/partitioning);
// requires togo-postgres with the vchord stack. See the file for the
// version-sensitive bits (bm25 tokenizer wiring, pg_partman signature).
//
//go:embed schema.sql
var schemaSQL string

// bm25SQL is the version-sensitive BM25 layer (tokenizer + bm25vector column +
// index), kept separate so a tokenizer-config issue can never block the core
// schema. Applied best-effort by Migrate and directly by ApplyBM25.
//
//go:embed bm25.sql
var bm25SQL string

// SchemaSQL exposes the embedded DDL (for inspection / external migration).
func SchemaSQL() string { return schemaSQL }

// BM25SQL exposes the embedded BM25 layer DDL.
func BM25SQL() string { return bm25SQL }

// Migrate applies the brain schema, then the BM25 layer. Idempotent for the
// CREATE ... IF NOT EXISTS parts; the extension and pg_partman statements need
// appropriate privileges and are guarded/annotated in schema.sql. The BM25 layer
// is applied best-effort — if the vchord_bm25/pg_tokenizer stack is absent, recall
// transparently falls back to vector-only (see Store.Recall), so a BM25 failure
// must not fail the whole migrate.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("brain.Migrate: nil db")
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("brain.Migrate: applying schema: %w", err)
	}
	if err := ApplyBM25(ctx, db); err != nil {
		// Non-fatal: recall degrades to vector-only. Wrap ErrBM25Skipped so callers
		// (brainctl, a boot hook) can errors.Is-distinguish it from a real failure.
		return fmt.Errorf("%w: %v", ErrBM25Skipped, err)
	}
	return nil
}

// ApplyBM25 applies the BM25 tokenizer/column/index (idempotent). Separate so ops
// can (re)apply it without touching the core schema.
func ApplyBM25(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("brain.ApplyBM25: nil db")
	}
	if _, err := db.ExecContext(ctx, bm25SQL); err != nil {
		return fmt.Errorf("brain.ApplyBM25: %w", err)
	}
	return nil
}

// recallSQL is the hot-tier hybrid retrieval query (SPEC §4.2): dense vector +
// multilingual BM25 fused with Reciprocal Rank Fusion (RRF, k=60) plus a salience
// nudge, scoped to namespace / hot tier / non-invalidated rows.
//
// The salience term is 0.01*importance and must stay SMALL relative to RRF. RRF
// contributes at most 1/61+1/61 ≈ 0.033 (rank 1 in both lists), so the old 0.15
// coefficient made importance worth ~9x the entire relevance signal: measured on
// flowos, the BIV alias ranked #1 in BOTH the vector and BM25 lists yet fell to pool
// rank 24, behind 33 rows that matched neither, purely because bulk-imported rows
// carry importance=1.0 while the curated alias carries 0.5. This query's output is
// the candidate pool the cross-encoder reranks, so anything pushed past LIMIT here is
// gone for good — a perfect match was one bad row away from being evicted before it
// could ever be reranked. Salience is a tie-breaker, not a ranker.
//
// BM25 uses the CONFIRMED vchord_bm25 0.3.0 API: content_bm25 <&> to_bm25query(
// 'memories_bm25', tokenize($q,'cabrain_ml')). The <&> operator returns a distance
// (lower = better; infra §5.2 saw an Arabic match at -0.907), so the BM25 CTE ranks
// ASC. Rows missing an embedding still rank via BM25 and vice-versa (LEFT JOINs +
// the OR filter), so this same query serves before TEI is reachable.
//
// Positional args: $1 query embedding (::vector literal), $2 namespace,
// $3 query text, $4 limit, $5 min importance, $6 bm25 tokenizer name. Reranking +
// 1-hop entity expansion happen in Go after this. The BM25 index is on the concrete
// default partition (memories_default_bm25) — see bm25.sql for why the parent index
// can't be used.
const recallSQL = `
WITH vec AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY embedding <=> $1::vector) AS r
  FROM memories
  WHERE namespace = $2 AND invalid_at IS NULL AND tier = 'hot' AND embedding IS NOT NULL
  ORDER BY embedding <=> $1::vector LIMIT 40
),
txt AS (
  SELECT id, ROW_NUMBER() OVER (
           ORDER BY content_bm25 <&> to_bm25query('memories_default_bm25', tokenize($3::text, $6::text))
         ) AS r
  FROM memories
  WHERE namespace = $2 AND invalid_at IS NULL AND tier = 'hot' AND content_bm25 IS NOT NULL
  ORDER BY content_bm25 <&> to_bm25query('memories_default_bm25', tokenize($3::text, $6::text))
  LIMIT 40
)
SELECT m.id, m.content, m.network, m.memory_type, COALESCE(m.source_kind,''),
       COALESCE(m.source_ref,''), m.importance, m.valid_at,
       COALESCE(1.0/(60+vec.r),0) + COALESCE(1.0/(60+txt.r),0) + 0.01 * m.importance AS score
FROM memories m
LEFT JOIN vec ON vec.id = m.id
LEFT JOIN txt ON txt.id = m.id
WHERE (vec.id IS NOT NULL OR txt.id IS NOT NULL) AND m.importance >= $5
ORDER BY score DESC LIMIT $4;`

// recallVecSQL is the vector-only fallback used when the BM25 layer is absent
// (recallSQL errors referencing content_bm25 / to_bm25query / the tokenizer). Same
// columns and salience nudge as recallSQL so the Go side is identical. It does not
// use the query text, so its args are ($1 embedding, $2 namespace, $3 limit,
// $4 min importance).
const recallVecSQL = `
SELECT id, content, network, memory_type, COALESCE(source_kind,''),
       COALESCE(source_ref,''), importance, valid_at,
       (1 - (embedding <=> $1::vector)) + 0.15 * importance AS score
FROM memories
WHERE namespace = $2 AND invalid_at IS NULL AND tier = 'hot'
      AND embedding IS NOT NULL AND importance >= $4
ORDER BY embedding <=> $1::vector
LIMIT $3;`
