package brain

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"time"
)

// Brain administration: per-namespace details, portable export/import, delete,
// and per-memory edit. A "brain" is a namespace.

// BrainDetail is the detail view for one namespace.
type BrainDetail struct {
	Namespace string         `json:"namespace"`
	Memories  int            `json:"memories"`
	Types     map[string]int `json:"types"`   // metadata->>'type' → count
	Sources   map[string]int `json:"sources"` // source_kind → count
	OpenGaps  int            `json:"openGaps"`
	Recalls   int            `json:"recalls"` // recall events, all time
	FirstAt   *time.Time     `json:"firstAt,omitempty"`
	LastAt    *time.Time     `json:"lastAt,omitempty"`
}

func (s *Store) BrainDetail(ctx context.Context, ns string) (*BrainDetail, error) {
	d := &BrainDetail{Namespace: ns, Types: map[string]int{}, Sources: map[string]int{}}
	db, err := s.db(ctx)
	if err != nil || !s.ready(ctx, db) {
		return d, nil
	}
	var first, last sql.NullTime
	_ = db.QueryRowContext(ctx,
		`SELECT count(*), min(valid_at), max(valid_at) FROM memories WHERE namespace=$1 AND invalid_at IS NULL`, ns).
		Scan(&d.Memories, &first, &last)
	if first.Valid {
		d.FirstAt = &first.Time
	}
	if last.Valid {
		d.LastAt = &last.Time
	}
	groupInto(ctx, db, d.Types,
		`SELECT COALESCE(NULLIF(metadata->>'type',''),'item'), count(*) FROM memories WHERE namespace=$1 AND invalid_at IS NULL GROUP BY 1 ORDER BY 2 DESC`, ns)
	groupInto(ctx, db, d.Sources,
		`SELECT COALESCE(source_kind,'unknown'), count(*) FROM memories WHERE namespace=$1 AND invalid_at IS NULL GROUP BY 1 ORDER BY 2 DESC`, ns)
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM memory_gaps WHERE namespace=$1 AND status='open'`, ns).Scan(&d.OpenGaps)
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM memory_events WHERE namespace=$1 AND op='recall'`, ns).Scan(&d.Recalls)
	return d, nil
}

func groupInto(ctx context.Context, db *sql.DB, m map[string]int, q string, args ...any) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var c int
		if rows.Scan(&k, &c) == nil {
			m[k] = c
		}
	}
}

// ExportRecord is one portable memory row (embedding kept as a pgvector text
// literal so it re-imports without re-embedding — same bge-m3/1024 everywhere).
type ExportRecord struct {
	Namespace  string         `json:"namespace"`
	Content    string         `json:"content"`
	SourceKind string         `json:"sourceKind,omitempty"`
	SourceRef  string         `json:"sourceRef,omitempty"`
	Visibility string         `json:"visibility,omitempty"`
	Network    string         `json:"network,omitempty"`
	MemoryType string         `json:"memoryType,omitempty"`
	Tier       string         `json:"tier,omitempty"`
	Importance float64        `json:"importance"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Embedding  string         `json:"embedding,omitempty"` // "[a,b,…]"
	ValidAt    time.Time      `json:"validAt"`
}

// Export streams a namespace as newline-delimited JSON (one ExportRecord per line).
func (s *Store) Export(ctx context.Context, ns string, w io.Writer) (int, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT namespace, content, COALESCE(source_kind,''), COALESCE(source_ref,''),
		       visibility, network, memory_type, tier, importance,
		       metadata, COALESCE(embedding::text,''), valid_at
		FROM memories WHERE namespace=$1 AND invalid_at IS NULL
		ORDER BY valid_at`, ns)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	enc := json.NewEncoder(w)
	n := 0
	for rows.Next() {
		var r ExportRecord
		var meta []byte
		if err := rows.Scan(&r.Namespace, &r.Content, &r.SourceKind, &r.SourceRef,
			&r.Visibility, &r.Network, &r.MemoryType, &r.Tier, &r.Importance,
			&meta, &r.Embedding, &r.ValidAt); err != nil {
			continue
		}
		r.Metadata = decodeJSONMap(meta)
		if enc.Encode(&r) == nil {
			n++
		}
	}
	return n, nil
}

