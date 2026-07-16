// Package braintei is the TEI provider plugin for CaBrain: it publishes an
// Embedder (BAAI/bge-m3) and Reranker (bge-reranker-v2-m3) onto the kernel for
// the brain plugin to use. Config from env (TEI_EMBEDDINGS_URL / _RERANKER_URL /
// _DIM). Self-registers on blank-import.
package braintei

import (
	"os"
	"strconv"

	"github.com/togo-framework/togo"

	"github.com/togo-framework/brain"
	"github.com/togo-framework/brain-tei/internal/tei"
)

const Name = "brain-tei"

func init() {
	togo.RegisterProviderFunc(Name, togo.PriorityLate, func(k *togo.Kernel) error {
		embedURL := os.Getenv("TEI_EMBEDDINGS_URL")
		if embedURL == "" {
			if k.Log != nil {
				k.Log.Warn("brain-tei: TEI_EMBEDDINGS_URL unset — embeddings disabled")
			}
			return nil
		}
		dim := 1024
		if d, err := strconv.Atoi(os.Getenv("TEI_EMBEDDINGS_DIM")); err == nil && d > 0 {
			dim = d
		}
		rerankURL := os.Getenv("TEI_RERANKER_URL")

		c := tei.New(embedURL, rerankURL, dim)
		brain.RegisterEmbedder(k, c)
		if rerankURL != "" {
			brain.RegisterReranker(k, c)
		}
		if k.Log != nil {
			k.Log.Info("plugin active", "plugin", Name, "embed", embedURL, "rerank", rerankURL, "dim", dim)
		}
		return nil
	})
}
