package brain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// decodeJSONMap parses a jsonb column into a map (nil on empty/invalid).
func decodeJSONMap(b []byte) map[string]any {
	if len(b) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// Point-lookup and lifecycle ops (SPEC §5.1: memory_get / memory_forget /
// memory_share) plus namespace-grant scoping (F5). None of these touch the
// embedder/reranker, so they run fully against the live DB before TEI is
// reachable — unlike retain/recall.

// Structured errors mapped to the tool error model (contracts/tools.md).
var (
	ErrNotFound     = errors.New("brain: not found")
	ErrPermission   = errors.New("brain: permission denied")
	ErrInvalidInput = errors.New("brain: invalid argument")
)

// MemoryRow is the full record returned by memory_get.
type MemoryRow struct {
	ID           string         `json:"id"`
	Namespace    string         `json:"namespace"`
	OwnerAgentID string         `json:"ownerAgentId,omitempty"`
	Visibility   string         `json:"visibility"`
	Network      string         `json:"network"`
	MemoryType   string         `json:"memoryType"`
	Content      string         `json:"content"`
	SourceKind   string         `json:"sourceKind,omitempty"`
	SourceRef    string         `json:"sourceRef,omitempty"`
	Importance   float64        `json:"importance"`
	AccessCount  int            `json:"accessCount"`
	Tier         string         `json:"tier"`
	ValidAt      time.Time      `json:"validAt"`
	InvalidAt    *time.Time     `json:"invalidAt,omitempty"`
	SupersededBy string         `json:"supersededBy,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// Grant is a namespace_grants row (memory_share result).
type Grant struct {
	AgentID   string `json:"agentId"`
	Namespace string `json:"namespace"`
	CanRead   bool   `json:"canRead"`
	CanWrite  bool   `json:"canWrite"`
}

// --- scoping (F5) -------------------------------------------------------------
//
// agentID is the MCP session identity, never a client field. An empty agentID is
// the trusted/server context (the console, capture-mode) and bypasses grant
// checks — namespace scoping still fully isolates data. When an agentID IS set,
// a matching grant with the right flag is required; a missing grant is
// permission_denied, never silently-empty (contracts/tools.md).

func (s *Store) hasGrant(ctx context.Context, db *sql.DB, agentID, ns, col string) (bool, error) {
	if agentID == "" {
		return true, nil // trusted server context
	}
	var ok bool
	err := db.QueryRowContext(ctx,
		`SELECT `+col+` FROM namespace_grants WHERE agent_id=$1 AND namespace=$2`, agentID, ns).Scan(&ok)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ok, nil
}

// CanRead / CanWrite enforce namespace_grants for a (non-empty) agent identity.
func (s *Store) CanRead(ctx context.Context, agentID, ns string) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	return s.hasGrant(ctx, db, agentID, ns, "can_read")
}

func (s *Store) CanWrite(ctx context.Context, agentID, ns string) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	return s.hasGrant(ctx, db, agentID, ns, "can_write")
}

// --- memory_get ---------------------------------------------------------------

func (s *Store) Get(ctx context.Context, ns, id string) (*MemoryRow, error) {
	if ns == "" || id == "" {
		return nil, ErrInvalidInput
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var m MemoryRow
	var owner, sk, sr, sup sql.NullString
	var inv sql.NullTime
	var meta []byte
	err = db.QueryRowContext(ctx, `
		SELECT id::text, namespace, owner_agent_id, visibility, network, memory_type, content,
		       source_kind, source_ref, importance, access_count, tier, valid_at, invalid_at,
		       superseded_by::text, metadata
		FROM memories WHERE id=$1 AND namespace=$2
		ORDER BY valid_at DESC LIMIT 1`, id, ns).Scan(
		&m.ID, &m.Namespace, &owner, &m.Visibility, &m.Network, &m.MemoryType, &m.Content,
		&sk, &sr, &m.Importance, &m.AccessCount, &m.Tier, &m.ValidAt, &inv, &sup, &meta)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.OwnerAgentID, m.SourceKind, m.SourceRef, m.SupersededBy = owner.String, sk.String, sr.String, sup.String
	if inv.Valid {
		m.InvalidAt = &inv.Time
	}
	m.Metadata = decodeJSONMap(meta)
	return &m, nil
}

// --- memory_forget (soft-invalidate; never hard-delete) -----------------------

func (s *Store) Forget(ctx context.Context, ns, id, reason string) (time.Time, error) {
	if ns == "" || id == "" {
		return time.Time{}, ErrInvalidInput
	}
	db, err := s.db(ctx)
	if err != nil {
		return time.Time{}, err
	}
	var invAt time.Time
	err = db.QueryRowContext(ctx, `
		UPDATE memories
		   SET invalid_at = now(),
		       metadata = CASE WHEN $3 <> '' THEN metadata || jsonb_build_object('forget_reason',$3::text) ELSE metadata END
		 WHERE id=$1 AND namespace=$2 AND invalid_at IS NULL
		RETURNING invalid_at`, id, ns, reason).Scan(&invAt)
	if err == sql.ErrNoRows {
		return time.Time{}, ErrNotFound // absent, or already invalidated
	}
	if err != nil {
		return time.Time{}, err
	}
	s.bumpEpoch(ns) // recall results change
	s.event(ctx, db, "forget", ns, "", "hit", id, 0)
	return invAt, nil
}

// --- memory_share (upsert a namespace grant) ----------------------------------

func (s *Store) Share(ctx context.Context, ns, grantee string, canRead, canWrite bool) (*Grant, error) {
	if ns == "" || grantee == "" {
		return nil, ErrInvalidInput
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO namespace_grants (agent_id, namespace, can_read, can_write)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (agent_id, namespace)
		DO UPDATE SET can_read=EXCLUDED.can_read, can_write=EXCLUDED.can_write`,
		grantee, ns, canRead, canWrite)
	if err != nil {
		return nil, err
	}
	s.event(ctx, db, "share", ns, grantee, "hit", nil, 0)
	return &Grant{AgentID: grantee, Namespace: ns, CanRead: canRead, CanWrite: canWrite}, nil
}
