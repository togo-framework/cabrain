package brain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The spine query: "give me EVERYTHING about venture X", in one call.
//
// The graph already holds the studio's spine
//
//	portfolio -> venture -> repo -> member -> activity -> feed
//	                     -> goal -> roadmap -> code_module -> doc
//
// but walking it meant hand-writing a recursive CTE per question, and the raw
// Traverse output is a flat node list an agent then has to bucket itself. Spine
// does the bucketing in SQL and returns ROLE GROUPS with a true total next to a
// capped sample.
//
// That last part is the lesson the graph explorer taught the hard way: it showed
// the SAMPLING QUOTA in the legend as if it were the population. So every group
// here carries `total` (the real count of distinct reachable nodes of that role)
// AND `shown`/`capped`; a capped list can never masquerade as a total.
//
// Shape of the walk: one generic breadth-first pass, not a hardcoded relation
// chain, so new relations added by the graph builders show up without a code
// change. Fan-out is contained by `hubs`: past the first hop only the structural
// hub types may be expanded (a venture's repos lead on to code/docs/activity; a
// venture's 200 goals do not lead anywhere useful and would explode the walk).

// SpineItem is one entity in a role group.
type SpineItem struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Type    string     `json:"type"`
	Depth   int        `json:"depth"`
	Via     string     `json:"via,omitempty"`  // relation on the hop that reached it
	Path    []string   `json:"path,omitempty"` // names walked, root first
	Summary string     `json:"summary,omitempty"`
	At      *time.Time `json:"at,omitempty"` // valid_from of the reaching edge
}

// SpineGroup is every entity of one role, with the honest population count.
type SpineGroup struct {
	Role   string      `json:"role"`
	Total  int         `json:"total"` // distinct reachable nodes of this role
	Shown  int         `json:"shown"` // how many are in Items
	Capped bool        `json:"capped"`
	Items  []SpineItem `json:"items"`
}

// SpineWindow reports the time bound and, crucially, WHICH roles it was applied
// to — a structural role is not filtered by "last week" (the repos of a venture
// are its repos, not the repos created last week).
type SpineWindow struct {
	Since     *time.Time `json:"since,omitempty"`
	Until     *time.Time `json:"until,omitempty"`
	AppliedTo []string   `json:"appliedTo,omitempty"`
}

// SpineRoot is the resolved starting entity.
type SpineRoot struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Summary string `json:"summary,omitempty"`
}

// SpineResult is the whole neighbourhood of one entity, grouped by role.
type SpineResult struct {
	Root   SpineRoot      `json:"root"`
	Depth  int            `json:"depth"`
	Hubs   []string       `json:"hubs"`
	Window SpineWindow    `json:"window"`
	Groups []SpineGroup   `json:"groups"`
	Totals map[string]int `json:"totals"`
	// Nodes is the sum of the group totals — every distinct entity reachable
	// under the query's depth/window/roles, NOT the number returned.
	Nodes      int      `json:"nodes"`
	Returned   int      `json:"returned"` // entities actually in Items
	Candidates []string `json:"candidates,omitempty"`
}

// SpineQuery asks for one entity's whole neighbourhood.
type SpineQuery struct {
	Namespace string `json:"namespace"`
	// Entity is a name (exact, then case-insensitive) or an entity id.
	Entity string `json:"entity"`
	// Depth caps the walk. 2 (default) is venture -> repo -> code/doc/activity.
	// A portfolio root wants 3.
	Depth int `json:"depth,omitempty"`
	// Hubs are the entity types that may be expanded past the first hop.
	// Empty = DefaultSpineHubs.
	Hubs []string `json:"hubs,omitempty"`
	// Roles restricts which groups come back (empty = all).
	Roles []string `json:"roles,omitempty"`
	// PerGroup caps items per group (default 10, max 200). Total is unaffected.
	PerGroup int `json:"perGroup,omitempty"`
	// Since/Until bound the reaching edge's valid_from, for TimeRoles only.
	Since *time.Time `json:"since,omitempty"`
	Until *time.Time `json:"until,omitempty"`
	// Window is sugar for Since: "7d", "2w", "3m" — relative to now.
	Window string `json:"window,omitempty"`
	// TimeRoles are the roles the window applies to. Empty = DefaultTimeRoles.
	// Pass ["*"] to time-bound every role.
	TimeRoles []string `json:"timeRoles,omitempty"`
}

