package brain

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// ACL: access tokens + per-brain grants. A token maps to an agent identity; the
// agent's brain access lives in namespace_grants. Admin tokens bypass grants.

// Token is an access token record (with its grants filled in by ListTokens).
type Token struct {
	Token      string     `json:"token"`
	AgentID    string     `json:"agentId"`
	Label      string     `json:"label,omitempty"`
	IsAdmin    bool       `json:"isAdmin"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Revoked    bool       `json:"revoked"`
	Grants     []Grant    `json:"grants,omitempty"`
}

// ResolveToken returns the agent id + admin flag for a token (and bumps last_used).
// ok=false if the token is unknown or revoked.
func (s *Store) ResolveToken(ctx context.Context, token string) (agentID string, isAdmin bool, ok bool) {
	if token == "" {
		return "", false, false
	}
	db, err := s.db(ctx)
	if err != nil {
		return "", false, false
	}
	err = db.QueryRowContext(ctx,
		`SELECT agent_id, is_admin FROM brain_tokens WHERE token=$1 AND revoked_at IS NULL`, token).
		Scan(&agentID, &isAdmin)
	if err != nil {
		return "", false, false
	}
	_, _ = db.ExecContext(ctx, `UPDATE brain_tokens SET last_used_at=now() WHERE token=$1`, token)
	return agentID, isAdmin, true
}

// CreateToken issues a new token for an agent identity.
func (s *Store) CreateToken(ctx context.Context, agentID, label string, isAdmin bool) (*Token, error) {
	if agentID == "" {
		return nil, ErrInvalidInput
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	tok := "cbt_" + hex.EncodeToString(b)
	_, err = db.ExecContext(ctx,
		`INSERT INTO brain_tokens (token, agent_id, label, is_admin) VALUES ($1,$2,$3,$4)`,
		tok, agentID, nullStr(label), isAdmin)
	if err != nil {
		return nil, err
	}
	return &Token{Token: tok, AgentID: agentID, Label: label, IsAdmin: isAdmin, CreatedAt: time.Now()}, nil
}

// RevokeToken disables a token.
func (s *Store) RevokeToken(ctx context.Context, token string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `UPDATE brain_tokens SET revoked_at=now() WHERE token=$1 AND revoked_at IS NULL`, token)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTokens returns tokens (optionally including revoked) with their brain grants.
func (s *Store) ListTokens(ctx context.Context, includeRevoked bool) ([]Token, error) {
	db, err := s.db(ctx)
	if err != nil || !s.ready(ctx, db) {
		return []Token{}, nil
	}
	where := "revoked_at IS NULL"
	if includeRevoked {
		where = "TRUE"
	}
	rows, err := db.QueryContext(ctx,
		`SELECT token, agent_id, COALESCE(label,''), is_admin, created_at, last_used_at, revoked_at
		 FROM brain_tokens WHERE `+where+` ORDER BY created_at DESC`)
	if err != nil {
		return []Token{}, nil
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		var last, rev sql.NullTime
		if err := rows.Scan(&t.Token, &t.AgentID, &t.Label, &t.IsAdmin, &t.CreatedAt, &last, &rev); err != nil {
			continue
		}
		if last.Valid {
			t.LastUsedAt = &last.Time
		}
		t.Revoked = rev.Valid
		t.Grants, _ = s.grantsFor(ctx, db, t.AgentID)
		out = append(out, t)
	}
	return out, nil
}

// grantsFor returns an agent's per-brain grants.
func (s *Store) grantsFor(ctx context.Context, db *sql.DB, agentID string) ([]Grant, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT agent_id, namespace, can_read, can_write FROM namespace_grants WHERE agent_id=$1 ORDER BY namespace`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if rows.Scan(&g.AgentID, &g.Namespace, &g.CanRead, &g.CanWrite) == nil {
			out = append(out, g)
		}
	}
	return out, nil
}

// Grants lists all grants for an agent (public helper for the ACL UI/MCP).
func (s *Store) Grants(ctx context.Context, agentID string) ([]Grant, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	return s.grantsFor(ctx, db, agentID)
}

// Revoke removes an agent's grant on a namespace.
func (s *Store) RevokeGrant(ctx context.Context, agentID, namespace string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM namespace_grants WHERE agent_id=$1 AND namespace=$2`, agentID, namespace)
	return err
}
