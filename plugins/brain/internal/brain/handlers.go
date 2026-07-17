package brain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}

// HTTP surface for the console. Read endpoints are always safe (defensive
// queries). Write/recall endpoints return a clear, structured error until the
// providers (brain-tei) and the live cabrain DB are wired (Blocker B).

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// GET /api/brain/stats
func (s *Service) Stats(w http.ResponseWriter, r *http.Request) {
	st, _ := s.Store.Stats(r.Context())
	writeJSON(w, http.StatusOK, st)
}

// GET /api/brain/activity?limit=50
func (s *Service) Activity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, _ := s.Store.Activity(r.Context(), limit)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/brain/namespaces
func (s *Service) Namespaces(w http.ResponseWriter, r *http.Request) {
	ns, _ := s.Store.Namespaces(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"brains": ns})
}

// GET /api/brain/graph?namespace=&limit=200
func (s *Service) Graph(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	g, _ := s.Store.Graph(r.Context(), r.URL.Query().Get("namespace"), limit)
	writeJSON(w, http.StatusOK, g)
}

// POST /api/brain/recall  { namespace, query, limit }
func (s *Service) Recall(w http.ResponseWriter, r *http.Request) {
	var q RecallQuery
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if q.Namespace == "" || q.Query == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace and query are required"))
		return
	}
	if !s.canRead(r, q.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read access to brain "+q.Namespace))
		return
	}
	res, err := s.Store.Recall(r.Context(), q)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, unavailable(err))
		return
	}
	s.hub.publish("recall", map[string]any{"namespace": q.Namespace, "count": len(res)})
	if len(res) == 0 {
		s.hub.publish("gap", map[string]any{"namespace": q.Namespace, "query": q.Query})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

// POST /api/brain/search  { query, namespaces?, limit }  (cross-brain search engine)
func (s *Service) Search(w http.ResponseWriter, r *http.Request) {
	var q SearchQuery
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if q.Query == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "query is required"))
		return
	}
	// Scope a non-admin caller's search to the brains they can read.
	if c := s.identify(r); !c.admin {
		cand := q.Namespaces
		if len(cand) == 0 {
			for _, b := range mustNamespaces(s, r) {
				cand = append(cand, b)
			}
		}
		allowed := []string{}
		for _, ns := range cand {
			if s.canRead(r, ns) {
				allowed = append(allowed, ns)
			}
		}
		if len(allowed) == 0 {
			writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no readable brains for this token"))
			return
		}
		q.Namespaces = allowed
	}
	res, err := s.Store.SearchAll(r.Context(), q)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, unavailable(err))
		return
	}
	s.hub.publish("search", map[string]any{"count": len(res)})
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

// POST /api/brain/retain  { namespace, content, sourceKind, ... }
func (s *Service) Retain(w http.ResponseWriter, r *http.Request) {
	var in MemoryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.Namespace == "" || in.Content == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace and content are required"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+in.Namespace))
		return
	}
	res, err := s.Store.Retain(r.Context(), in)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, unavailable(err))
		return
	}
	s.hub.publish("retain", map[string]any{"namespace": in.Namespace, "decision": res.Decision})
	writeJSON(w, http.StatusOK, res)
}

// agentID reads the MCP/session identity from a trusted header. Empty = the
// server/console context (grant checks bypassed; namespace scoping still applies).
// caller is the resolved identity of a request.
type caller struct {
	agent string
	admin bool // admin bypasses grants
	valid bool // a presented token resolved (or no token needed)
}

// identify resolves the caller from the X-Cabrain-Token header (preferred) or the
// X-Agent-Id header. A tokenless request is the trusted local console UNLESS
// CABRAIN_REQUIRE_TOKEN=1. An invalid token resolves to no access.
func (s *Service) identify(r *http.Request) caller {
	if tok := r.Header.Get("X-Cabrain-Token"); tok != "" {
		if agent, admin, ok := s.Store.ResolveToken(r.Context(), tok); ok {
			return caller{agent: agent, admin: admin, valid: true}
		}
		return caller{valid: false} // bad/revoked token → deny
	}
	agent := r.Header.Get("X-Agent-Id")
	// No token presented. The X-Cabrain-Token ACL is the enforcement mechanism; a
	// bare X-Agent-Id is only an identity label (activity attribution), NOT a
	// credential. So unless token enforcement is explicitly ON, a tokenless caller
	// is the trusted local console/MCP — EVEN when it sends an agent id. (Previously
	// a tokenless call that set X-Agent-Id fell into grant checks and got denied on
	// every brain — the local .mcp sets CABRAIN_AGENT_ID=claude-code, so it locked
	// itself out.)
	if os.Getenv("CABRAIN_REQUIRE_TOKEN") != "1" {
		return caller{agent: agent, admin: true, valid: true}
	}
	// Enforcement ON + no token → must be a known, granted agent (never admin).
	return caller{agent: agent, valid: agent != ""}
}