// DefaultSpineHubs — the structural types worth walking THROUGH. A repo carries
// its code, docs and activity; a venture carries everything under a portfolio.
//
// `portfolio` is deliberately NOT a default hub. It is expanded when it is the
// ROOT (depth 0 always expands), but expanding it mid-walk would let a venture
// climb to its portfolio and come back down with its SIBLINGS' repos, presenting
// them as the venture's own. Pass hubs=[...,"portfolio"] to ask for that on
// purpose.
var DefaultSpineHubs = []string{"repo", "venture", "feed"}

// DefaultTimeRoles — the event-bearing roles. Everything else is structural and
// is deliberately not filtered by the window.
var DefaultTimeRoles = []string{"activity", "meeting", "channel", "feed"}

// spineRoleOrder puts the answer in the order the studio thinks about it.
var spineRoleOrder = []string{
	"portfolio", "venture", "repo", "person", "agent", "activity",
	"feed", "channel", "goal", "okr", "roadmap", "code_module", "doc",
	"learning", "issue", "meeting", "campaign",
}

// ParseSpineWindow turns "7d" / "2w" / "3m" / "24h" into a start instant.
func ParseSpineWindow(s string, now time.Time) (*time.Time, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "last")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(strings.TrimSpace(s[:len(s)-1]))
	if err != nil || n <= 0 {
		return nil, false
	}
	var d time.Duration
	switch unit {
	case 'h':
		d = time.Duration(n) * time.Hour
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	case 'w':
		d = time.Duration(n) * 7 * 24 * time.Hour
	case 'm':
		d = time.Duration(n) * 30 * 24 * time.Hour
	default:
		return nil, false
	}
	t := now.Add(-d)
	return &t, true
}

// placeholders appends vals to args and returns "$n,$n+1,…" for an IN list.
func placeholders(args *[]any, next *int, vals []string) string {
	ph := make([]string, len(vals))
	for i, v := range vals {
		ph[i] = fmt.Sprintf("$%d", *next)
		*args = append(*args, v)
		*next++
	}
	return strings.Join(ph, ",")
}

