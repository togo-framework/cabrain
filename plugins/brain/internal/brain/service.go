package brain

import (
	"encoding/json"
	"net/http"
	"os"

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

// Ping is a health endpoint (GET /api/brain/ping). It also advertises whether the
// human console login gate is enforced (CABRAIN_REQUIRE_AUTH) so the SPA can
// decide to show the login page before hitting a gated endpoint.
func (s *Service) Ping(w http.ResponseWriter, r *http.Request) {
	authRequired := os.Getenv("CABRAIN_REQUIRE_AUTH") == "1" || os.Getenv("CABRAIN_REQUIRE_AUTH") == "true"
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"plugin": "brain", "status": "ok", "authRequired": authRequired,
	})
}
