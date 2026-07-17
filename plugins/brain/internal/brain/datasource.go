package brain

// Data sources (connectors) — pluggable knowledge inputs for a brain. Each source
// is bound to a namespace; on sync it produces Documents which are chunked and
// retained (auto-embed + BM25 + secret-redaction, same §4.1 write-decision as any
// retain). Connectors self-register into a registry by kind, so new source types
// are added as plugins without touching the core (microkernel style):
//
//	brain.RegisterConnector("notion", myNotionConnector)
//
// Built-in kinds (connectors.go): text, markdown, crawler, github, sql. The
// "webhook" kind is PUSH (no Fetch) — data arrives at /api/brain/ingest/<id>.
// pdf / image / mcp are follow-ups.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Document is one unit of knowledge a connector emits.
type Document struct {
	ExternalID string         // stable id for dedup across syncs (optional)
	Content    string         // the text to remember
	SourceRef  string         // provenance (url, file path, row id, …)
	Metadata   map[string]any // free-form (title, tags, type, …)
}

// Connector pulls documents from an external source. cfg is the source's stored
// config; cursor lets incremental sources resume (return the next cursor).
type Connector interface {
	Fetch(ctx context.Context, cfg map[string]any, cursor string) (docs []Document, nextCursor string, err error)
}

// ConnectorFunc adapts a function to a Connector.
type ConnectorFunc func(ctx context.Context, cfg map[string]any, cursor string) ([]Document, string, error)

func (f ConnectorFunc) Fetch(ctx context.Context, cfg map[string]any, cursor string) ([]Document, string, error) {
	return f(ctx, cfg, cursor)
}

var connectorRegistry = map[string]Connector{}

// RegisterConnector adds a connector kind. Called from init() by built-ins and by
// any plugin that wants to add a source type.
func RegisterConnector(kind string, c Connector) { connectorRegistry[kind] = c }

// ConnectorKinds lists the registered kinds (+ the push-only "webhook").
func ConnectorKinds() []string {
	out := []string{"webhook"}
	for k := range connectorRegistry {
		out = append(out, k)
	}
	return out
}

// Datasource is a configured connector instance bound to a brain.
type Datasource struct {
	ID         string         `json:"id"`
	Namespace  string         `json:"namespace"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Config     map[string]any `json:"config"`
	Status     string         `json:"status"` // idle|syncing|ok|error
	Cursor     string         `json:"cursor,omitempty"`
	LastError  string         `json:"lastError,omitempty"`
	DocCount   int            `json:"docCount"`
	LastSyncAt *time.Time     `json:"lastSyncAt,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
}

