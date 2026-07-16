package brain

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/togo-framework/togo"
)

// ErrNoEmbedder is returned when a path needs embeddings but no Embedder driver
// (brain-tei) is registered.
var ErrNoEmbedder = errors.New("brain: no embedder registered — install the brain-tei plugin")

// Store is the brain's data layer over togo-postgres. It acquires the *sql.DB
// and the provider drivers lazily from the kernel (so plugin boot never depends
// on ordering or on the DB being configured), mirroring the togo cache driver.
type Store struct{ k *togo.Kernel }

func newStore(k *togo.Kernel) *Store { return &Store{k: k} }

func (s *Store) db(ctx context.Context) (*sql.DB, error) { return s.k.SQL(ctx) }

// Lazy provider lookups (published by brain-tei / brain-cognee).
func (s *Store) embedder() Embedder { v, _ := s.k.Get(keyEmbedder); e, _ := v.(Embedder); return e }
func (s *Store) reranker() Reranker { v, _ := s.k.Get(keyReranker); r, _ := v.(Reranker); return r }
func (s *Store) engine() Engine     { v, _ := s.k.Get(keyEngine); e, _ := v.(Engine); return e }

// Migrate applies the embedded schema against the configured database.
func (s *Store) Migrate(ctx context.Context) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return Migrate(ctx, db)
}