// ValidToken reports whether a raw token resolves to a live (non-revoked) token.
// Used by the security gate to let MCP callers (who present X-Cabrain-Token, not a
// login session) through when console auth enforcement is on.
func (s *Service) ValidToken(ctx context.Context, tok string) bool {
	_, _, ok := s.Store.ResolveToken(ctx, tok)
	return ok
}

func (s *Service) canRead(r *http.Request, ns string) bool {
	c := s.identify(r)
	if c.admin {
		return true
	}
	if !c.valid || c.agent == "" {
		return false
	}
	ok, _ := s.Store.CanRead(r.Context(), c.agent, ns)
	return ok
}

func (s *Service) canWrite(r *http.Request, ns string) bool {
	c := s.identify(r)
	if c.admin {
		return true
	}
	if !c.valid || c.agent == "" {
		return false
	}
	ok, _ := s.Store.CanWrite(r.Context(), c.agent, ns)
	return ok
}

func agentID(r *http.Request) string { return r.Header.Get("X-Agent-Id") }

// GET /api/brain/memory?namespace=&id=   (memory_get)
func (s *Service) Get(w http.ResponseWriter, r *http.Request) {
	ns, id := r.URL.Query().Get("namespace"), r.URL.Query().Get("id")
	if !s.canRead(r, ns) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read access to brain "+ns))
		return
	}
	m, err := s.Store.Get(r.Context(), ns, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// POST /api/brain/forget  { namespace, id, reason }   (memory_forget)
func (s *Service) Forget(w http.ResponseWriter, r *http.Request) {
	var in struct{ Namespace, ID, Reason string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write grant for namespace"))
		return
	}
	invAt, err := s.Store.Forget(r.Context(), in.Namespace, in.ID, in.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "invalidAt": invAt})
}

// POST /api/brain/share  { namespace, granteeAgentId, canRead, canWrite }   (memory_share)
func (s *Service) Share(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace      string `json:"namespace"`
		GranteeAgentID string `json:"granteeAgentId"`
		CanRead        *bool  `json:"canRead"`
		CanWrite       *bool  `json:"canWrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	// Caller must already hold a grant on the namespace (bootstrap seeded out-of-band).
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "caller has no grant on namespace"))
		return
	}
	canRead, canWrite := true, false
	if in.CanRead != nil {
		canRead = *in.CanRead
	}
	if in.CanWrite != nil {
		canWrite = *in.CanWrite
	}
	g, err := s.Store.Share(r.Context(), in.Namespace, in.GranteeAgentID, canRead, canWrite)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// GET /api/brain/gaps?namespace=&status=&limit=   (knowledge gaps / missed questions)
func (s *Service) Gaps(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	gaps, _ := s.Store.Gaps(r.Context(), r.URL.Query().Get("namespace"), r.URL.Query().Get("status"), limit)
	writeJSON(w, http.StatusOK, map[string]any{"gaps": gaps})
}

// POST /api/brain/gaps/resolve  { id, status, resolution }
func (s *Service) ResolveGap(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID         int64  `json:"id"`
		Status     string `json:"status"`
		Resolution string `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if err := s.Store.ResolveGap(r.Context(), in.ID, in.Status, in.Resolution); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("gap", map[string]any{"resolved": in.ID, "status": in.Status})
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "status": in.Status})
}

// GET /api/brain/brain?namespace=   (brain details)
func (s *Service) BrainDetail(w http.ResponseWriter, r *http.Request) {
	d, _ := s.Store.BrainDetail(r.Context(), r.URL.Query().Get("namespace"))
	writeJSON(w, http.StatusOK, d)
}

// GET /api/brain/export?namespace=   (portable NDJSON)
func (s *Service) Export(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace required"))
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", "attachment; filename=\"cabrain-"+ns+".ndjson\"")
	_, _ = s.Store.Export(r.Context(), ns, w)
}

