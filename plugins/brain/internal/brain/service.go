package brain

import (
	"net/http"

	"github.com/togo-framework/togo"
)

// Service is the brain plugin backend — the CaBrain memory organ. It owns the
// Store (data layer). Provider drivers (brain-tei, brain-cognee) publish onto the
// kernel via RegisterEmbedder/Reranker/Engine; the Store reads them lazily.
type Service struct {
	k     *togo.Kernel
	Store *Store
	hub   *hub // realtime SSE fan-out for multi-user live updates
}

func New(k *togo.Kernel) *Service {
	return &Service{k: k, Store: newStore(k), hub: newHub()}
}

// Ping is a health endpoint (GET /api/brain/ping).
func (s *Service) Ping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"plugin":"brain","status":"ok"}`))
}
