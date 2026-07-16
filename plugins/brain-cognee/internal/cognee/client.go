// Package cognee is CaBrain's client for the Cognee cognify engine (SPEC §7): it
// feeds retained memories into Cognee's graph pipeline so entities/relations are
// extracted off the hot path. It targets the Cognee 1.3.0 REST API discovered at
// runtime:
//
//	POST /api/v1/add       (multipart) — add text to a dataset (dataset = namespace)
//	POST /api/v1/cognify   (json)      — build the knowledge graph for datasets
//	POST /api/v1/search    (json)      — graph/vector search (used by MirrorGraph/optional recall)
//	GET  /api/v1/datasets/{id}/graph   — read the built graph (entity mirroring, Phase 2)
//
// Auth is configurable: Cognee gates these behind a bearer token / api-key. The
// token + header scheme come from env so the operator wires the correct value
// on-stack (the workspace probe returned 401, mirroring the TEI deploy boundary).
package cognee

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a Cognee instance. It is safe for concurrent use.
type Client struct {
	base       string
	token      string
	authHeader string // e.g. "Authorization"; value is authPrefix+token
	authPrefix string // e.g. "Bearer "
	hc         *http.Client
	warn       func(msg string, args ...any)
}

// Option configures the client.
type Option func(*Client)

// WithAuthHeader overrides the auth header name + value prefix (default
// "Authorization" / "Bearer "). Some Cognee deployments use "X-Api-Key" / "".
func WithAuthHeader(name, prefix string) Option {
	return func(c *Client) { c.authHeader, c.authPrefix = name, prefix }
}

// WithWarnFunc wires a logger for best-effort warnings.
func WithWarnFunc(f func(msg string, args ...any)) Option {
	return func(c *Client) { c.warn = f }
}