// POST /api/brain/import?namespace=   (body = NDJSON export; namespace overrides)
func (s *Service) Import(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.Import(r.Context(), r.URL.Query().Get("namespace"), r.Body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": n})
}

// POST /api/brain/brain/delete  { namespace, confirm }  (confirm must equal namespace)
func (s *Service) DeleteBrain(w http.ResponseWriter, r *http.Request) {
	var in struct{ Namespace, Confirm string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.Namespace == "" || in.Confirm != in.Namespace {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "set confirm = namespace to delete"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+in.Namespace))
		return
	}
	n, err := s.Store.DeleteBrain(r.Context(), in.Namespace)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("brain", map[string]any{"deleted": in.Namespace})
	writeJSON(w, http.StatusOK, map[string]any{"namespace": in.Namespace, "deleted": n})
}

// POST /api/brain/memory/edit  { namespace, id, content?, importance?, metadata? }
func (s *Service) EditMemory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace  string         `json:"namespace"`
		ID         string         `json:"id"`
		Content    string         `json:"content"`
		Importance float64        `json:"importance"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+in.Namespace))
		return
	}
	if err := s.Store.EditMemory(r.Context(), in.Namespace, in.ID, in.Content, in.Importance, in.Metadata); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "updated": true})
}

// POST /api/brain/chat  { namespace, message, history?, topK? }
// Live agent: chat with a selected brain (RAG grounded in its memories, with
// citations + a footprint). Read access on the brain is required.
func (s *Service) Chat(w http.ResponseWriter, r *http.Request) {
	var in ChatQuery
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.Namespace == "" || strings.TrimSpace(in.Message) == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace and message required"))
		return
	}
	if !s.canRead(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no access to brain "+in.Namespace))
		return
	}
	ans, err := s.Store.Chat(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("chat", map[string]any{"namespace": in.Namespace, "recalled": ans.Footprint.Recalled})
	writeJSON(w, http.StatusOK, ans)
}

// --- Per-brain secrets vault -------------------------------------------------

// GET /api/brain/secrets?namespace=   → metadata only (names + masked hints), canRead.
func (s *Service) SecretsList(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace required"))
		return
	}
	if !s.canRead(r, ns) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no access to brain "+ns))
		return
	}
	items, err := s.Store.ListSecrets(r.Context(), ns)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []SecretMeta{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": items})
}

// POST /api/brain/secrets  { namespace, name, value, kind? }  → store/update, canWrite.
func (s *Service) SecretPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace, Name, Value, Kind string
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.Namespace == "" || in.Name == "" || in.Value == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace, name, value required"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+in.Namespace))
		return
	}
	if in.Kind == "" {
		in.Kind = "generic"
	}
	if err := s.Store.PutSecret(r.Context(), in.Namespace, sanitizeSecretName(in.Name), in.Value, in.Kind, "console", s.identify(r).agent); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("secret", map[string]any{"namespace": in.Namespace, "name": in.Name, "op": "put"})
	writeJSON(w, http.StatusOK, map[string]any{"namespace": in.Namespace, "name": in.Name, "stored": true})
}

// POST /api/brain/secrets/reveal  { namespace, name }  → decrypted value.
// Stricter than read: requires write/admin on the brain (revealing a raw secret).
func (s *Service) SecretReveal(w http.ResponseWriter, r *http.Request) {
	var in struct{ Namespace, Name string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.Namespace == "" || in.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace and name required"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "revealing a secret needs write/admin on brain "+in.Namespace))
		return
	}
	val, err := s.Store.RevealSecret(r.Context(), in.Namespace, in.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("secret", map[string]any{"namespace": in.Namespace, "name": in.Name, "op": "reveal"})
	writeJSON(w, http.StatusOK, map[string]any{"namespace": in.Namespace, "name": in.Name, "value": val})
}

// POST /api/brain/secrets/delete  { namespace, name }  → delete, canWrite.
func (s *Service) SecretDelete(w http.ResponseWriter, r *http.Request) {
	var in struct{ Namespace, Name string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+in.Namespace))
		return
	}
	ok, err := s.Store.DeleteSecret(r.Context(), in.Namespace, in.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("secret", map[string]any{"namespace": in.Namespace, "name": in.Name, "op": "delete"})
	writeJSON(w, http.StatusOK, map[string]any{"namespace": in.Namespace, "name": in.Name, "deleted": ok})
}

func mustNamespaces(s *Service, r *http.Request) []string {
	ns, _ := s.Store.Namespaces(r.Context())
	out := make([]string, 0, len(ns))
	for _, b := range ns {
		out = append(out, b.Namespace)
	}
	return out
}

// --- ACL management (admin-only) ---------------------------------------------

func (s *Service) adminOnly(r *http.Request) bool { return s.identify(r).admin }

// POST /api/brain/tokens  { agentId, label, isAdmin }
func (s *Service) CreateToken(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(r) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "admin only"))
		return
	}
	var in struct {
		AgentID string `json:"agentId"`
		Label   string `json:"label"`
		IsAdmin bool   `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	t, err := s.Store.CreateToken(r.Context(), in.AgentID, in.Label, in.IsAdmin)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// GET /api/brain/tokens?includeRevoked=
