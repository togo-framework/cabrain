// Package tei is a client for HuggingFace Text Embeddings Inference (TEI):
// embeddings (BAAI/bge-m3, 1024-dim, multilingual) and reranking
// (bge-reranker-v2-m3). It satisfies brain's Embedder + Reranker interfaces.
package tei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	embedURL  string
	rerankURL string
	dim       int
	hc        *http.Client
}

func New(embedURL, rerankURL string, dim int) *Client {
	return &Client{
		embedURL:  strings.TrimRight(embedURL, "/"),
		rerankURL: strings.TrimRight(rerankURL, "/"),
		dim:       dim,
		hc:        &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Dim() int { return c.dim }

// Embed returns one 1024-dim vector per input (TEI POST /embed).
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{"inputs": texts})
	var out [][]float32
	if err := c.post(ctx, c.embedURL+"/embed", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type rerankHit struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

// Rerank returns a relevance score per doc, aligned to the input order
// (TEI POST /rerank returns {index,score} sorted by score; we re-align).
func (c *Client) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	if c.rerankURL == "" || len(docs) == 0 {
		return nil, fmt.Errorf("tei: reranker not configured")
	}
	body, _ := json.Marshal(map[string]any{"query": query, "texts": docs})
	var hits []rerankHit
	if err := c.post(ctx, c.rerankURL+"/rerank", body, &hits); err != nil {
		return nil, err
	}
	scores := make([]float64, len(docs))
	for _, h := range hits {
		if h.Index >= 0 && h.Index < len(scores) {
			scores[h.Index] = h.Score
		}
	}
	return scores, nil
}

func (c *Client) post(ctx context.Context, url string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("tei: %s → HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