// CreateDatasource stores a new source. For webhook kind a shared secret is
// generated into config.secret if absent so the push endpoint can authenticate.
func (s *Store) CreateDatasource(ctx context.Context, ns, kind, name string, cfg map[string]any) (*Datasource, error) {
	if ns == "" || kind == "" || name == "" {
		return nil, errors.New("namespace, kind, name required")
	}
	if _, ok := connectorRegistry[kind]; !ok && kind != "webhook" {
		return nil, errors.New("unknown connector kind: " + kind)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	if kind == "webhook" {
		if _, ok := cfg["secret"]; !ok {
			cfg["secret"] = "whk_" + randHex(16)
		}
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	cfgJSON, _ := json.Marshal(cfg)
	var id string
	var created time.Time
	err = db.QueryRowContext(ctx, `
		INSERT INTO datasources (namespace, kind, name, config)
		VALUES ($1,$2,$3,$4::jsonb) RETURNING id, created_at`,
		ns, kind, name, string(cfgJSON)).Scan(&id, &created)
	if err != nil {
		return nil, err
	}
	return &Datasource{ID: id, Namespace: ns, Kind: kind, Name: name, Config: cfg, Status: "idle", CreatedAt: created}, nil
}

func (s *Store) ListDatasources(ctx context.Context, ns string) ([]Datasource, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, namespace, kind, name, config, status, COALESCE(cursor,''),
		       COALESCE(last_error,''), doc_count, last_sync_at, created_at
		FROM datasources WHERE namespace=$1 ORDER BY created_at DESC`, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Datasource
	for rows.Next() {
		var d Datasource
		var cfg []byte
		var last sql.NullTime
		if err := rows.Scan(&d.ID, &d.Namespace, &d.Kind, &d.Name, &cfg, &d.Status,
			&d.Cursor, &d.LastError, &d.DocCount, &last, &d.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(cfg, &d.Config)
		redactDatasourceSecrets(&d)
		if last.Valid {
			d.LastSyncAt = &last.Time
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) getDatasource(ctx context.Context, id string) (*Datasource, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var d Datasource
	var cfg []byte
	var last sql.NullTime
	err = db.QueryRowContext(ctx, `
		SELECT id, namespace, kind, name, config, status, COALESCE(cursor,''),
		       COALESCE(last_error,''), doc_count, last_sync_at, created_at
		FROM datasources WHERE id=$1`, id).Scan(&d.ID, &d.Namespace, &d.Kind, &d.Name,
		&cfg, &d.Status, &d.Cursor, &d.LastError, &d.DocCount, &last, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(cfg, &d.Config)
	if last.Valid {
		d.LastSyncAt = &last.Time
	}
	return &d, nil
}

func (s *Store) DeleteDatasource(ctx context.Context, id string) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM datasources WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SyncResult is returned from a sync run.
type SyncResult struct {
	Ingested int    `json:"ingested"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// SyncDatasource runs the connector, retains each produced document (chunked), and
// records status/cursor/count. webhook kind is push-only and cannot be synced.
func (s *Store) SyncDatasource(ctx context.Context, id string) (*SyncResult, error) {
	ds, err := s.getDatasource(ctx, id)
	if err != nil {
		return nil, err
	}
	if ds.Kind == "webhook" {
		return nil, errors.New("webhook sources are push-only — POST to /api/brain/ingest/" + id)
	}
	c, ok := connectorRegistry[ds.Kind]
	if !ok {
		return nil, errors.New("unknown connector kind: " + ds.Kind)
	}
	s.setDatasourceStatus(ctx, id, "syncing", "", ds.Cursor, ds.DocCount)

	docs, nextCursor, ferr := c.Fetch(ctx, ds.Config, ds.Cursor)
	if ferr != nil {
		s.setDatasourceStatus(ctx, id, "error", ferr.Error(), ds.Cursor, ds.DocCount)
		return &SyncResult{Status: "error", Error: ferr.Error()}, nil
	}
	ingested := s.ingestDocuments(ctx, ds, docs)
	s.setDatasourceStatus(ctx, id, "ok", "", nextCursor, ds.DocCount+ingested)
	return &SyncResult{Ingested: ingested, Status: "ok"}, nil
}

// ingestDocuments chunks + retains each document into the brain. Best-effort per
// document (one failure doesn't abort the batch).
func (s *Store) ingestDocuments(ctx context.Context, ds *Datasource, docs []Document) int {
	n := 0
	for _, d := range docs {
		for i, chunk := range chunkText(d.Content, 1600) {
			meta := map[string]any{"datasource": ds.Name, "datasourceKind": ds.Kind}
			for k, v := range d.Metadata {
				meta[k] = v
			}
			ref := d.SourceRef
			if len(chunkText(d.Content, 1600)) > 1 {
				ref = d.SourceRef + "#" + strconv.Itoa(i)
			}
			_, err := s.Retain(ctx, MemoryInput{
				Namespace:  ds.Namespace,
				Content:    chunk,
				SourceKind: "datasource:" + ds.Kind,
				SourceRef:  ref,
				Metadata:   meta,
			})
			if err == nil {
				n++
			}
		}
	}
	return n
}

func (s *Store) setDatasourceStatus(ctx context.Context, id, status, errMsg, cursor string, count int) {
	db, err := s.db(ctx)
	if err != nil {
		return
	}
	var last any
	if status == "ok" || status == "error" {
		last = time.Now()
	}
	_, _ = db.ExecContext(ctx, `
		UPDATE datasources SET status=$2, last_error=NULLIF($3,''), cursor=NULLIF($4,''),
		       doc_count=$5, last_sync_at=COALESCE($6, last_sync_at) WHERE id=$1`,
		id, status, errMsg, cursor, count, last)
}

// IngestWebhook is the push path: content POSTed to a webhook datasource is retained
// directly. Returns the number of chunks stored.
func (s *Store) IngestWebhook(ctx context.Context, id, content, ref string, meta map[string]any) (int, error) {
	ds, err := s.getDatasource(ctx, id)
	if err != nil {
		return 0, err
	}
	if ds.Kind != "webhook" {
		return 0, errors.New("not a webhook datasource")
	}
	if strings.TrimSpace(content) == "" {
		return 0, errors.New("empty content")
	}
	if meta == nil {
		meta = map[string]any{}
	}
	if ref == "" {
		ref = "webhook"
	}
	n := s.ingestDocuments(ctx, ds, []Document{{Content: content, SourceRef: ref, Metadata: meta}})
	s.setDatasourceStatus(ctx, id, "ok", "", ds.Cursor, ds.DocCount+n)
	return n, nil
}

// webhookSecret returns the configured shared secret for a webhook source.
func (s *Store) webhookSecret(ctx context.Context, id string) (string, string, error) {
	ds, err := s.getDatasource(ctx, id)
	if err != nil {
		return "", "", err
	}
	if ds.Kind != "webhook" {
		return "", "", errors.New("not a webhook datasource")
	}
	sec, _ := ds.Config["secret"].(string)
	return sec, ds.Namespace, nil
}

// --- helpers ------------------------------------------------------------------

// chunkText splits text into ~max-rune chunks on paragraph/line boundaries so a
// long document becomes several retainable memories without cutting mid-sentence.
func chunkText(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len([]rune(text)) <= max {
		return []string{text}
	}
	paras := strings.Split(text, "\n\n")
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	for _, p := range paras {
		if cur.Len()+len(p)+2 > max && cur.Len() > 0 {
			flush()
		}
		if len([]rune(p)) > max {
			flush()
			// hard-split an oversized paragraph
			r := []rune(p)
			for i := 0; i < len(r); i += max {
				end := i + max
				if end > len(r) {
					end = len(r)
				}
				out = append(out, strings.TrimSpace(string(r[i:end])))
			}
			continue
		}
		cur.WriteString(p)
		cur.WriteString("\n\n")
	}
	flush()
	return out
}

func redactDatasourceSecrets(d *Datasource) {
	for _, k := range []string{"secret", "token", "password", "apiKey", "dsn"} {
		if v, ok := d.Config[k]; ok && v != "" {
			d.Config[k] = "••••"
		}
	}
}
