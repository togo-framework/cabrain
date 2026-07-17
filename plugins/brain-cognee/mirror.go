package braincognee

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/togo-framework/brain-cognee/internal/cognee"
)

// MirrorResult summarizes a graph-mirror run.
type MirrorResult struct {
	Namespace       string
	DatasetID       string
	EntitiesUpated  int // entity rows upserted
	MemoryLinks     int // memory_entities rows upserted
	EntitiesInGraph int // distinct entity names seen in the Cognee graph
}

// clientFromEnv builds a Cognee client from the same env the plugin uses.
func clientFromEnv() (*cognee.Client, error) {
	base := os.Getenv("COGNEE_API_URL")
	if base == "" {
		return nil, fmt.Errorf("brain-cognee: COGNEE_API_URL unset")
	}
	return cognee.New(base, os.Getenv("COGNEE_ADMIN_EMAIL"), os.Getenv("COGNEE_API_TOKEN")), nil
}

// Mirror pulls a namespace's Cognee knowledge graph and mirrors it into CaBrain's
// Postgres tables (SPEC §7), so the Graph Explorer + 1-hop recall read entities
// from Postgres instead of hitting Cognee on the hot path:
//
//   - entity nodes → entities(namespace, name)         (embedding left NULL)
//   - memory→entity links → memory_entities            (via the mem-<uuid> doc naming)
//
// Idempotent (ON CONFLICT DO NOTHING). The Cognee client authenticates with the
// admin login (COGNEE_ADMIN_EMAIL / COGNEE_API_TOKEN).
func Mirror(ctx context.Context, db *sql.DB, namespace string) (*MirrorResult, error) {
	if db == nil {
		return nil, fmt.Errorf("brain-cognee.Mirror: nil db")
	}
	c, err := clientFromEnv()
	if err != nil {
		return nil, err
	}
	dsID, err := c.DatasetIDByName(ctx, namespace)
	if err != nil {
		return nil, err
	}
	graph, err := c.FetchGraph(ctx, dsID)
	if err != nil {
		return nil, fmt.Errorf("brain-cognee.Mirror: fetch graph (dataset %s): %w", dsID, err)
	}

	res := &MirrorResult{Namespace: namespace, DatasetID: dsID}

	// 1. Entities. Map name → entity id so we can resolve memory_entities below.
	names := graph.EntityNames()
	res.EntitiesInGraph = len(names)
	idByName := make(map[string]string, len(names))
	for _, name := range names {
		var id string
		// Upsert then read back the id (works whether it was inserted or already present).
		err := db.QueryRowContext(ctx, `
			INSERT INTO entities (namespace, name) VALUES ($1, $2)
			ON CONFLICT (namespace, name) DO UPDATE SET name = EXCLUDED.name
			RETURNING id::text`, namespace, name).Scan(&id)
		if err != nil {
			return res, fmt.Errorf("brain-cognee.Mirror: upsert entity %q: %w", name, err)
		}
		idByName[name] = id
		res.EntitiesUpated++
	}

	// 2. memory_entities (best-effort). Only link when the memory row actually
	//    exists in this namespace, so a stale/foreign id never leaks in.
	for _, link := range graph.MemoryEntityLinks() {
		eid, ok := idByName[link.EntityName]
		if !ok {
			continue
		}
		var exists bool
		if err := db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM memories WHERE id = $1 AND namespace = $2)`,
			link.MemoryID, namespace).Scan(&exists); err != nil || !exists {
			continue
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO memory_entities (memory_id, entity_id) VALUES ($1, $2)
			ON CONFLICT (memory_id, entity_id) DO NOTHING`, link.MemoryID, eid); err != nil {
			return res, fmt.Errorf("brain-cognee.Mirror: link memory_entities: %w", err)
		}
		res.MemoryLinks++
	}
	return res, nil
}