func (s *Service) ListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(r) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "admin only"))
		return
	}
	toks, _ := s.Store.ListTokens(r.Context(), r.URL.Query().Get("includeRevoked") == "1")
	writeJSON(w, http.StatusOK, map[string]any{"tokens": toks})
}

// POST /api/brain/tokens/revoke  { token }
func (s *Service) RevokeToken(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(r) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "admin only"))
		return
	}
	var in struct{ Token string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if err := s.Store.RevokeToken(r.Context(), in.Token); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// POST /api/brain/grant  { agentId, namespace, canRead, canWrite }   (admin)
func (s *Service) GrantBrain(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(r) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "admin only"))
		return
	}
	var in struct {
		AgentID   string `json:"agentId"`
		Namespace string `json:"namespace"`
		CanRead   *bool  `json:"canRead"`
		CanWrite  *bool  `json:"canWrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	cr, cw := true, false
	if in.CanRead != nil {
		cr = *in.CanRead
	}
	if in.CanWrite != nil {
		cw = *in.CanWrite
	}
	g, err := s.Store.Share(r.Context(), in.Namespace, in.AgentID, cr, cw)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("grant", map[string]any{"agentId": in.AgentID, "namespace": in.Namespace})
	writeJSON(w, http.StatusOK, g)
}

// POST /api/brain/grant/revoke  { agentId, namespace }   (admin)
func (s *Service) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(r) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "admin only"))
		return
	}
	var in struct{ AgentID, Namespace string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if err := s.Store.RevokeGrant(r.Context(), in.AgentID, in.Namespace); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// POST /api/brain/session { namespace, write?, label? } — mint a scoped token and
// return a ready-to-use Claude Code session config bound to that brain (omnigent-
// style). Caller must be able to read the brain (and write, if write requested).
func (s *Service) Session(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace string `json:"namespace"`
		Write     bool   `json:"write"`
		Label     string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.Namespace == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace required"))
		return
	}
	if !s.canRead(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no access to brain "+in.Namespace))
		return
	}
	if in.Write && !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+in.Namespace))
		return
	}
	agent := "session-" + in.Namespace + "-" + randHex(4)
	label := in.Label
	if label == "" {
		label = "session for " + in.Namespace
	}
	t, err := s.Store.CreateToken(r.Context(), agent, label, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.Store.Share(r.Context(), in.Namespace, agent, true, in.Write); err != nil {
		writeErr(w, err)
		return
	}
	pub := os.Getenv("CABRAIN_PUBLIC_URL")
	if pub == "" {
		pub = "http://localhost:8080"
	}
	mcp := map[string]any{"mcpServers": map[string]any{"cabrain": map[string]any{
		"command": "brain-mcp",
		"env": map[string]any{
			"CABRAIN_API_URL":           pub,
			"CABRAIN_TOKEN":             t.Token,
			"CABRAIN_DEFAULT_NAMESPACE": in.Namespace,
		},
	}}}
	s.hub.publish("session", map[string]any{"namespace": in.Namespace, "agentId": agent, "write": in.Write})
	writeJSON(w, http.StatusOK, map[string]any{
		"agentId":   agent,
		"namespace": in.Namespace,
		"write":     in.Write,
		"token":     t.Token,
		"mcpConfig": mcp,
		"howto": "Install: go install ./cmd/brain-mcp. Drop mcpConfig into .mcp.json, then start Claude Code — " +
			"it recalls/retains against brain '" + in.Namespace + "' by default, with " +
			map[bool]string{true: "read+write", false: "read-only"}[in.Write] + " access.",
	})
}

// --- Data sources (connectors) -----------------------------------------------

// GET /api/brain/datasources?namespace=   → list a brain's configured sources. canRead.
func (s *Service) Datasources(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace required"))
		return
	}
	if !s.canRead(r, ns) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read access to brain "+ns))
		return
	}
	items, err := s.Store.ListDatasources(r.Context(), ns)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []Datasource{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"datasources": items, "kinds": ConnectorKinds()})
}

// POST /api/brain/datasources  { namespace, kind, name, config }  → create. canWrite.
func (s *Service) CreateDatasource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Namespace string         `json:"namespace"`
		Kind      string         `json:"kind"`
		Name      string         `json:"name"`
		Config    map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.Namespace == "" || in.Kind == "" || in.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "namespace, kind, name required"))
		return
	}
	if !s.canWrite(r, in.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+in.Namespace))
		return
	}
	ds, err := s.Store.CreateDatasource(r.Context(), in.Namespace, in.Kind, in.Name, in.Config)
	if err != nil {
		writeErr(w, err)
		return
	}
	redactDatasourceSecrets(ds)
	s.hub.publish("datasource", map[string]any{"namespace": in.Namespace, "op": "create", "id": ds.ID, "kind": ds.Kind})
	writeJSON(w, http.StatusOK, ds)
}

// POST /api/brain/datasources/sync  { id }  → run the connector, retain its docs. canWrite.
func (s *Service) SyncDatasource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.ID == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "id required"))
		return
	}
	ds, err := s.Store.getDatasource(r.Context(), in.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !s.canWrite(r, ds.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+ds.Namespace))
		return
	}
	res, err := s.Store.SyncDatasource(r.Context(), in.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("datasource", map[string]any{"namespace": ds.Namespace, "op": "sync", "id": in.ID, "ingested": res.Ingested, "status": res.Status})
	writeJSON(w, http.StatusOK, res)
}

// POST /api/brain/datasources/delete  { id }  → remove a source. canWrite.
func (s *Service) DeleteDatasource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if in.ID == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "id required"))
		return
	}
	ds, err := s.Store.getDatasource(r.Context(), in.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !s.canWrite(r, ds.Namespace) {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no write access to brain "+ds.Namespace))
		return
	}
	ok, err := s.Store.DeleteDatasource(r.Context(), in.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("datasource", map[string]any{"namespace": ds.Namespace, "op": "delete", "id": in.ID})
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "deleted": ok})
}

// POST /api/brain/ingest/{id}  → PUSH path for webhook datasources. NOT gated by the
// normal ACL: it authenticates via the X-Webhook-Secret header matched against the
// datasource's stored config.secret. Body { content, sourceRef?, metadata? }.
func (s *Service) IngestWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "id required"))
		return
	}
	secret, ns, err := s.Store.webhookSecret(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	presented := r.Header.Get("X-Webhook-Secret")
	if secret == "" || presented != secret {
		writeJSON(w, http.StatusUnauthorized, apiErr("unauthenticated", "invalid or missing X-Webhook-Secret"))
		return
	}
	var in struct {
		Content   string         `json:"content"`
		SourceRef string         `json:"sourceRef"`
		Metadata  map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "bad JSON body"))
		return
	}
	if strings.TrimSpace(in.Content) == "" {
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", "content required"))
		return
	}
	n, err := s.Store.IngestWebhook(r.Context(), id, in.Content, in.SourceRef, in.Metadata)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.hub.publish("datasource", map[string]any{"namespace": ns, "op": "ingest", "id": id, "ingested": n})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "ingested": n})
}

func apiErr(code, msg string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": msg}}
}

// writeErr maps the ops error sentinels to the shared tool error model.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, apiErr("not_found", err.Error()))
	case errors.Is(err, ErrPermission):
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", err.Error()))
	case errors.Is(err, ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, apiErr("invalid_argument", err.Error()))
	default:
		writeJSON(w, http.StatusServiceUnavailable, unavailable(err))
	}
}

// unavailable maps a not-yet-wired provider error to a structured, honest
// response the UI can render (Blocker B), rather than a 500.
func unavailable(err error) map[string]any {
	code := "unavailable"
	if errors.Is(err, ErrNoEmbedder) {
		code = "no_embedder"
	}
	return map[string]any{"error": map[string]string{"code": code, "message": err.Error()}}
}
