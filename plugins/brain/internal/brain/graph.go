package brain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Graph traversal over the typed entity graph, in plain Postgres.
//
// This is the capability people reach for Neo4j to get. At this scale a recursive
// CTE beats running a second stateful datastore: the graph lives in the same
// transaction as the memories it describes, so there is no sync problem and no
// split source of truth. Revisit only if traversal becomes the dominant workload
// at millions of edges.
//
// Everything here is time-aware: edges carry valid_from/valid_to, so a traversal
// can ask what the graph looked like at any instant, not just now.

// TraversalNode is one entity in a traversal result.
type TraversalNode struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Depth    int      `json:"depth"`
	Path     []string `json:"path"`          // names walked to reach it
	Via      string   `json:"via,omitempty"` // relation on the final hop
	Summary  string   `json:"summary,omitempty"`
	Distance float64  `json:"distance"` // graph distance, for distance-reranking
}

// RelationEdge is one typed relationship.
type RelationEdge struct {
	ID        string     `json:"id"`
	Src       string     `json:"src"`
	Dst       string     `json:"dst"`
	Relation  string     `json:"relation"`
	Fact      string     `json:"fact,omitempty"`
	ValidFrom time.Time  `json:"validFrom"`
	ValidTo   *time.Time `json:"validTo,omitempty"`
}

// TraverseQuery walks the graph outward from a named entity.
type TraverseQuery struct {
	Namespace string   `json:"namespace"`
	Entity    string   `json:"entity"`              // start node name
	Depth     int      `json:"depth"`               // hops (default 2, capped at 6)
	Relations []string `json:"relations,omitempty"` // restrict to these relation types
	Types     []string `json:"types,omitempty"`     // only return these entity types
	Direction string   `json:"direction,omitempty"` // out | in | both (default both)
	Limit     int      `json:"limit,omitempty"`
	// AsOf walks the graph as it stood at that instant — edges whose validity
	// window contains it. Empty = the graph as it is now.
	AsOf *time.Time `json:"asOf,omitempty"`
}

// Traverse runs a bounded breadth-first walk and returns the reachable entities.
//
// The recursive CTE carries `path` so cycles terminate (an entity is never
// revisited on the same path) and so callers can see HOW a node was reached,
// which is what makes multi-hop answers explainable rather than magic.
func (s *Store) Traverse(ctx context.Context, q TraverseQuery) ([]TraversalNode, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	if q.Namespace == "" || strings.TrimSpace(q.Entity) == "" {
		return nil, ErrInvalidInput
	}
	if q.Depth <= 0 {
		q.Depth = 2
	}
	if q.Depth > 6 { // a wide graph explodes combinatorially; keep the blast radius sane
		q.Depth = 6
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}

	// Direction decides which endpoint of the edge continues the walk.
	var join string
	switch strings.ToLower(q.Direction) {
	case "out":
		join = "JOIN entity_edges e ON e.src_id = w.id"
		join += "\n\t\t  JOIN entities ne ON ne.id = e.dst_id"
	case "in":
		join = "JOIN entity_edges e ON e.dst_id = w.id"
		join += "\n\t\t  JOIN entities ne ON ne.id = e.src_id"
	default:
		join = "JOIN entity_edges e ON (e.src_id = w.id OR e.dst_id = w.id)"
		join += "\n\t\t  JOIN entities ne ON ne.id = CASE WHEN e.src_id = w.id THEN e.dst_id ELSE e.src_id END"
	}

	args := []any{q.Namespace, q.Entity, q.Depth}
	next := 4
	edgeWhere := ""
	if q.AsOf != nil {
		edgeWhere += fmt.Sprintf(" AND e.valid_from <= $%d AND (e.valid_to IS NULL OR e.valid_to > $%d)", next, next)
		args = append(args, *q.AsOf)
		next++
	} else {
		edgeWhere += " AND e.valid_to IS NULL"
	}
	if len(q.Relations) > 0 {
		ph := make([]string, len(q.Relations))
		for i, r := range q.Relations {
			ph[i] = fmt.Sprintf("$%d", next)
			args = append(args, r)
			next++
		}
		edgeWhere += " AND e.relation IN (" + strings.Join(ph, ",") + ")"
	}
	typeWhere := ""
	if len(q.Types) > 0 {
		ph := make([]string, len(q.Types))
		for i, t := range q.Types {
			ph[i] = fmt.Sprintf("$%d", next)
			args = append(args, t)
			next++
		}
		typeWhere = " AND entity_type IN (" + strings.Join(ph, ",") + ")"
	}
	args = append(args, q.Limit)
	limitPh := fmt.Sprintf("$%d", next)

	query := fmt.Sprintf(`
WITH RECURSIVE walk(id, name, entity_type, summary, depth, path, via) AS (
  SELECT id, name, entity_type, COALESCE(summary,''), 0, ARRAY[name], ''
  FROM entities WHERE namespace = $1 AND name = $2
  UNION ALL
  SELECT ne.id, ne.name, ne.entity_type, COALESCE(ne.summary,''),
         w.depth + 1, w.path || ne.name, e.relation
  FROM walk w
  %s
  WHERE w.depth < $3 AND ne.namespace = $1 AND NOT ne.name = ANY(w.path) %s
)
SELECT DISTINCT ON (id) id::text, name, entity_type, summary, depth, path, via
FROM walk
WHERE depth > 0 %s
ORDER BY id, depth
LIMIT %s`, join, edgeWhere, typeWhere, limitPh)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("brain.Traverse: %w", err)
	}
	defer rows.Close()
	out := []TraversalNode{}
	for rows.Next() {
		var n TraversalNode
		var path []string
		if err := rows.Scan(&n.ID, &n.Name, &n.Type, &n.Summary, &n.Depth, &path, &n.Via); err != nil {
			continue
		}
		n.Path = path
		n.Distance = 1.0 / float64(1+n.Depth) // closer = higher, for distance-reranking
		out = append(out, n)
	}
	return out, rows.Err()
}

