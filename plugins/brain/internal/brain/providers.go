package brain

import "context"

// Provider seams. The self-hosted embedding/rerank plane and the cognify engine
// are separate driver plugins (brain-tei, brain-cognee) that register their
// implementations on the brain Service — mirroring togo's driver-registry
// plugins. When a provider is absent, the dependent operation returns a clear
// "install <plugin>" error rather than silently degrading.

// Embedder turns text into dense vectors (TEI → Qwen3-Embedding-0.6B, 1024-dim).
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
// Optional — recall/retain work without it; it enriches the entity graph used
// for 1-hop spreading activation.
type Engine interface {
	// Cognify extracts entities/relations for a stored memory and populates the
	// entities / memory_entities graph for the namespace.
	Cognify(ctx context.Context, namespace, memoryID, content string) error
}

// providers holds the optionally-registered driver implementations.
type providers struct {
	embedder Embedder
	reranker Reranker
	engine   Engine
}
