package brain

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Knowledge gaps — questions the brain couldn't answer (recall returned empty).
// Captured so the operator can index the missing knowledge via MCP / chat /
// dashboard, then mark the gap resolved.

// Gap is one missed question.
type Gap struct {
	ID         int64     `json:"id"`
	Namespace  string    `json:"namespace"`
	Query      string    `json:"query"`
	Hits       int       `json:"hits"`
	Status     string    `json:"status"` // open | indexed | dismissed
	Resolution string    `json:"resolution,omitempty"`
	FirstSeen  time.Time `json:"firstSeen"`
	LastSeen   time.Time `json:"lastSeen"`
}

// recordGap upserts a missed query (best-effort; never fails the recall path).
func (s *Store) recordGap(ctx context.Context, db *sql.DB, ns, query string) {
	q := trimTo(query, 2000)
	if q == "" {
		return
	}
	_, _ = db.ExecContext(ctx, `
		INSERT INTO memory_gaps (namespace, query, norm_query)
		VALUES ($1, $2, $3)
		ON CONFLICT (namespace, norm_query)
		DO UPDATE SET hits = memory_gaps.hits + 1, last_seen = now(),
		              status = CASE WHEN memory_gaps.status = 'dismissed'
		                            THEN memory_gaps.status ELSE memory_gaps.status END`,
		ns, q, normText(q))
}

// Gaps lists knowledge gaps. status "" → open+indexed (not dismissed); "all" → all.
func (s *Store) Gaps(ctx context.Context, namespace, status string, limit int) ([]Gap, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	db, err := s.db(ctx)
	if err != nil || !s.ready(ctx, db) {
		return []Gap{}, nil
	}
	where := "status <> 'dismissed'"
	args := []any{}
	i := 1
	if status == "all" {
		where = "TRUE"
	} else if status != "" {
		where = "status = $" + itoa(i)
		args = append(args, status)
		i++
	}
	if namespace != "" {
		where += " AND namespace = $" + itoa(i)
		args = append(args, namespace)
		i++
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, namespace, query, hits, status, COALESCE(resolution,''), first_seen, last_seen
		FROM memory_gaps WHERE `+where+`
		ORDER BY (status='open') DESC, hits DESC, last_seen DESC
		LIMIT `+itoa(limit), args...)
	if err != nil {
		return []Gap{}, nil
	}
	defer rows.Close()
	out := []Gap{}
	for rows.Next() {
		var g Gap
		if err := rows.Scan(&g.ID, &g.Namespace, &g.Query, &g.Hits, &g.Status,
			&g.Resolution, &g.FirstSeen, &g.LastSeen); err == nil {
			out = append(out, g)
		}
	}
	return out, nil
}

// ResolveGap sets a gap's status (indexed | dismissed | open) + optional note.
func (s *Store) ResolveGap(ctx context.Context, id int64, status, resolution string) error {
	if status != "indexed" && status != "dismissed" && status != "open" {
		return ErrInvalidInput
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx,
		`UPDATE memory_gaps SET status=$2, resolution=NULLIF($3,''), last_seen=now() WHERE id=$1`,
		id, status, resolution)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GapsCount returns the number of open gaps (for the dashboard metric).
func (s *Store) GapsCount(ctx context.Context) int {
	db, err := s.db(ctx)
	if err != nil || !s.ready(ctx, db) {
		return 0
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM memory_gaps WHERE status='open'`).Scan(&n)
	return n
}

func trimTo(s string, n int) string {
	s = normSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// normSpace trims + collapses internal whitespace (keeps original case).
func normSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
