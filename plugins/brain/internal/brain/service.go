package brain

import (
	"net/http"

	"github.com/togo-framework/togo"
)

// Service is the brain plugin backend — the CaBrain memory organ.
type Service struct{ k *togo.Kernel }

func New(k *togo.Kernel) *Service { return &Service{k: k} }

// Ping is a health endpoint (GET /api/brain/ping).
func (s *Service) Ping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"plugin":"brain","status":"ok"}`))
}
