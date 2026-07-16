package brain

import (
	"net/http"

	"github.com/togo-framework/togo"
)

// Service is the brain plugin backend — the CaBrain memory organ. It owns the
// Store (data layer) and is the object driver plugins (brain-tei, brain-cognee)
// fetch from the kernel to register their providers.
type Service struct {
	k     *togo.Kernel
	Store *Store
}

func New(k *togo.Kernel) *Service {
	return &Service{k: k, Store: newStore(k)}
}

// Ping is a health endpoint (GET /api/brain/ping).
func (s *Service) Ping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"plugin":"brain","status":"ok"}`))
}

// UseEmbedder / UseReranker / UseEngine let driver plugins register providers:
//
//	if svc, ok := k.Get("brain").(*brain.Service); ok { svc.UseEmbedder(tei) }
func (s *Service) UseEmbedder(e Embedder) { s.Store.UseEmbedder(e) }
func (s *Service) UseReranker(r Reranker) { s.Store.UseReranker(r) }
func (s *Service) UseEngine(e Engine)     { s.Store.UseEngine(e) }
