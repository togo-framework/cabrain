package brain

// Public provider contract for driver plugins (brain-tei, brain-cognee, …).
// External plugins import github.com/togo-framework/brain and register their
// implementation on the kernel; brain reads it lazily on the hot path. This
// re-exports the internal contract so external code never imports internal/.

import (
	"context"
	"database/sql"

	"github.com/togo-framework/togo"

	ib "github.com/togo-framework/brain/internal/brain"
)

// Provider interfaces (aliases to the internal contract).
type (
	Embedder = ib.Embedder
	Reranker = ib.Reranker
	Engine   = ib.Engine
)

// RegisterEmbedder publishes the embeddings driver (e.g. brain-tei) onto the kernel.
func RegisterEmbedder(k *togo.Kernel, e Embedder) { ib.RegisterEmbedder(k, e) }

// RegisterReranker publishes the rerank driver (e.g. brain-tei) onto the kernel.
func RegisterReranker(k *togo.Kernel, r Reranker) { ib.RegisterReranker(k, r) }

// RegisterEngine publishes the cognify-engine driver (e.g. brain-cognee) onto the kernel.
func RegisterEngine(k *togo.Kernel, e Engine) { ib.RegisterEngine(k, e) }

// --- Schema / migration surface (for ops tooling, e.g. cmd/brainctl) ----------

// Migrate applies the brain schema (SPEC §3) then the BM25 layer against db.
func Migrate(ctx context.Context, db *sql.DB) error { return ib.Migrate(ctx, db) }

// ApplyBM25 applies just the vchord_bm25 tokenizer/column/index (idempotent).
func ApplyBM25(ctx context.Context, db *sql.DB) error { return ib.ApplyBM25(ctx, db) }

// ErrBM25Skipped marks a non-fatal BM25-layer failure during Migrate (schema
// applied; recall degrades to vector-only). Distinguish with errors.Is.
var ErrBM25Skipped = ib.ErrBM25Skipped

// SchemaSQL / BM25SQL expose the embedded DDL for inspection.
func SchemaSQL() string { return ib.SchemaSQL() }
func BM25SQL() string   { return ib.BM25SQL() }