// Import loads NDJSON export records. targetNS overrides the record namespace when
// non-empty (so you can re-home a brain). Embeddings are preserved (no re-embed).
func (s *Store) Import(ctx context.Context, targetNS string, r io.Reader) (int, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	n := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec ExportRecord
		if json.Unmarshal(line, &rec) != nil || rec.Content == "" {
			continue
		}
		ns := targetNS
		if ns == "" {
			ns = rec.Namespace
		}
		emb := any(nil)
		if rec.Embedding != "" {
			emb = rec.Embedding
		}
		vis := rec.Visibility
		if vis == "" {
			vis = "private"
		}
		net := rec.Network
		if net == "" {
			net = "experience"
		}
		mt := rec.MemoryType
		if mt == "" {
			mt = "episodic"
		}
		tier := rec.Tier
		if tier == "" {
			tier = "hot"
		}
		_, err := db.ExecContext(ctx, `
			INSERT INTO memories (namespace, visibility, network, memory_type, content,
			                      source_kind, source_ref, embedding, importance, tier, metadata)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::vector,$9,$10,COALESCE($11::jsonb,'{}'::jsonb))`,
			ns, vis, net, mt, rec.Content, nullStr(rec.SourceKind), nullStr(rec.SourceRef),
			emb, rec.Importance, tier, metaJSON(rec.Metadata))
		if err == nil {
			n++
			if s.cache() != nil {
				// leave epoch bump to the end
			}
		}
	}
	if targetNS != "" {
		s.bumpEpoch(targetNS)
	}
	return n, sc.Err()
}

// DeleteBrain removes a whole namespace (memories + gaps + events). Destructive.
func (s *Store) DeleteBrain(ctx context.Context, ns string) (int, error) {
	if ns == "" {
		return 0, ErrInvalidInput
	}
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM memories WHERE namespace=$1`, ns)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	_, _ = db.ExecContext(ctx, `DELETE FROM memory_gaps WHERE namespace=$1`, ns)
	_, _ = db.ExecContext(ctx, `DELETE FROM memory_events WHERE namespace=$1`, ns)
	s.bumpEpoch(ns)
	return int(n), nil
}

// EditMemory updates a memory's content / importance / metadata. If content
// changes, it re-embeds (needs the embedder) so recall stays correct.
func (s *Store) EditMemory(ctx context.Context, ns, id, content string, importance float64, metadata map[string]any) error {
	if ns == "" || id == "" {
		return ErrInvalidInput
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	sets := "last_accessed_at = now()"
	args := []any{id, ns}
	i := 3
	if content != "" {
		emb := s.embedder()
		if emb == nil {
			return ErrNoEmbedder
		}
		vecs, err := emb.Embed(ctx, []string{content})
		if err != nil || len(vecs) == 0 {
			if err == nil {
				err = ErrInvalidInput
			}
			return err
		}
		sets += ", content = $" + itoa(i)
		args = append(args, content)
		i++
		sets += ", embedding = $" + itoa(i) + "::vector"
		args = append(args, vecLit(vecs[0]))
		i++
	}
	if importance > 0 {
		sets += ", importance = $" + itoa(i)
		args = append(args, importance)
		i++
	}
	if metadata != nil {
		sets += ", metadata = COALESCE($" + itoa(i) + "::jsonb,'{}'::jsonb)"
		args = append(args, metaJSON(metadata))
		i++
	}
	res, err := db.ExecContext(ctx, `UPDATE memories SET `+sets+` WHERE id=$1 AND namespace=$2`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if content != "" {
		// re-tokenize BM25 best-effort (accelerator, not authoritative).
		_, _ = db.ExecContext(ctx, `UPDATE memories SET content_bm25 = tokenize($2,$3) WHERE id=$1`, id, content, bm25Tokenizer())
	}
	s.bumpEpoch(ns)
	return nil
}
