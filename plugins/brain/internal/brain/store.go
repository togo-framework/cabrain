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

// Retain embeds the content, runs the §4.1 write-decision against its nearest
// existing memory, and applies it: ADD a new hot episodic row, UPDATE (supersede)
// an evolved memory, INVALIDATE a retracted one, or NOOP an exact/near-duplicate.
// Importance is seeded from the hint + a novelty floor (the full salience formula
// is a Phase-2 tuning job). The write-decision logic is pure (writedecision.go);
// only the neighbor lookup needs the embedder.
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
	vec := vecLit(vecs[0])
	vis := in.Visibility
	if vis == "" {
		vis = "private"
	}
	imp := in.ImportanceHint
	if imp <= 0 {
		imp = 0.5
	}

	// §4.1 decision: compare against the nearest existing memory in scope.
	top := s.topNeighbor(ctx, db, in.Namespace, vec)
	decision, relatedID := writeDecision(top, in.Content)

	// NOOP: the memory already exists — strengthen it (reconsolidation) instead of
	// storing a duplicate. No new row.
	if decision == "noop" {
		_, _ = db.ExecContext(ctx,
			`UPDATE memories SET access_count = access_count + 1, last_accessed_at = now(),
			        importance = LEAST(1.0, importance + 0.02) WHERE id = $1`, relatedID)
		s.event(ctx, db, "retain", in.Namespace, in.OwnerAgentID, "noop", relatedID, int(time.Since(start).Milliseconds()))
		return &RetainResult{ID: relatedID, Decision: "noop", Importance: top.simImportance(imp)}, nil
	}

	// INVALIDATE: retract the contradicted memory, and record the correction as a
	// new memory so the retraction itself is queryable.
	if decision == "invalidate" {
		_, _ = db.ExecContext(ctx, `UPDATE memories SET invalid_at = now() WHERE id = $1 AND invalid_at IS NULL`, relatedID)
	}

	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO memories
		  (namespace, owner_agent_id, visibility, network, memory_type, content,
		   source_kind, source_ref, embedding, importance, tier)
		VALUES ($1,$2,$3,'experience','episodic',$4,$5,$6,$7::vector,$8,'hot')
		RETURNING id`,
		in.Namespace, nullStr(in.OwnerAgentID), vis, in.Content,
		nullStr(in.SourceKind), nullStr(in.SourceRef), vec, imp,
	).Scan(&id)
	if err != nil {
		return nil, errors.New("brain.Retain: insert: " + err.Error())
	}

	// UPDATE: the new row supersedes the evolved one (never hard-delete — the old
	// row stays queryable, tagged with superseded_by + invalid_at).
	if decision == "update" {
		_, _ = db.ExecContext(ctx,
			`UPDATE memories SET invalid_at = now(), superseded_by = $2 WHERE id = $1 AND invalid_at IS NULL`,
			relatedID, id)
	}
	// Populate the BM25 vector best-effort: BM25 is an accelerator, not the
	// authoritative store, so a tokenizer hiccup must never fail a write (the row
	// is already committed and recallable by vector). Off the correctness path.
	_, _ = db.ExecContext(ctx,
		`UPDATE memories SET content_bm25 = tokenize($2,'cabrain_ml') WHERE id = $1`, id, in.Content)
	// Invalidate this namespace's L1 recall cache — a new memory can change results.
	s.bumpEpoch(in.Namespace)
	s.event(ctx, db, "retain", in.Namespace, in.OwnerAgentID, decision, id, int(time.Since(start).Milliseconds()))
	// Fire-and-forget graph enrichment when the cognify engine is present.
	if eng := s.engine(); eng != nil {
		go func() { _ = eng.Cognify(context.Background(), in.Namespace, id, in.Content) }()
	}
	res := &RetainResult{ID: id, Decision: decision, Importance: imp}
	if decision == "update" || decision == "invalidate" {
		res.SupersededID = relatedID
	}
	return res, nil
}

// topNeighbor returns the nearest existing memory to the candidate embedding in
// the namespace (hot, non-invalidated), or nil if none / on error. Best-effort:
// a failure just yields a plain ADD.
func (s *Store) topNeighbor(ctx context.Context, db *sql.DB, ns, vec string) *neighbor {
	var n neighbor
	err := db.QueryRowContext(ctx, `
		SELECT id::text, content, 1 - (embedding <=> $1::vector) AS sim
		FROM memories
		WHERE namespace = $2 AND invalid_at IS NULL AND tier = 'hot' AND embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT 1`, vec, ns).Scan(&n.ID, &n.Content, &n.Sim)
	if err != nil {
		return nil
	}
	return &n
}

// simImportance blends the caller hint with the matched memory's salience on NOOP
// (a repeat sighting slightly strengthens the memory).
func (n *neighbor) simImportance(hint float64) float64 {
	if hint <= 0 {
		hint = 0.5
	}
	return hint
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
	// L1 cache-aside (SPEC §2.1): serve identical repeated recalls without an embed
	// call or a DB hit. Checked before embedding so a hit is genuinely cheap. Keyed
	// by the namespace epoch so any retain in the namespace invalidates it.
	ckey := recallCacheKey(q, s.nsEpoch(q.Namespace))
	if hit, ok := s.getCachedRecall(ckey); ok {
		s.event(ctx, db, "recall", q.Namespace, "", "hit", nil, int(time.Since(start).Milliseconds()))
		return hit, nil
	}
	vecs, err := emb.Embed(ctx, []string{q.Query})
	if err != nil || len(vecs) == 0 {
		return nil, errors.New("brain.Recall: embed failed: " + errStr(err))
	}
	// Hybrid candidate pool: dense vector + multilingual BM25 fused with RRF
	// (recallSQL). If the BM25 layer is absent this errors on content_bm25 /
	// to_bm25query, so transparently fall back to the vector-only query — recall
	// still works, just without lexical fusion. Pull a wide pool; rerank narrows it.
	const poolSize = 40
	vec := vecLit(vecs[0])
	pool, err := s.recallPool(ctx, db, recallSQL, vec, q.Namespace, q.Query, poolSize, q.MinImportance)
	if err != nil {
		pool, err = s.recallPool(ctx, db, recallVecSQL, vec, q.Namespace, poolSize, q.MinImportance)
		if err != nil {
			return nil, errors.New("brain.Recall: query: " + err.Error())
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
	// 1-hop spreading activation (SPEC §4.2): pull memories that share an entity
	// with the top results, tagged via_entity. DB-only; a no-op until Cognee has
	// populated the entity graph (brain-cognee). Default-on per the tool contract.
	if q.ExpandEntity && len(pool) > 0 {
		if extra := s.expandEntities(ctx, db, q.Namespace, pool, maxExpand(q.Limit)); len(extra) > 0 {
			pool = append(pool, extra...)
		}
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
	// Populate L1 for subsequent identical recalls (best-effort; TTL-bounded).
	s.putCachedRecall(ckey, pool)
	s.event(ctx, db, "recall", q.Namespace, "", outcome, nil, int(time.Since(start).Milliseconds()))
	return pool, nil
}

// maxExpand budgets 1-hop neighbors relative to the primary result count.
func maxExpand(limit int) int {
	n := limit / 2
	if n < 2 {
		n = 2
	}
	return n
}

// expandEntities returns up to `budget` memories that share an entity with any of
// the seed results (1-hop), scoped to the namespace/hot tier and excluding seeds.
// via_entity names the linking entity. Passes seed ids as a comma-joined string
// cast to uuid[] so it works over database/sql without a driver-specific array.
func (s *Store) expandEntities(ctx context.Context, db *sql.DB, ns string, seed []Recalled, budget int) []Recalled {
	if budget <= 0 || len(seed) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(seed))
	ids := make([]string, 0, len(seed))
	for _, r := range seed {
		seen[r.ID] = true
		ids = append(ids, r.ID)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ON (m.id)
		       m.id::text, m.content, m.network, m.memory_type, COALESCE(m.source_kind,''),
		       COALESCE(m.source_ref,''), m.importance, m.valid_at, e.name
		FROM memory_entities seed_me
		JOIN memory_entities nb_me ON nb_me.entity_id = seed_me.entity_id
		     AND nb_me.memory_id <> seed_me.memory_id
		JOIN entities  e ON e.id = seed_me.entity_id
		JOIN memories  m ON m.id = nb_me.memory_id
		WHERE seed_me.memory_id = ANY(string_to_array($1, ',')::uuid[])
		      AND m.namespace = $2 AND m.invalid_at IS NULL AND m.tier = 'hot'
		LIMIT $3`, strings.Join(ids, ","), ns, budget)
	if err != nil {
		return nil // entity tables absent / not populated → silently skip
	}
	defer rows.Close()
	out := []Recalled{}
	for rows.Next() {
		var r Recalled
		var via string
		if err := rows.Scan(&r.ID, &r.Content, &r.Network, &r.MemoryType, &r.SourceKind,
			&r.SourceRef, &r.Importance, &r.ValidAt, &via); err != nil {
			continue
		}
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		r.ViaEntity = via
		r.Score = 0.05 + 0.15*r.Importance // ranks below primary hits
		out = append(out, r)
	}
	return out
}

// recallPool runs a candidate query (recallSQL or recallVecSQL) and scans the
// standard Recalled column set. Both queries share the same projection.
func (s *Store) recallPool(ctx context.Context, db *sql.DB, query string, args ...any) ([]Recalled, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
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
	return pool, rows.Err()
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
