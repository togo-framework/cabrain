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

func apiErr(code, msg string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": msg}}
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
