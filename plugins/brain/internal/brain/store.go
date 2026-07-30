package brain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/togo-framework/togo"
	"strconv"
	"strings"
	"time"
)

// classifyMemory picks the (network, memory_type) pair for a write.
//
// The schema DESIGNS a taxonomy — memory_type ∈ episodic|semantic|procedural|
// working and network ∈ fact|experience|observation|belief, both CHECK-constrained
// — but the insert used to hardcode 'experience','episodic' for every row. So both
// columns carried exactly one value each (verified: 5,329/5,329 flowos rows
// 'episodic'/'experience') while the real distinction lived in metadata->>'type'.
// Two dedicated typed columns that say nothing, and the meaningful axis buried in
// JSON.
//
// They are genuinely different axes and both are worth having:
//
//	memory_type = the COGNITIVE class, which consolidation/demotion policy keys on
//	              (a venture description is a durable fact; a post is an event).
//	metadata.type = the DOMAIN type (post, issue, venture…), used for filtering.
//
// A caller may state either explicitly; otherwise derive from the domain type,
// falling back to source_kind when a brain stores no type at all.
//
// The vocabulary below is deliberately broad because "episodic" is the DEFAULT,
// and defaulting wrongly is the expensive direction: a first pass that only knew
// the FlowOS types left 2,749 `doc` rows, 838 `knowledge`, and the whole avo
// board/kickstart/radar corpus filed as dated events when they are reference
// material that never "happened" at all.
func classifyMemory(in MemoryInput) (network, memType string) {
	network, memType = in.Network, in.MemoryType
	domain, _ := in.Metadata["type"].(string)
	if memType == "" {
		memType = classifyKind(domain)
	}
	if memType == "" { // brains that store no metadata.type — infer from provenance
		memType = classifyKind(in.SourceKind)
	}
	if memType == "" {
		memType = "episodic" // a dated occurrence: post, issue, release, commit…
	}
	if network == "" {
		switch memType {
		case "semantic":
			network = "fact"
		case "procedural":
			network = "fact"
		default:
			network = "experience"
		}
	}
	// Never write a value the CHECK constraint would reject.
	if !validMemType[memType] {
		memType = "episodic"
	}
	if !validNetwork[network] {
		network = "experience"
	}
	return network, memType
}

// classifyKind maps a domain type OR a source_kind to a cognitive class, or ""
// when it recognises neither. Matching is on the leading token so families like
// "datasource:markdown" and "flowos_learning" resolve without listing every
// variant.
func classifyKind(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	for _, sep := range []string{":", "/"} { // datasource:markdown → datasource
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
		}
	}
	s = strings.TrimPrefix(s, "flowos_")
	switch s {
	// SEMANTIC — durable descriptions and reference material. Things that ARE.
	case "venture", "person", "portfolio", "agent", "position", "canvas",
		"org-rollup", "alias", "spec", "learning", "dependency", "channel",
		"responsibility", "infra", "code", "codebase",
		"doc", "docs", "knowledge", "reference", "taxonomy", "note", "guide",
		"faq", "research", "thesis", "board", "kickstart", "radar", "design",
		"rules", "decision", "definition", "glossary", "profile", "system-note":
		return "semantic"
	// PROCEDURAL — how to do something.
	case "workflow", "playbook", "roadmap", "transition", "plan", "runbook",
		"checklist", "sop", "howto", "drill", "exercise", "procedure", "recipe":
		return "procedural"
	// EPISODIC — dated occurrences. Listed explicitly so the default stays honest.
	case "post", "issue", "goal", "release", "calendar-event", "transcript",
		"incident", "task", "approval", "pipeline", "okr", "metric", "budget",
		"activity-score", "session-topic", "harvested", "marketing",
		"git-activity", "claude-activity", "chat-session", "deployment", "item":
		return "episodic"
	}
	return ""
}

var validMemType = map[string]bool{"episodic": true, "semantic": true, "procedural": true, "working": true}
var validNetwork = map[string]bool{"fact": true, "experience": true, "observation": true, "belief": true}

// metaJSON marshals a metadata map to a JSON string for the jsonb column (nil → NULL).
func metaJSON(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return string(b)
}

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
	Metadata       map[string]any // free-form; stored as jsonb (type, slug, tags, …)

	// ValidAt is the EVENT time of the memory (when the thing happened), not the
	// ingest time. Nil → now(), which is right for a live observation but WRONG
	// for backfilled records: without it every imported post/goal/issue lands with
	// the same timestamp and "what shipped last week?" becomes unanswerable — the
	// store has no way to order them. Set it whenever you are importing something
	// that already has a creation date at the source.
	ValidAt *time.Time

	// Network / MemoryType let a caller state the cognitive classification
	// explicitly. Empty → derived from Metadata["type"] by classifyMemory.
	Network    string // fact | experience | observation | belief
	MemoryType string // episodic | semantic | procedural | working
}