// Spine returns one entity's whole neighbourhood, grouped by role, with counts.
//
// One round trip: a recursive CTE walks, a window function both counts each role
// and numbers its rows, and only the first PerGroup rows of each role come back.
func (s *Store) Spine(ctx context.Context, q SpineQuery) (*SpineResult, error) {
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
	if q.Depth > 4 { // the walk is breadth-first over a dense graph; 4 is already wide
		q.Depth = 4
	}
	if q.PerGroup <= 0 {
		q.PerGroup = 10
	}
	if q.PerGroup > 200 {
		q.PerGroup = 200
	}
	hubs := q.Hubs
	if len(hubs) == 0 {
		hubs = DefaultSpineHubs
	}
	if q.Since == nil && q.Window != "" {
		if t, ok := ParseSpineWindow(q.Window, time.Now().UTC()); ok {
			q.Since = t
		}
	}
	timeRoles := q.TimeRoles
	if len(timeRoles) == 0 {
		timeRoles = DefaultTimeRoles
	}
	allRoles := len(timeRoles) == 1 && timeRoles[0] == "*"

	res := &SpineResult{
		Depth: q.Depth, Hubs: hubs,
		Totals: map[string]int{}, Groups: []SpineGroup{},
	}

	// Resolve the root first, so a typo gets candidates instead of an empty walk.
	var rootID string
	err = db.QueryRowContext(ctx, `
		SELECT id::text, name, entity_type, COALESCE(summary,'')
		FROM entities
		WHERE namespace = $1 AND (name = $2 OR lower(name) = lower($2) OR id::text = $2)
		ORDER BY (name = $2) DESC, (id::text = $2) DESC, length(name)
		LIMIT 1`, q.Namespace, q.Entity).
		Scan(&rootID, &res.Root.Name, &res.Root.Type, &res.Root.Summary)
	if err == sql.ErrNoRows {
		rows, e := db.QueryContext(ctx, `
			SELECT name FROM entities
			WHERE namespace = $1 AND name ILIKE '%' || $2 || '%'
			ORDER BY entity_type IN ('portfolio','venture') DESC, length(name) LIMIT 10`,
			q.Namespace, q.Entity)
		if e == nil {
			defer rows.Close()
			for rows.Next() {
				var n string
				if rows.Scan(&n) == nil {
					res.Candidates = append(res.Candidates, n)
				}
			}
		}
		return res, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("brain.Spine: resolve root: %w", err)
	}
	res.Root.ID = rootID

	args := []any{q.Namespace, rootID, q.Depth}
	next := 4

	// Past hop 1 only hub types are expanded. Empty hub list = a single hop.
	hubCond := " AND w.depth = 0"
	if len(hubs) > 0 {
		hubCond = " AND (w.depth = 0 OR w.entity_type IN (" + placeholders(&args, &next, hubs) + "))"
	}

	// The window binds the reaching edge's valid_from, and only for TimeRoles.
	timeCond := ""
	if q.Since != nil || q.Until != nil {
		bound := ""
		if q.Since != nil {
			bound += fmt.Sprintf(" AND at >= $%d", next)
			args = append(args, *q.Since)
			next++
		}
		if q.Until != nil {
			bound += fmt.Sprintf(" AND at <= $%d", next)
			args = append(args, *q.Until)
			next++
		}
		if allRoles {
			timeCond = " AND (TRUE" + bound + ")"
			res.Window.AppliedTo = []string{"*"}
		} else {
			in := placeholders(&args, &next, timeRoles)
			timeCond = " AND (entity_type NOT IN (" + in + ") OR (TRUE" + bound + "))"
			res.Window.AppliedTo = timeRoles
		}
		res.Window.Since, res.Window.Until = q.Since, q.Until
	}

	roleCond := ""
	if len(q.Roles) > 0 {
		roleCond = " AND entity_type IN (" + placeholders(&args, &next, q.Roles) + ")"
	}

	args = append(args, q.PerGroup)
	capPh := fmt.Sprintf("$%d", next)

	query := fmt.Sprintf(`
WITH RECURSIVE walk(id, name, entity_type, summary, depth, path, via, at) AS (
  SELECT id, name, entity_type, COALESCE(summary,''), 0, ARRAY[name], '', NULL::timestamptz
  FROM entities WHERE id = $2::uuid
  UNION ALL
  SELECT ne.id, ne.name, ne.entity_type, COALESCE(ne.summary,''),
         w.depth + 1, w.path || ne.name, e.relation, e.valid_from
  FROM walk w
  JOIN entity_edges e ON (e.src_id = w.id OR e.dst_id = w.id)
       AND e.valid_to IS NULL AND e.namespace = $1
  JOIN entities ne ON ne.id = CASE WHEN e.src_id = w.id THEN e.dst_id ELSE e.src_id END
  WHERE w.depth < $3 AND ne.namespace = $1 AND NOT ne.name = ANY(w.path)%s
), kept AS (
  SELECT * FROM walk WHERE depth > 0%s%s
), best AS (
  SELECT DISTINCT ON (id) id, name, entity_type, summary, depth, path, via, at
  FROM kept ORDER BY id, depth, at DESC NULLS LAST
), ranked AS (
  SELECT id::text AS id, name, entity_type, summary, depth,
         array_to_string(path, chr(31)) AS path, via, at,
         count(*) OVER (PARTITION BY entity_type) AS total,
         row_number() OVER (PARTITION BY entity_type
                            ORDER BY at DESC NULLS LAST, depth, name) AS rn
  FROM best
)
SELECT id, name, entity_type, summary, depth, path, via, at, total, rn
FROM ranked WHERE rn <= %s
ORDER BY entity_type, rn`, hubCond, timeCond, roleCond, capPh)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("brain.Spine: %w", err)
	}
	defer rows.Close()

	byRole := map[string]*SpineGroup{}
	for rows.Next() {
		var it SpineItem
		// text[] cannot be scanned into []string without a driver-specific array
		// wrapper — every row would silently fail its Scan. Join in SQL with a
		// one-byte separator (chr(31)) and split on the same one byte here.
		var path string
		var at sql.NullTime
		var total, rn int
		if err := rows.Scan(&it.ID, &it.Name, &it.Type, &it.Summary, &it.Depth,
			&path, &it.Via, &at, &total, &rn); err != nil {
			continue
		}
		if path != "" {
			it.Path = strings.Split(path, "\x1f")
		}
		if at.Valid {
			t := at.Time
			it.At = &t
		}
		g := byRole[it.Type]
		if g == nil {
			g = &SpineGroup{Role: it.Type, Total: total, Items: []SpineItem{}}
			byRole[it.Type] = g
			res.Totals[it.Type] = total
			res.Nodes += total
		}
		g.Items = append(g.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// A role the caller explicitly asked for and that came back empty must say
	// so with total=0 rather than silently vanishing — "no meetings last week"
	// and "I forgot to look for meetings" are different answers.
	for _, r := range q.Roles {
		if byRole[r] == nil {
			byRole[r] = &SpineGroup{Role: r, Items: []SpineItem{}}
			res.Totals[r] = 0
		}
	}

	order := map[string]int{}
	for i, r := range spineRoleOrder {
		order[r] = i
	}
	roles := make([]string, 0, len(byRole))
	for r := range byRole {
		roles = append(roles, r)
	}
	sort.Slice(roles, func(i, j int) bool {
		oi, oki := order[roles[i]]
		oj, okj := order[roles[j]]
		if oki != okj {
			return oki
		}
		if oki && okj && oi != oj {
			return oi < oj
		}
		return roles[i] < roles[j]
	})
	for _, r := range roles {
		g := byRole[r]
		g.Shown = len(g.Items)
		g.Capped = g.Shown < g.Total
		res.Returned += g.Shown
		res.Groups = append(res.Groups, *g)
	}
	return res, nil
}

// ── HTTP surface ──────────────────────────────────────────────────────────────

// Spine — POST /api/brain/graph/spine
//
//	{ namespace, entity, depth?, hubs?, roles?, perGroup?, since?, until?,
//	  window?, timeRoles? }
//
// Also accepts GET with the same names as query parameters, so it is curl-able.
func (s *Service) SpineHandler(w http.ResponseWriter, r *http.Request) {
	var q SpineQuery
	if r.Method == http.MethodGet {
		v := r.URL.Query()
		q.Namespace, q.Entity, q.Window = v.Get("namespace"), v.Get("entity"), v.Get("window")
		q.Depth, _ = strconv.Atoi(v.Get("depth"))
		q.PerGroup, _ = strconv.Atoi(v.Get("perGroup"))
		q.Hubs = splitCSV(v.Get("hubs"))
		q.Roles = splitCSV(v.Get("roles"))
		q.TimeRoles = splitCSV(v.Get("timeRoles"))
		if t, err := time.Parse(time.RFC3339, v.Get("since")); err == nil {
			q.Since = &t
		}
		if t, err := time.Parse(time.RFC3339, v.Get("until")); err == nil {
			q.Until = &t
		}
	} else if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if !s.canRead(r, q.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read access to brain "+q.Namespace))
		return
	}
	res, err := s.Store.Spine(r.Context(), q)
	if err == ErrNotFound && res != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"code": "not_found",
				"message": "no entity named " + q.Entity + " in brain " + q.Namespace},
			"candidates": res.Candidates})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
