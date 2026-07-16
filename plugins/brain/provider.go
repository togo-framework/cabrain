package brain

// Public provider contract for driver plugins (brain-tei, brain-cognee, …).
// External plugins import github.com/togo-framework/brain and register their
// implementation on the kernel; brain reads it lazily on the hot path. This
// re-exports the internal contract so external code never imports internal/.

import (
	"github.com/togo-framework/togo"

	ib "github.com/togo-framework/brain/internal/brain"
)

// Provider interfaces (aliases to the internal contract).
type (
	Embedder = ib.Embedder
	Reranker = ib.Reranker
	Engine   = ib.Engine
)

// RegisterEmbedder publishes the embeddings driver (e.g. brain-tei) onto the kernel.
func RegisterEmbedder(k *togo.Kernel, e Embedder) { ib.RegisterEmbedder(k, e) }

// RegisterReranker publishes the rerank driver (e.g. brain-tei) onto the kernel.
func RegisterReranker(k *togo.Kernel, r Reranker) { ib.RegisterReranker(k, r) }

// RegisterEngine publishes the cognify-engine driver (e.g. brain-cognee) onto the kernel.
func RegisterEngine(k *togo.Kernel, e Engine) { ib.RegisterEngine(k, e) }
