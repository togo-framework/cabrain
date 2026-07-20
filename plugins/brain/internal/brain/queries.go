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
	OpenGaps    int  `json:"openGaps"`    // missed questions awaiting indexing
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
	scan(`SELECT COUNT(*) FROM memory_gaps WHERE status='open'`, &st.OpenGaps)
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
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group,omitempty"` // for coloring: root | type | <type>
}
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}
type GraphData struct {
	Ready   bool        `json:"ready"`
	Derived bool        `json:"derived"` // true = built from memory metadata (Cognee graph absent)
	Nodes   []GraphNode `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
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

	// Use the Cognee-populated entity graph when it exists; otherwise derive a
	// browsable mindmap from memory metadata (namespace → type → sample entities).
	var entCount int
	_ = db.QueryRowContext(ctx,
		`SELECT count(*) FROM entities WHERE ($1='' OR namespace=$1)`, namespace).Scan(&entCount)
	if entCount == 0 {
		return s.derivedGraph(ctx, db, namespace, limit)
	}

	nq := `SELECT id::text, name FROM entities`
	args := []any{}
	if namespace != "" {
		nq += ` WHERE namespace = $1`
		args = append(args, namespace)
	}
	rows, err := db.QueryContext(ctx, nq+` LIMIT `+itoa(limit), args...)
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

// derivedGraph builds a hierarchical mindmap from memory metadata:
// namespace (root) → memory type → up to N sample entities per type.
func (s *Store) derivedGraph(ctx context.Context, db *sql.DB, namespace string, limit int) (*GraphData, error) {
	g := &GraphData{Ready: true, Derived: true, Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	if namespace == "" {
		// no scope → show namespaces as the first level
		g.Nodes = append(g.Nodes, GraphNode{ID: "root", Name: "brain", Group: "root"})
		rows, err := db.QueryContext(ctx, `
			SELECT namespace, count(*) FROM memories WHERE invalid_at IS NULL
			GROUP BY namespace ORDER BY 2 DESC LIMIT 30`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var ns string
				var c int
				if rows.Scan(&ns, &c) == nil {
					g.Nodes = append(g.Nodes, GraphNode{ID: "ns:" + ns, Name: ns + " (" + itoa(c) + ")", Group: "type"})
					g.Edges = append(g.Edges, GraphEdge{Source: "root", Target: "ns:" + ns})
				}
			}
		}
		return g, nil
	}

	// Rich path: if this brain has venture-structured metadata, build a real
	// portfolio → venture → entity graph instead of the flat type tree.
	var ventureCount int
	_ = db.QueryRowContext(ctx,
		`SELECT count(*) FROM memories WHERE namespace=$1 AND invalid_at IS NULL AND metadata->>'type'='venture'`, namespace).Scan(&ventureCount)
	if ventureCount > 0 {
		return s.ventureGraph(ctx, db, namespace, limit)
	}

	g.Nodes = append(g.Nodes, GraphNode{ID: "root", Name: namespace, Group: "root"})
	// level 1: types
	trows, err := db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(metadata->>'type',''),'item') AS t, count(*)
		FROM memories WHERE namespace=$1 AND invalid_at IS NULL
		GROUP BY 1 ORDER BY 2 DESC LIMIT 24`, namespace)
	if err != nil {
		return g, nil
	}
	types := []string{}
	defer trows.Close()
	for trows.Next() {
		var t string
		var c int
		if trows.Scan(&t, &c) == nil {
			g.Nodes = append(g.Nodes, GraphNode{ID: "type:" + t, Name: t + " (" + itoa(c) + ")", Group: "type"})
			g.Edges = append(g.Edges, GraphEdge{Source: "root", Target: "type:" + t})
			types = append(types, t)
		}
	}
	if len(types) == 0 {
		return g, nil
	}
	perType := limit / len(types)
	if perType < 4 {
		perType = 4
	}
	if perType > 20 {
		perType = 20
	}
	// level 2: sample entities per type
	for _, t := range types {
		erows, err := db.QueryContext(ctx, `
			SELECT id::text,
			       left(regexp_replace(COALESCE(NULLIF(metadata->>'slug',''), NULLIF(metadata->>'path',''), content),'\s+',' ','g'), 48) AS name
			FROM memories
			WHERE namespace=$1 AND invalid_at IS NULL AND COALESCE(NULLIF(metadata->>'type',''),'item')=$2
			LIMIT `+itoa(perType), namespace, t)
		if err != nil {
			continue
		}
		for erows.Next() {
			var id, name string
			if erows.Scan(&id, &name) == nil {
				g.Nodes = append(g.Nodes, GraphNode{ID: "ent:" + id, Name: name, Group: t})
				g.Edges = append(g.Edges, GraphEdge{Source: "type:" + t, Target: "ent:" + id})
			}
		}
		erows.Close()
	}
	return g, nil
}