// Neighbors returns the immediate typed relationships of an entity, in both
// directions, with the relation and the human-readable fact.
func (s *Store) Neighbors(ctx context.Context, ns, entity string, asOf *time.Time) ([]RelationEdge, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	timeCond := "e.valid_to IS NULL"
	args := []any{ns, entity}
	if asOf != nil {
		timeCond = "e.valid_from <= $3 AND (e.valid_to IS NULL OR e.valid_to > $3)"
		args = append(args, *asOf)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT e.id::text, se.name, de.name, e.relation, COALESCE(e.fact,''), e.valid_from, e.valid_to
		FROM entity_edges e
		JOIN entities se ON se.id = e.src_id
		JOIN entities de ON de.id = e.dst_id
		WHERE e.namespace = $1 AND (se.name = $2 OR de.name = $2) AND `+timeCond+`
		ORDER BY e.relation, de.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RelationEdge{}
	for rows.Next() {
		var g RelationEdge
		var vt sql.NullTime
		if err := rows.Scan(&g.ID, &g.Src, &g.Dst, &g.Relation, &g.Fact, &g.ValidFrom, &vt); err != nil {
			continue
		}
		if vt.Valid {
			g.ValidTo = &vt.Time
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ShortestPath returns the hop sequence connecting two entities, or nil if they
// are not connected within maxDepth. Answers "how are these two related?".
func (s *Store) ShortestPath(ctx context.Context, ns, from, to string, maxDepth int) ([]string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	if maxDepth <= 0 || maxDepth > 6 {
		maxDepth = 4
	}
	var path []string
	err = db.QueryRowContext(ctx, `
WITH RECURSIVE walk(id, name, depth, path) AS (
  SELECT id, name, 0, ARRAY[name] FROM entities WHERE namespace=$1 AND name=$2
  UNION ALL
  SELECT ne.id, ne.name, w.depth+1, w.path || ne.name
  FROM walk w
  JOIN entity_edges e ON (e.src_id = w.id OR e.dst_id = w.id) AND e.valid_to IS NULL
  JOIN entities ne ON ne.id = CASE WHEN e.src_id = w.id THEN e.dst_id ELSE e.src_id END
  WHERE w.depth < $4 AND ne.namespace = $1 AND NOT ne.name = ANY(w.path)
)
SELECT path FROM walk WHERE name = $3 ORDER BY depth LIMIT 1`, ns, from, to, maxDepth).Scan(&path)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return path, err
}

// DetectCommunities assigns entities to clusters by label propagation over the
// live edges: every node adopts the most common label among its neighbours,
// iterated to convergence. Pure SQL — no external graph library.
func (s *Store) DetectCommunities(ctx context.Context, ns string, iterations int) (int, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	if iterations <= 0 || iterations > 20 {
		iterations = 8
	}
	// Seed: every entity is its own community.
	if _, err := db.ExecContext(ctx,
		`UPDATE entities SET community_id = id WHERE namespace = $1`, ns); err != nil {
		return 0, err
	}
	for i := 0; i < iterations; i++ {
		if _, err := db.ExecContext(ctx, `
UPDATE entities t SET community_id = best.cid
FROM (
  SELECT n.id, (
    SELECT nb.community_id
    FROM entity_edges e
    JOIN entities nb ON nb.id = CASE WHEN e.src_id = n.id THEN e.dst_id ELSE e.src_id END
    WHERE (e.src_id = n.id OR e.dst_id = n.id) AND e.valid_to IS NULL AND nb.namespace = $1
    GROUP BY nb.community_id
    ORDER BY count(*) DESC, nb.community_id
    LIMIT 1
  ) AS cid
  FROM entities n WHERE n.namespace = $1
) best
WHERE t.id = best.id AND best.cid IS NOT NULL AND t.community_id IS DISTINCT FROM best.cid`, ns); err != nil {
			return 0, err
		}
	}
	// Materialise the clusters, naming each after its largest member.
	if _, err := db.ExecContext(ctx, `DELETE FROM communities WHERE namespace = $1`, ns); err != nil {
		return 0, err
	}
	var n int
	err = db.QueryRowContext(ctx, `
WITH grp AS (
  SELECT community_id, count(*) AS sz,
         (ARRAY_AGG(name ORDER BY entity_type='portfolio' DESC, entity_type='venture' DESC, name))[1] AS lead
  FROM entities WHERE namespace = $1 AND community_id IS NOT NULL
  GROUP BY community_id HAVING count(*) > 1
), ins AS (
  INSERT INTO communities (namespace, name, size, summary)
  SELECT $1, 'cluster: ' || lead, sz, 'Entities densely connected around ' || lead
  FROM grp ON CONFLICT (namespace, name) DO UPDATE SET size = EXCLUDED.size
  RETURNING 1
)
SELECT count(*) FROM ins`, ns).Scan(&n)
	return n, err
}

// ── HTTP surface ──────────────────────────────────────────────────────────────

// Traverse — POST /api/brain/graph/traverse
// { namespace, entity, depth?, relations?, types?, direction?, asOf?, limit? }
func (s *Service) TraverseHandler(w http.ResponseWriter, r *http.Request) {
	var q TraverseQuery
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if !s.canRead(r, q.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read access to brain "+q.Namespace))
		return
	}
	nodes, err := s.Store.Traverse(r.Context(), q)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "count": len(nodes)})
}

// Neighbors — POST /api/brain/graph/neighbors { namespace, entity, asOf? }
func (s *Service) NeighborsHandler(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace string     `json:"namespace"`
		Entity    string     `json:"entity"`
		AsOf      *time.Time `json:"asOf"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if !s.canRead(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read access"))
		return
	}
	edges, err := s.Store.Neighbors(r.Context(), in.Namespace, in.Entity, in.AsOf)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": edges, "count": len(edges)})
}

// Path — POST /api/brain/graph/path { namespace, from, to, maxDepth? }
func (s *Service) PathHandler(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace string `json:"namespace"`
		From      string `json:"from"`
		To        string `json:"to"`
		MaxDepth  int    `json:"maxDepth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if !s.canRead(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read access"))
		return
	}
	path, err := s.Store.ShortestPath(r.Context(), in.Namespace, in.From, in.To, in.MaxDepth)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "hops": len(path) - 1, "connected": len(path) > 0})
}

