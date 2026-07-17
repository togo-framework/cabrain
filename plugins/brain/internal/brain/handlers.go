package brain

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

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
	res, err := s.Store.Recall(r.Context(), q)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, unavailable(err))
		return
	}
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
	res, err := s.Store.Retain(r.Context(), in)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, unavailable(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// agentID reads the MCP/session identity from a trusted header. Empty = the
// server/console context (grant checks bypassed; namespace scoping still applies).
func agentID(r *http.Request) string { return r.Header.Get("X-Agent-Id") }

// GET /api/brain/memory?namespace=&id=   (memory_get)
func (s *Service) Get(w http.ResponseWriter, r *http.Request) {
	ns, id := r.URL.Query().Get("namespace"), r.URL.Query().Get("id")
	if ok, err := s.Store.CanRead(r.Context(), agentID(r), ns); err == nil && !ok {
		writeJSON(w, http.StatusForbidden, apiErr("permission_denied", "no read grant for namespace"))
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
	if ok, err := s.Store.CanWrite(r.Context(), agentID(r), in.Namespace); err == nil && !ok {
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
	if ok, err := s.Store.CanWrite(r.Context(), agentID(r), in.Namespace); err == nil && !ok {
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
	n, err := s.Store.DeleteBrain(r.Context(), in.Namespace)
	if err != nil {
		writeErr(w, err)
		return
	}
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
	if err := s.Store.EditMemory(r.Context(), in.Namespace, in.ID, in.Content, in.Importance, in.Metadata); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "updated": true})
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