// ventureGraph builds a real knowledge graph from venture-structured metadata:
// portfolio → venture → entities (issues/posts/people/... linked via
// metadata->>'venture'). Much richer than the flat namespace→type→sample tree.
func (s *Store) ventureGraph(ctx context.Context, db *sql.DB, ns string, limit int) (*GraphData, error) {
	g := &GraphData{Ready: true, Derived: true, Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	g.Nodes = append(g.Nodes, GraphNode{ID: "root", Name: ns, Group: "root"})

	// Relationships are recoverable from the content text (portfolio, venture name),
	// since the structured metadata was dropped by an earlier retain bug. Extract
	// them in SQL: "Venture: <name> … Portfolio: <pf>" and entity "in venture <name>".
	ventureSet := map[string]bool{}
	portfolios := map[string]bool{}
	vrows, err := db.QueryContext(ctx, `
		SELECT DISTINCT
		  trim(substring(content from 'Venture: ([^(\n]+)')) AS vname,
		  COALESCE(NULLIF(trim(substring(content from 'Portfolio: ([A-Za-z0-9_ -]+)')),''),'unassigned') AS pf
		FROM memories WHERE namespace=$1 AND invalid_at IS NULL AND metadata->>'type'='venture'`, ns)
	if err != nil {
		return g, nil
	}
	for vrows.Next() {
		var vname, pf string
		if vrows.Scan(&vname, &pf) == nil && vname != "" {
			if !portfolios[pf] {
				portfolios[pf] = true
				g.Nodes = append(g.Nodes, GraphNode{ID: "pf:" + pf, Name: pf, Group: "portfolio"})
				g.Edges = append(g.Edges, GraphEdge{Source: "root", Target: "pf:" + pf})
			}
			g.Nodes = append(g.Nodes, GraphNode{ID: "v:" + vname, Name: vname, Group: "venture"})
			g.Edges = append(g.Edges, GraphEdge{Source: "pf:" + pf, Target: "v:" + vname})
			ventureSet[vname] = true
		}
	}
	vrows.Close()

	// entities linked to a venture by name (capped).
	erows, err := db.QueryContext(ctx, `
		SELECT id::text, COALESCE(NULLIF(metadata->>'type',''),'item') AS t,
		       trim(substring(content from ' in venture ([^:.\n]+)')) AS vname,
		       left(regexp_replace(content,'\s+',' ','g'), 44) AS name
		FROM memories
		WHERE namespace=$1 AND invalid_at IS NULL AND metadata->>'type' <> 'venture'
		      AND content LIKE '%in venture %'
		ORDER BY valid_at DESC LIMIT `+itoa(limit), ns)
	if err == nil {
		for erows.Next() {
			var id, t, v, name string
			if erows.Scan(&id, &t, &v, &name) == nil && ventureSet[v] {
				g.Nodes = append(g.Nodes, GraphNode{ID: "ent:" + id, Name: name, Group: t})
				g.Edges = append(g.Edges, GraphEdge{Source: "v:" + v, Target: "ent:" + id})
			}
		}
		erows.Close()
	}

	// Type spine: surface EVERY memory type — not just venture-linked entities — so the
	// graph shows all the data (git-activity, code, task, learning, issue, goal, …) as
	// root → type:<t> → sample entities. Entities already attached to a venture are
	// reused by node id and simply gain a second edge to their type node.
	entSeen := map[string]bool{}
	for _, n := range g.Nodes {
		if len(n.ID) > 4 && n.ID[:4] == "ent:" {
			entSeen[n.ID] = true
		}
	}
	trows, terr := db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(metadata->>'type',''),'item') AS t, count(*)
		FROM memories
		WHERE namespace=$1 AND invalid_at IS NULL
		      AND COALESCE(NULLIF(metadata->>'type',''),'item') <> 'venture'
		GROUP BY 1 ORDER BY 2 DESC LIMIT 30`, ns)
	if terr == nil {
		types := []string{}
		for trows.Next() {
			var t string
			var c int
			if trows.Scan(&t, &c) == nil {
				g.Nodes = append(g.Nodes, GraphNode{ID: "type:" + t, Name: t + " (" + itoa(c) + ")", Group: "type"})
				g.Edges = append(g.Edges, GraphEdge{Source: "root", Target: "type:" + t})
				types = append(types, t)
			}
		}
		trows.Close()
		perType := limit / 10
		if perType < 8 {
			perType = 8
		}
		if perType > 40 {
			perType = 40
		}
		for _, t := range types {
			srows, serr := db.QueryContext(ctx, `
				SELECT id::text,
				       left(regexp_replace(COALESCE(NULLIF(metadata->>'slug',''), NULLIF(metadata->>'path',''), NULLIF(metadata->>'file',''), content),'\s+',' ','g'), 44) AS name
				FROM memories
				WHERE namespace=$1 AND invalid_at IS NULL AND COALESCE(NULLIF(metadata->>'type',''),'item')=$2
				ORDER BY valid_at DESC LIMIT `+itoa(perType), ns, t)
			if serr != nil {
				continue
			}
			for srows.Next() {
				var id, name string
				if srows.Scan(&id, &name) == nil {
					nid := "ent:" + id
					if !entSeen[nid] {
						g.Nodes = append(g.Nodes, GraphNode{ID: nid, Name: name, Group: t})
						entSeen[nid] = true
					}
					g.Edges = append(g.Edges, GraphEdge{Source: "type:" + t, Target: nid})
				}
			}
			srows.Close()
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