// Communities — POST /api/brain/graph/communities { namespace, iterations? }
func (s *Service) CommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace  string `json:"namespace"`
		Iterations int    `json:"iterations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access"))
		return
	}
	n, err := s.Store.DetectCommunities(r.Context(), in.Namespace, in.Iterations)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"communities": n})
}

// Ontology — GET /api/brain/graph/ontology?namespace= : the declared entity and
// edge types, so a caller can discover a brain's shape instead of guessing it.
func (s *Service) OntologyHandler(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if !s.canRead(r, ns) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read access"))
		return
	}
	db, err := s.Store.db(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	type et struct {
		Name, Description string
		Count             int
	}
	ents := []et{}
	rows, _ := db.QueryContext(r.Context(), `
		SELECT t.name, COALESCE(t.description,''), (SELECT count(*) FROM entities e
		  WHERE e.namespace=t.namespace AND e.entity_type=t.name)
		FROM entity_types t WHERE t.namespace=$1 ORDER BY 3 DESC`, ns)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var x et
			if rows.Scan(&x.Name, &x.Description, &x.Count) == nil {
				ents = append(ents, x)
			}
		}
	}
	type rt struct {
		Name, Description, Src, Dst string
		Count                       int
	}
	rels := []rt{}
	rows2, _ := db.QueryContext(r.Context(), `
		SELECT t.name, COALESCE(t.description,''), COALESCE(t.src_type,''), COALESCE(t.dst_type,''),
		       (SELECT count(*) FROM entity_edges e WHERE e.namespace=t.namespace AND e.relation=t.name AND e.valid_to IS NULL)
		FROM edge_types t WHERE t.namespace=$1 ORDER BY 5 DESC`, ns)
	if rows2 != nil {
		defer rows2.Close()
		for rows2.Next() {
			var x rt
			if rows2.Scan(&x.Name, &x.Description, &x.Src, &x.Dst, &x.Count) == nil {
				rels = append(rels, x)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entityTypes": ents, "edgeTypes": rels})
}
