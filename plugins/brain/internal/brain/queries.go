package brain

import (
	"context"
	"database/sql"
	"time"
)

// Read-side queries that feed the console. All are defensive: when the brain
// schema isn't applied yet (e.g. local SQLite smoke-boot, or the cabrain DB not
// provisioned — Blocker B), they return zero/empty values with ready=false
// instead of erroring, so the UI renders its "brain not connected" state.

// ready reports whether the memories table exists (schema applied).
func (s *Store) ready(ctx context.Context, db *sql.DB) bool {
	var x int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM memories LIMIT 1").Scan(&x)
	return err == nil || err == sql.ErrNoRows
}

// Stats — the dashboard metric strip (Cognee: nodes/edges/brains + agents/sessions/calls).
type Stats struct {
	Ready       bool `json:"ready"`
	Brains      int  `json:"brains"` // distinct namespaces
	Memories    int  `json:"memories"`
	Entities    int  `json:"entities"`    // graph nodes
	Edges       int  `json:"edges"`       // graph edges
	Agents      int  `json:"agents"`      // distinct owner agents
	Sessions24h int  `json:"sessions24h"` // distinct source_ref, last 24h
	Recalls24h  int  `json:"recalls24h"`  // recall events, last 24h
}

func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	db, err := s.db(ctx)
	if err != nil {
		return &Stats{Ready: false}, nil
	}
	if !s.ready(ctx, db) {
		return &Stats{Ready: false}, nil
	}
	st := &Stats{Ready: true}
	scan := func(q string, dst *int) {
		_ = db.QueryRowContext(ctx, q).Scan(dst) // best-effort; leaves 0 on error
	}
	scan(`SELECT COUNT(DISTINCT namespace) FROM memories WHERE invalid_at IS NULL`, &st.Brains)
	scan(`SELECT COUNT(*) FROM memories WHERE invalid_at IS NULL`, &st.Memories)
	scan(`SELECT COUNT(*) FROM entities`, &st.Entities)
	scan(`SELECT COUNT(*) FROM memory_entities`, &st.Edges)
	scan(`SELECT COUNT(DISTINCT owner_agent_id) FROM memories WHERE owner_agent_id IS NOT NULL`, &st.Agents)
	scan(`SELECT COUNT(DISTINCT source_ref) FROM memories WHERE source_ref IS NOT NULL AND valid_at > now() - interval '24 hours'`, &st.Sessions24h)
	scan(`SELECT COUNT(*) FROM memory_events WHERE op='recall' AND ts > now() - interval '24 hours'`, &st.Recalls24h)
	return st, nil
}

// ActivityItem — one row of the Memory Activity log.
type ActivityItem struct {
	ID        int64     `json:"id"`
	TS        time.Time `json:"ts"`
	Op        string    `json:"op"`
	Namespace string    `json:"namespace"`
	AgentID   string    `json:"agentId"`
	Outcome   string    `json:"outcome"` // hit | empty | error | running
	LatencyMs int       `json:"latencyMs"`
}

func (s *Store) Activity(ctx context.Context, limit int) ([]ActivityItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	db, err := s.db(ctx)
	if err != nil || !s.ready(ctx, db) {
		return []ActivityItem{}, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, ts, op, COALESCE(namespace,''), COALESCE(agent_id,''),
		       COALESCE(metadata->>'outcome','hit'), COALESCE(latency_ms,0)
		FROM memory_events ORDER BY ts DESC LIMIT $1`, limit)
	if err != nil {
		return []ActivityItem{}, nil
	}
	defer rows.Close()
	out := []ActivityItem{}
	for rows.Next() {
		var a ActivityItem
		if err := rows.Scan(&a.ID, &a.TS, &a.Op, &a.Namespace, &a.AgentID, &a.Outcome, &a.LatencyMs); err == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// NamespaceInfo — one "brain" (dataset) in the workspace.
type NamespaceInfo struct {
	Namespace string    `json:"namespace"`
	Memories  int       `json:"memories"`
	LastAt    time.Time `json:"lastAt"`
}

func (s *Store) Namespaces(ctx context.Context) ([]NamespaceInfo, error) {
	db, err := s.db(ctx)
	if err != nil || !s.ready(ctx, db) {
		return []NamespaceInfo{}, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT namespace, COUNT(*), MAX(valid_at)
		FROM memories WHERE invalid_at IS NULL
		GROUP BY namespace ORDER BY COUNT(*) DESC`)
	if err != nil {
		return []NamespaceInfo{}, nil
	}
	defer rows.Close()
	out := []NamespaceInfo{}
	for rows.Next() {
		var n NamespaceInfo
		if err := rows.Scan(&n.Namespace, &n.Memories, &n.LastAt); err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

// Graph — the mindmap / Graph Explorer subgraph.
type GraphNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}
type GraphData struct {
	Ready bool        `json:"ready"`
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

func (s *Store) Graph(ctx context.Context, namespace string, limit int) (*GraphData, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	g := &GraphData{Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	db, err := s.db(ctx)
	if err != nil || !s.ready(ctx, db) {
		return g, nil
	}
	g.Ready = true
	nq := `SELECT id::text, name FROM entities`
	args := []any{}
	if namespace != "" {
		nq += ` WHERE namespace = $1`
		args = append(args, namespace)
	}
	nq += ` LIMIT ` // limit appended below via fmt-free positional
	rows, err := db.QueryContext(ctx, nq+itoa(limit), args...)
	if err != nil {
		return g, nil
	}
	defer rows.Close()
	for rows.Next() {
		var n GraphNode
		if err := rows.Scan(&n.ID, &n.Name); err == nil {
			g.Nodes = append(g.Nodes, n)
		}
	}
	erows, err := db.QueryContext(ctx, `
		SELECT me1.entity_id::text, me2.entity_id::text
		FROM memory_entities me1
		JOIN memory_entities me2 ON me1.memory_id = me2.memory_id AND me1.entity_id < me2.entity_id
		LIMIT `+itoa(limit))
	if err == nil {
		defer erows.Close()
		for erows.Next() {
			var e GraphEdge
			if err := erows.Scan(&e.Source, &e.Target); err == nil {
				g.Edges = append(g.Edges, e)
			}
		}
	}
	return g, nil
}

// itoa avoids fmt for the trivial LIMIT integer (already range-checked).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