// UnmarshalJSON accepts BOTH camelCase and snake_case keys. The struct has no
// json tags, so Go's case-insensitive matching binds "sourceKind" but silently
// DROPS "source_kind" — which is what the cabrain-agents client sends, so every
// memory an agent retained lost its provenance. Normalising here fixes that
// without breaking the camelCase callers (cmd/brain-mcp) already in the field.
func (m *MemoryInput) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	norm := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		norm[strings.ToLower(strings.ReplaceAll(k, "_", ""))] = v
	}
	get := func(name string, dst any) {
		if v, ok := norm[name]; ok {
			_ = json.Unmarshal(v, dst)
		}
	}
	get("namespace", &m.Namespace)
	get("content", &m.Content)
	get("sourcekind", &m.SourceKind)
	get("sourceref", &m.SourceRef)
	get("visibility", &m.Visibility)
	get("importancehint", &m.ImportanceHint)
	get("owneragentid", &m.OwnerAgentID)
	get("metadata", &m.Metadata)
	get("validat", &m.ValidAt)
	get("network", &m.Network)
	get("memorytype", &m.MemoryType)
	return nil
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
	// Secrets-first: move any API keys / passwords / .env values / connection
	// strings / private keys OUT of the content into the per-brain vault, and keep
	// only the redacted `[secret:<name>]` reference. This runs BEFORE embedding so a
	// raw secret never enters the vector index or a recall response. Best-effort —
	// on any vault error the original content is kept unchanged (nothing dropped).
	if red, names := s.captureSecrets(ctx, in.Namespace, in.Content, in.SourceRef, in.OwnerAgentID); len(names) > 0 {
		in.Content = red
		if in.Metadata == nil {
			in.Metadata = map[string]any{}
		}
		in.Metadata["hasSecrets"] = true
		in.Metadata["secrets"] = names
	}
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
	//
	// EXCEPT when the caller supplied a source_ref. A source_ref is an IDENTITY
	// ("db:release:42"), not a similarity hint: that row is the same record, and a
	// different row is a different record no matter how alike the text is. Running
	// the similarity heuristic on imports is lossy — backfilling FlowOS collapsed
	// 108 releases into 2 and 226 roadmaps into 43, because rows that differ only
	// by version/date embed above the 0.97 NOOP threshold. So when the ref already
	// exists we supersede exactly that row, and when it doesn't we always ADD.
	var decision, relatedID string
	var top *neighbor
	if strings.TrimSpace(in.SourceRef) != "" {
		var existing string
		err := db.QueryRowContext(ctx,
			`SELECT id::text FROM memories
			 WHERE namespace = $1 AND source_ref = $2 AND invalid_at IS NULL
			 ORDER BY valid_at DESC LIMIT 1`, in.Namespace, in.SourceRef).Scan(&existing)
		switch {
		case err == nil && existing != "":
			decision, relatedID = "update", existing
		default:
			decision = "add"
		}
	} else {
		top = s.topNeighbor(ctx, db, in.Namespace, vec)
		decision, relatedID = writeDecision(top, in.Content)
	}

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

	// valid_at is the partition key, so the event time has to be supplied at INSERT
	// (moving a row between partitions later is expensive). NULL → now().
	var validAt any
	if in.ValidAt != nil && !in.ValidAt.IsZero() {
		validAt = *in.ValidAt
	}
	network, memType := classifyMemory(in)
	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO memories
		  (namespace, owner_agent_id, visibility, network, memory_type, content,
		   source_kind, source_ref, embedding, importance, tier, metadata, valid_at)
		VALUES ($1,$2,$3,$11,$12,$4,$5,$6,$7::vector,$8,'hot',
		        COALESCE($9::jsonb,'{}'::jsonb), COALESCE($10::timestamptz, now()))
		RETURNING id`,
		in.Namespace, nullStr(in.OwnerAgentID), vis, in.Content,
		nullStr(in.SourceKind), nullStr(in.SourceRef), vec, imp, metaJSON(in.Metadata), validAt,
		network, memType,
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
		`UPDATE memories SET content_bm25 = tokenize($2,$3) WHERE id = $1`, id, in.Content, bm25Tokenizer())
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
	Namespace string `json:"namespace"`
	Query     string `json:"query"`
	Limit     int    `json:"limit"` // final N after rerank (default 8)
	// ExpandEntity turns on 1-hop spreading activation over the entity graph.
	// DEFAULT ON: a JSON body that omits the key gets true (see UnmarshalJSON).
	// It used to default to false for REST callers while only the internal chat/
	// agent paths opted in, so POST /api/brain/recall — the endpoint the MCP tools
	// and cabrain-agents actually use — never touched the graph at all. Send
	// "expandEntity": false to opt out.
	ExpandEntity  bool    `json:"expandEntity"`
	MinImportance float64 `json:"minImportance"`
	// Types narrows candidates to these metadata->>'type' DOMAIN types — "post",
	// "venture", "issue" … NOT the memory_type column (which is the cognitive
	// class: episodic/semantic/procedural). Empty = no filter.
	// Use to scope recall semantically, e.g. Types=["venture","spec","goal"] when
	// the caller wants project descriptions rather than commit-count episodics.
	Types []string `json:"types,omitempty"`
	// ExcludeSourceKinds drops candidates whose source_kind is in this list. Use
	// to muffle noisy ingest streams (e.g. flowos_github_activity) that would
	// otherwise dominate the pool.
	ExcludeSourceKinds []string `json:"excludeSourceKinds,omitempty"`

	// ── Temporal (bi-temporal query surface) ──────────────────────────────────
	// Since/Until bound the EVENT time (valid_at) — "what happened last week".
	// AsOf asks what the brain believed at a moment in time: it keeps rows whose
	// validity window contains that instant, INCLUDING ones later superseded, so
	// history is queryable rather than only the current truth.
	// OrderBy "recent"/"oldest" replaces relevance ranking with time ordering,
	// which is what "the latest N feeds" actually needs — similarity alone will
	// happily hand back an April meeting for "this week".
	Since   *time.Time `json:"since,omitempty"`
	Until   *time.Time `json:"until,omitempty"`
	AsOf    *time.Time `json:"asOf,omitempty"`
	OrderBy string     `json:"orderBy,omitempty"` // ""|relevance | recent | oldest
}

// UnmarshalJSON decodes a RecallQuery with ExpandEntity defaulting to TRUE when
// the caller omits it. Plain struct decoding gives the zero value (false), which
// silently disabled entity expansion for every REST/MCP caller.
func (q *RecallQuery) UnmarshalJSON(b []byte) error {
	type alias RecallQuery // avoid recursing into this method
	tmp := struct {
		ExpandEntity *bool `json:"expandEntity"`
		*alias
	}{alias: (*alias)(q)}
	if err := json.Unmarshal(b, &tmp); err != nil {
		return err
	}
	q.ExpandEntity = tmp.ExpandEntity == nil || *tmp.ExpandEntity
	return nil
}

type Recalled struct {
	ID         string    `json:"id"`
	Namespace  string    `json:"namespace,omitempty"` // set by cross-brain SearchAll
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
	sqlHybrid, argsHybrid := buildFilteredRecallSQL(recallSQL, 6, q.Types, q.ExcludeSourceKinds, &q)
	hybridArgs := append([]any{vec, q.Namespace, q.Query, poolSize, q.MinImportance, bm25Tokenizer()}, argsHybrid...)
	pool, err := s.recallPool(ctx, db, sqlHybrid, hybridArgs...)
	if err != nil {
		sqlVec, argsVec := buildFilteredRecallSQL(recallVecSQL, 4, q.Types, q.ExcludeSourceKinds, &q)
		vecArgs := append([]any{vec, q.Namespace, poolSize, q.MinImportance}, argsVec...)
		pool, err = s.recallPool(ctx, db, sqlVec, vecArgs...)
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
	// Time ordering, when the caller asked for it. Applied AFTER rerank so the
	// semantic pass still chooses WHICH memories are relevant and the clock only
	// decides the order — "the latest 5 posts about X", not "5 arbitrary recent rows".
	switch strings.ToLower(q.OrderBy) {
	case "recent":
		sortByValidAt(pool, true)
	case "oldest":
		sortByValidAt(pool, false)
	}
	// Bump access stats for what we surfaced (best-effort) + emit the event.
	for _, r := range pool {
		_, _ = db.ExecContext(ctx,
			`UPDATE memories SET access_count = access_count + 1, last_accessed_at = now() WHERE id = $1`, r.ID)
	}
	outcome := "hit"
	if len(pool) == 0 {
		outcome = "empty"
		// A miss: record it as a knowledge gap the operator can index later.
		s.recordGap(ctx, db, q.Namespace, q.Query)
	}
	// Populate L1 for subsequent identical recalls (best-effort; TTL-bounded).
	s.putCachedRecall(ckey, pool)
	s.event(ctx, db, "recall", q.Namespace, "", outcome, nil, int(time.Since(start).Milliseconds()))
	return pool, nil
}

// sortByValidAt orders results by event time (newest first when desc).
func sortByValidAt(rs []Recalled, desc bool) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0; j-- {
			newer := rs[j].ValidAt.After(rs[j-1].ValidAt)
			if (desc && !newer) || (!desc && newer) {
				break
			}
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
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
// It runs inside a read-only transaction so `SET LOCAL hnsw.ef_search` applies
// to this query alone (see hnswEFSearch: without it the ANN candidate list is
// gutted by the invalid_at/tier post-filter and recall silently returns few or
// zero rows) and is reverted on commit — no pooled connection is left mutated.
func (s *Store) recallPool(ctx context.Context, db *sql.DB, query string, args ...any) ([]Recalled, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // read-only tx: rollback is the normal exit
	// Best-effort: a Postgres without pgvector's GUC just errors here and the
	// query still runs (this only affects candidate depth, never correctness).
	_, _ = tx.ExecContext(ctx, "SET LOCAL hnsw.ef_search = "+strconv.Itoa(hnswEFSearch()))
	rows, err := tx.QueryContext(ctx, query, args...)
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

// buildFilteredRecallSQL splices optional type / exclude-source-kind filters
// into the base recallSQL / recallVecSQL, returning the modified SQL and the
// extra args to append after the base positional args. baseArgCount is the
// number of positional args the base SQL already consumes ($1..$baseArgCount);
// new filters get $baseArgCount+1 onwards.
//
// Type filter targets metadata->>'type' — the JSON field set by ingest (person,
// venture, goal, spec, …) that admin.go's brain_details also groups on. NOT the
// enum memory_type column (which is episodic/semantic/procedural — too coarse
// to distinguish a person memory from a git-activity one).
//
// Filters are injected into every CTE that scans memories, so pre-rerank
// candidates are already narrowed. Zero-cost when both filters are empty.
func buildFilteredRecallSQL(base string, baseArgCount int, types, exclude []string, q *RecallQuery) (string, []any) {
	temporal := q != nil && (q.Since != nil || q.Until != nil || q.AsOf != nil)
	if len(types) == 0 && len(exclude) == 0 && !temporal {
		return base, nil
	}
	var extraArgs []any
	next := baseArgCount + 1
	injection := ""
	if len(types) > 0 {
		phs := make([]string, len(types))
		for i, t := range types {
			phs[i] = fmt.Sprintf("$%d", next)
			extraArgs = append(extraArgs, t)
			next++
		}
		injection += " AND COALESCE(NULLIF(metadata->>'type',''),'item') IN (" + strings.Join(phs, ",") + ")"
	}
	if len(exclude) > 0 {
		phs := make([]string, len(exclude))
		for i, s := range exclude {
			phs[i] = fmt.Sprintf("$%d", next)
			extraArgs = append(extraArgs, s)
			next++
		}
		injection += " AND COALESCE(source_kind,'') NOT IN (" + strings.Join(phs, ",") + ")"
	}
	if temporal {
		if q.Since != nil {
			injection += fmt.Sprintf(" AND valid_at >= $%d", next)
			extraArgs = append(extraArgs, *q.Since)
			next++
		}
		if q.Until != nil {
			injection += fmt.Sprintf(" AND valid_at <= $%d", next)
			extraArgs = append(extraArgs, *q.Until)
			next++
		}
		if q.AsOf != nil {
			// Point-in-time: what the brain held true at that instant. Deliberately
			// admits rows since superseded — the base SQL's `invalid_at IS NULL`
			// only ever shows CURRENT truth, which cannot answer "as of March".
			injection += fmt.Sprintf(" AND valid_at <= $%d AND (invalid_at IS NULL OR invalid_at > $%d)", next, next)
			extraArgs = append(extraArgs, *q.AsOf)
			next++
		}
	}
	anchor := "tier = 'hot'"
	out := strings.ReplaceAll(base, anchor, anchor+injection)
	if q != nil && q.AsOf != nil {
		// Relax the hard-coded current-truth predicate so the AsOf window governs.
		out = strings.ReplaceAll(out, "invalid_at IS NULL AND tier = 'hot'", "tier = 'hot'")
		out = strings.ReplaceAll(out, "invalid_at IS NULL AND tier='hot'", "tier='hot'")
	}
	return out, extraArgs
}
