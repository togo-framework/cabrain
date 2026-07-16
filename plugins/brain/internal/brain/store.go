package brain

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/togo-framework/togo"
)

// ErrNoEmbedder is returned when a path needs embeddings but no Embedder driver
// (brain-tei) is registered.
var ErrNoEmbedder = errors.New("brain: no embedder registered — install the brain-tei plugin")

// Store is the brain's data layer over togo-postgres. It acquires the *sql.DB
// lazily from the kernel (so plugin boot never fails when the DB is not yet
// configured — Blocker B), mirroring the togo cache driver.
type Store struct {
	k    *togo.Kernel
	prov providers
}

func newStore(k *togo.Kernel) *Store { return &Store{k: k} }

func (s *Store) db(ctx context.Context) (*sql.DB, error) { return s.k.SQL(ctx) }

// Migrate applies the embedded schema against the configured database.
func (s *Store) Migrate(ctx context.Context) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return Migrate(ctx, db)
}

// --- retain (SPEC §4.1) -------------------------------------------------------

// MemoryInput is one write into the brain.
type MemoryInput struct {
	Namespace      string
	Content        string
	SourceKind     string
	SourceRef      string
	Visibility     string  // private|team|global ("" → private)
	ImportanceHint float64 // optional caller salience flag, blended, not authoritative
	OwnerAgentID   string
}

// RetainResult reports what the write pipeline decided.
type RetainResult struct {
	ID           string
	Decision     string // add|update|invalidate|noop
	Importance   float64
	SupersededID string
}

// Retain runs the write pipeline: embed → recall neighbors → ADD/UPDATE/
// INVALIDATE/NOOP decision → compute importance → insert episodic/hot → entity
// graph → event. Requires an Embedder (brain-tei). The neighbor write-decision
// (Engine/LLM) and importance formula land as those providers are wired.
func (s *Store) Retain(ctx context.Context, in MemoryInput) (*RetainResult, error) {
	if s.prov.embedder == nil {
		return nil, ErrNoEmbedder
	}
	// TODO(phase1): embed → neighbor recall → write-decision → importance →
	// INSERT into memories (network='experience', memory_type='episodic',
	// tier='hot') → Engine.Cognify → memory_events(op='retain'). Wired once the
	// cabrain DB + brain-tei are live (Blocker B).
	return nil, errors.New("brain.Retain: not yet wired (needs live DB + brain-tei)")
}

// --- recall (SPEC §4.2) -------------------------------------------------------

// RecallQuery is a hybrid retrieval request.
type RecallQuery struct {
	Namespace     string
	Query         string
	Limit         int  // final N after rerank (default 8)
	ExpandEntity  bool // 1-hop spreading activation (default true)
	MinImportance float64
}

// Recalled is one returned memory (provenance included — Gate 1).
type Recalled struct {
	ID         string
	Content    string
	Score      float64
	Network    string
	MemoryType string
	SourceKind string
	SourceRef  string
	Importance float64
	ValidAt    time.Time
	ViaEntity  string // set when surfaced via 1-hop expansion
}

// Recall runs the hot-tier hybrid query (recallSQL), then rerank + optional
// 1-hop entity expansion, and bumps access stats. N1: no inline LLM, no cold
// tier. Requires an Embedder for the query vector.
func (s *Store) Recall(ctx context.Context, q RecallQuery) ([]Recalled, error) {
	if s.prov.embedder == nil {
		return nil, ErrNoEmbedder
	}
	if q.Limit <= 0 {
		q.Limit = 8
	}
	// TODO(phase1): embed query → run recallSQL($vec,$ns,$text,$limit*k) →
	// rerank top-20 (Reranker) → optional 1-hop via memory_entities → bump
	// access_count/last_accessed_at → memory_events(op='recall'). Wired once the
	// cabrain DB + brain-tei are live (Blocker B).
	return nil, errors.New("brain.Recall: not yet wired (needs live DB + brain-tei)")
}

// --- provider registration (driver plugins call these) ------------------------

// UseEmbedder registers the embeddings driver (brain-tei).
func (s *Store) UseEmbedder(e Embedder) { s.prov.embedder = e }

// UseReranker registers the rerank driver (brain-tei).
func (s *Store) UseReranker(r Reranker) { s.prov.reranker = r }

// UseEngine registers the cognify engine driver (brain-cognee).
func (s *Store) UseEngine(e Engine) { s.prov.engine = e }