// New builds a Cognee client. base is the API root (…:8000), token the bearer/api key.
func New(base, token string, opts ...Option) *Client {
	c := &Client{
		base:       strings.TrimRight(base, "/"),
		token:      token,
		authHeader: "Authorization",
		authPrefix: "Bearer ",
		hc:         &http.Client{Timeout: 30 * time.Second},
		warn:       func(string, ...any) {},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Cognify implements brain.Engine: it adds the memory's content to the
// namespace's Cognee dataset and triggers a background graph build. Fire-and-
// forget by design — the brain calls this in a goroutine and ignores the error;
// Postgres already holds the authoritative memory. run_in_background lets Cognee
// process asynchronously so this returns fast.
func (c *Client) Cognify(ctx context.Context, namespace, memoryID, content string) error {
	if content == "" {
		return nil
	}
	if err := c.add(ctx, namespace, memoryID, content); err != nil {
		c.warn("cognee.add failed", "namespace", namespace, "err", err)
		return err
	}
	if err := c.cognify(ctx, namespace); err != nil {
		c.warn("cognee.cognify failed", "namespace", namespace, "err", err)
		return err
	}
	return nil
}

// add uploads the content as a text datum to the dataset (= namespace).
func (c *Client) add(ctx context.Context, namespace, memoryID, content string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// data is an array of files; send the memory as one text part named by its id
	// so Cognee's provenance can trace back to the CaBrain memory.
	fw, err := mw.CreateFormFile("data", "mem-"+memoryID+".txt")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(fw, content); err != nil {
		return err
	}
	_ = mw.WriteField("datasetName", namespace)
	_ = mw.WriteField("run_in_background", "true")
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/add", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.send(req)
}

// cognify triggers the (background) graph build for the dataset.
func (c *Client) cognify(ctx context.Context, namespace string) error {
	body := fmt.Sprintf(`{"datasets":[%q],"runInBackground":true}`, namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/cognify",
		strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.send(req)
}

// Search runs a Cognee graph/vector search over a namespace's dataset. Returns the
// raw JSON body. Optional helper for a future graph-aware recall fusion (SPEC §7).
func (c *Client) Search(ctx context.Context, namespace, query, searchType string, topK int) ([]byte, error) {
	if searchType == "" {
		searchType = "GRAPH_COMPLETION"
	}
	if topK <= 0 {
		topK = 10
	}
	body := fmt.Sprintf(`{"searchType":%q,"datasets":[%q],"query":%q,"topK":%d}`,
		searchType, namespace, query, topK)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/search",
		strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.recv(req)
}

// Ping checks reachability (unauthenticated root). Used by ops/health.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/", nil)
	if err != nil {
		return err
	}
	_, err = c.recv(req)
	return err
}

// --- transport ----------------------------------------------------------------

func (c *Client) auth(req *http.Request) {
	if c.token != "" {
		req.Header.Set(c.authHeader, c.authPrefix+c.token)
	}
}

// send fires a request and discards the body, erroring on non-2xx.
func (c *Client) send(req *http.Request) error {
	_, err := c.recv(req)
	return err
}

// recv fires a request and returns the body, erroring on non-2xx.
func (c *Client) recv(req *http.Request) ([]byte, error) {
	c.auth(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return b, fmt.Errorf("cognee %s %s: HTTP %d: %s",
			req.Method, req.URL.Path, resp.StatusCode, truncate(string(b), 200))
	}
	return b, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// DatasetGraphURL is the endpoint for reading a built graph. Exposed so the mirror
// step can be wired without re-deriving paths.
func (c *Client) DatasetGraphURL(datasetID string) string {
	return c.base + "/api/v1/datasets/" + url.PathEscape(datasetID) + "/graph"
}

// --- graph mirroring (SPEC §7) ------------------------------------------------
//
// GraphDTO mirrors Cognee's GET /api/v1/datasets/{id}/graph response (from its
// public OpenAPI): nodes carry an id/label/type/properties, edges a source/target/
// label. CaBrain mirrors the entity nodes into its own `entities` table so the
// Graph Explorer + 1-hop expansion read from Postgres (not a live Cognee call on
// the hot path).

type GraphDTO struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type GraphNode struct {
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
}

type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Label  string `json:"label"`
}

// FetchGraph reads and parses the built knowledge graph for a dataset.
func (c *Client) FetchGraph(ctx context.Context, datasetID string) (*GraphDTO, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DatasetGraphURL(datasetID), nil)
	if err != nil {
		return nil, err
	}
	b, err := c.recv(req)
	if err != nil {
		return nil, err
	}
	return ParseGraph(b)
}

// ParseGraph decodes a GraphDTO body. Split out so it is unit-testable without a
// live (authenticated) Cognee.
func ParseGraph(b []byte) (*GraphDTO, error) {
	var g GraphDTO
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("cognee: parse graph: %w", err)
	}
	return &g, nil
}

// EntityNames returns the distinct, non-empty entity labels from the graph — the
// set CaBrain upserts into its `entities` table for a namespace. Cognee tags
// structural nodes (documents, chunks) with a Type; when types are present we keep
// only entity-like nodes, otherwise (older graphs) we fall back to all labeled
// nodes. Deterministic order for stable upserts/tests.
func (g *GraphDTO) EntityNames() []string {
	typed := false
	for _, n := range g.Nodes {
		if n.Type != "" {
			typed = true
			break
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, n := range g.Nodes {
		if n.Label == "" {
			continue
		}
		if typed && !isEntityType(n.Type) {
			continue
		}
		if seen[n.Label] {
			continue
		}
		seen[n.Label] = true
		out = append(out, n.Label)
	}
	return out
}

// isEntityType keeps semantic entity nodes, dropping Cognee's structural node types
// (documents, chunks, the raw text carriers).
func isEntityType(t string) bool {
	switch strings.ToLower(t) {
	case "documentchunk", "textdocument", "document", "chunk", "textsummary":
		return false
	default:
		return true
	}
}