// vecLit formats a float32 vector as a pgvector text literal: [a,b,c].
func vecLit(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func (s *Store) event(ctx context.Context, db *sql.DB, op, ns, agent, outcome string, memID any, ms int) {
	_, _ = db.ExecContext(ctx,
		`INSERT INTO memory_events (namespace, op, memory_id, agent_id, latency_ms, metadata)
		 VALUES ($1,$2,$3,$4,$5, jsonb_build_object('outcome',$6::text))`,
		ns, op, memID, nullStr(agent), ms, outcome)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// --- retain (SPEC §4.1) -------------------------------------------------------

type MemoryInput struct {
	Namespace      string
	Content        string
	SourceKind     string
	SourceRef      string
	Visibility     string  // private|team|global ("" → private)
	ImportanceHint float64 // optional caller salience flag, blended, not authoritative
	OwnerAgentID   string
}

type RetainResult struct {
	ID           string  `json:"id"`
	Decision     string  `json:"decision"` // add|update|invalidate|noop
	Importance   float64 `json:"importance"`
	SupersededID string  `json:"supersededId,omitempty"`
}

// Retain embeds the content and stores it as a hot episodic memory. The Mem0-style
// ADD/UPDATE/INVALIDATE/NOOP write-decision and the full salience formula land as
// the Engine/LLM provider is wired (SPEC §4.1); for now every write is an ADD with
// importance seeded from the hint + a novelty floor.
func (s *Store) Retain(ctx context.Context, in MemoryInput) (*RetainResult, error) {
	emb := s.embedder()
	if emb == nil {
		return nil, ErrNoEmbedder
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	vecs, err := emb.Embed(ctx, []string{in.Content})
	if err != nil || len(vecs) == 0 {
		return nil, errors.New("brain.Retain: embed failed: " + errStr(err))
	}
	vis := in.Visibility
	if vis == "" {
		vis = "private"
	}
	imp := in.ImportanceHint
	if imp <= 0 {
		imp = 0.5
	}
	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO memories
		  (namespace, owner_agent_id, visibility, network, memory_type, content,
		   source_kind, source_ref, embedding, importance, tier)
		VALUES ($1,$2,$3,'experience','episodic',$4,$5,$6,$7::vector,$8,'hot')
		RETURNING id`,
		in.Namespace, nullStr(in.OwnerAgentID), vis, in.Content,
		nullStr(in.SourceKind), nullStr(in.SourceRef), vecLit(vecs[0]), imp,
	).Scan(&id)
	if err != nil {
		return nil, errors.New("brain.Retain: insert: " + err.Error())
	}
	s.event(ctx, db, "retain", in.Namespace, in.OwnerAgentID, "hit", id, int(time.Since(start).Milliseconds()))
	// Fire-and-forget graph enrichment when the cognify engine is present.
	if eng := s.engine(); eng != nil {
		go func() { _ = eng.Cognify(context.Background(), in.Namespace, id, in.Content) }()
	}
	return &RetainResult{ID: id, Decision: "add", Importance: imp}, nil
}

// --- recall (SPEC §4.2) -------------------------------------------------------

type RecallQuery struct {
	Namespace     string  `json:"namespace"`
	Query         string  `json:"query"`
	Limit         int     `json:"limit"`        // final N after rerank (default 8)
	ExpandEntity  bool    `json:"expandEntity"` // 1-hop spreading activation
	MinImportance float64 `json:"minImportance"`
}

type Recalled struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Score      float64   `json:"score"`
	Network    string    `json:"network"`
	MemoryType string    `json:"memoryType"`
	SourceKind string    `json:"sourceKind"`
	SourceRef  string    `json:"sourceRef"`
	Importance float64   `json:"importance"`
	ValidAt    time.Time `json:"validAt"`
	ViaEntity  string    `json:"viaEntity,omitempty"`
}

// Recall runs scoped dense retrieval on the hot tier, then reranks (when a
// Reranker is present). BM25 fusion (vchord_bm25) is an additive enhancement over
// this vector path — see bm25 notes in schema.sql. N1: no inline LLM, no cold tier.
func (s *Store) Recall(ctx context.Context, q RecallQuery) ([]Recalled, error) {
	emb := s.embedder()
	if emb == nil {
		return nil, ErrNoEmbedder
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	if q.Limit <= 0 {
		q.Limit = 8
	}
	start := time.Now()
	vecs, err := emb.Embed(ctx, []string{q.Query})
	if err != nil || len(vecs) == 0 {
		return nil, errors.New("brain.Recall: embed failed: " + errStr(err))
	}
	// Pull a candidate pool (rerank narrows it). Salience nudge folded into order.
	rows, err := db.QueryContext(ctx, `
		SELECT id, content, network, memory_type, COALESCE(source_kind,''),
		       COALESCE(source_ref,''), importance, valid_at,
		       (1 - (embedding <=> $1::vector)) + 0.15 * importance AS score
		FROM memories
		WHERE namespace = $2 AND invalid_at IS NULL AND tier = 'hot'
		      AND embedding IS NOT NULL AND importance >= $3
		ORDER BY embedding <=> $1::vector
		LIMIT 40`,
		vecLit(vecs[0]), q.Namespace, q.MinImportance)
	if err != nil {
		return nil, errors.New("brain.Recall: query: " + err.Error())
	}
	defer rows.Close()
	pool := []Recalled{}
	for rows.Next() {
		var r Recalled
		if err := rows.Scan(&r.ID, &r.Content, &r.Network, &r.MemoryType, &r.SourceKind,
			&r.SourceRef, &r.Importance, &r.ValidAt, &r.Score); err == nil {
			pool = append(pool, r)
		}
	}
	// Rerank the pool with the cross-encoder when available.
	if rr := s.reranker(); rr != nil && len(pool) > 1 {
		docs := make([]string, len(pool))
		for i := range pool {
			docs[i] = pool[i].Content
		}
		if scores, err := rr.Rerank(ctx, q.Query, docs); err == nil && len(scores) == len(pool) {
			for i := range pool {
				pool[i].Score = scores[i]
			}
			sortByScoreDesc(pool)
		}
	}
	if len(pool) > q.Limit {
		pool = pool[:q.Limit]
	}
	// Bump access stats for what we surfaced (best-effort) + emit the event.
	for _, r := range pool {
		_, _ = db.ExecContext(ctx,
			`UPDATE memories SET access_count = access_count + 1, last_accessed_at = now() WHERE id = $1`, r.ID)
	}
	outcome := "hit"
	if len(pool) == 0 {
		outcome = "empty"
	}
	s.event(ctx, db, "recall", q.Namespace, "", outcome, nil, int(time.Since(start).Milliseconds()))
	return pool, nil
}

func sortByScoreDesc(rs []Recalled) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].Score > rs[j-1].Score; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

func errStr(err error) string {
	if err == nil {
		return "empty embedding result"
	}
	return err.Error()
}
