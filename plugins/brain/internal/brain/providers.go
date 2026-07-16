package brain

import (
	"context"

	"github.com/togo-framework/togo"
)

// Provider seams. The self-hosted embedding/rerank plane and the cognify engine
// are separate driver plugins (brain-tei, brain-cognee) that publish their
// implementations onto the kernel under the well-known keys below; brain reads
// them lazily on the hot path (order-independent — no boot coupling). When a
// provider is absent, the dependent op returns a clear "install <plugin>" error
// rather than silently degrading.

// Embedder turns text into dense vectors (TEI → BAAI/bge-m3, 1024-dim).
type Embedder interface {
	// Embed returns one vector per input text, each of length Dim().
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dim is the embedding width; must match the memories.embedding column.
	Dim() int
}

// Reranker reorders candidate documents against a query (bge-reranker-v2-m3).
type Reranker interface {
	// Rerank returns a relevance score per doc, aligned to the input order.
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

// Engine is the cognify engine (Cognee): entity/graph extraction from a memory.
// Optional — retain/recall work without it; it enriches the entity graph used
// for 1-hop spreading activation.
type Engine interface {
	// Cognify extracts entities/relations for a stored memory and populates the
	// entities / memory_entities graph for the namespace.
	Cognify(ctx context.Context, namespace, memoryID, content string) error
}

// Well-known kernel keys the driver plugins publish under.
const (
	keyEmbedder = "brain.embedder"
	keyReranker = "brain.reranker"
	keyEngine   = "brain.engine"
)

// RegisterEmbedder/Reranker/Engine are called by driver plugins to publish their
// implementation onto the kernel. Exported from internal and re-exported by the
// root package so external plugins never import internal.
func RegisterEmbedder(k *togo.Kernel, e Embedder) { k.Set(keyEmbedder, e) }
func RegisterReranker(k *togo.Kernel, r Reranker) { k.Set(keyReranker, r) }
func RegisterEngine(k *togo.Kernel, e Engine)     { k.Set(keyEngine, e) }
