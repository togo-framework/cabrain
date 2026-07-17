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
	"regexp"
	"strings"
	"sync"
	"time"
)

// Client talks to a Cognee instance. It is safe for concurrent use. Cognee gates
// its API behind fastapi-users: authenticate with POST /auth/login (form
// username+password) to get a JWT, then send it as a Bearer token. The JWT is
// cached and refreshed on a 401.
type Client struct {
	base     string
	email    string
	password string
	hc       *http.Client
	warn     func(msg string, args ...any)

	mu  sync.Mutex
	jwt string
}

// Option configures the client.
type Option func(*Client)

// WithWarnFunc wires a logger for best-effort warnings.
func WithWarnFunc(f func(msg string, args ...any)) Option {
	return func(c *Client) { c.warn = f }
}

// New builds a Cognee client. base is the API root (…:8000); email/password are the
// admin login (COGNEE_ADMIN_EMAIL / COGNEE_API_TOKEN).
func New(base, email, password string, opts ...Option) *Client {
	c := &Client{
		base:     strings.TrimRight(base, "/"),
		email:    email,
		password: password,
		hc:       &http.Client{Timeout: 60 * time.Second},
		warn:     func(string, ...any) {},
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

// Dataset is one entry from GET /api/v1/datasets.
type Dataset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Datasets lists the caller's Cognee datasets.
func (c *Client) Datasets(ctx context.Context) ([]Dataset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/datasets", nil)
	if err != nil {
		return nil, err
	}
	b, err := c.recv(req)
	if err != nil {
		return nil, err
	}
	var out []Dataset
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("cognee: parse datasets: %w", err)
	}
	return out, nil
}

// DatasetIDByName resolves a dataset name (== CaBrain namespace) to its id.
func (c *Client) DatasetIDByName(ctx context.Context, name string) (string, error) {
	ds, err := c.Datasets(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Name == name {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("cognee: no dataset named %q", name)
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

// token returns a cached JWT, logging in on first use.
func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	j := c.jwt
	c.mu.Unlock()
	if j != "" {
		return j, nil
	}
	j, err := c.login(ctx)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.jwt = j
	c.mu.Unlock()
	return j, nil
}

// login exchanges the admin email/password for a JWT (fastapi-users login form).
func (c *Client) login(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("username", c.email)
	_ = mw.WriteField("password", c.password)
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/auth/login", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("cognee login: HTTP %d: %s", resp.StatusCode, truncate(string(b), 160))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("cognee login: no access_token in response")
	}
	return out.AccessToken, nil
}

// send fires a request and discards the body, erroring on non-2xx.
func (c *Client) send(req *http.Request) error {
	_, err := c.recv(req)
	return err
}

// recv fires a request with the JWT and returns the body; on a 401 it drops the
// cached token, re-logs-in, and retries once (the request body is replayed via
// GetBody, which http.NewRequest sets for bytes/strings bodies).
func (c *Client) recv(req *http.Request) ([]byte, error) {
	b, code, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if code == http.StatusUnauthorized {
		c.mu.Lock()
		c.jwt = ""
		c.mu.Unlock()
		if req.GetBody != nil {
			if body, e := req.GetBody(); e == nil {
				req.Body = body
			}
		}
		b, code, err = c.do(req)
		if err != nil {
			return nil, err
		}
	}
	if code >= 300 {
		return b, fmt.Errorf("cognee %s %s: HTTP %d: %s",
			req.Method, req.URL.Path, code, truncate(string(b), 200))
	}
	return b, nil
}

// do sets a fresh Bearer token and executes the request.
func (c *Client) do(req *http.Request) ([]byte, int, error) {
	tok, err := c.token(req.Context())
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
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

// memDocRe recovers a CaBrain memory UUID from a Cognee document node: brain-cognee
// uploads each memory as a file named "mem-<uuid>.txt", so the id round-trips
// through Cognee's document node label/properties.
var memDocRe = regexp.MustCompile(`mem-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)

// MemLink pairs a CaBrain memory UUID with an entity label reachable from that
// memory's document node in the graph.
type MemLink struct {
	MemoryID   string
	EntityName string
}

// nodeMemoryID extracts a CaBrain memory UUID from a node's label or string-valued
// properties (the "mem-<uuid>.txt" naming), or "" if none.
func nodeMemoryID(n GraphNode) string {
	if m := memDocRe.FindStringSubmatch(n.Label); m != nil {
		return m[1]
	}
	for _, v := range n.Properties {
		if s, ok := v.(string); ok {
			if m := memDocRe.FindStringSubmatch(s); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// MemoryEntityLinks derives (memory_id, entity_name) pairs by finding document
// nodes that carry a CaBrain memory id and collecting the entity nodes reachable
// from them within maxHops (edges treated as undirected: doc → chunk → entity).
// Best-effort: it depends on Cognee's document-node naming surviving cognify;
// verify against a live graph once Cognee ingestion is fixed. Deterministic order.
func (g *GraphDTO) MemoryEntityLinks() []MemLink {
	const maxHops = 2
	byID := make(map[string]GraphNode, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	adj := make(map[string][]string, len(g.Nodes))
	for _, e := range g.Edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
		adj[e.Target] = append(adj[e.Target], e.Source)
	}
	typed := false
	for _, n := range g.Nodes {
		if n.Type != "" {
			typed = true
			break
		}
	}
	isEntity := func(n GraphNode) bool { return n.Label != "" && (!typed || isEntityType(n.Type)) }

	seen := map[string]bool{}
	out := []MemLink{}
	for _, start := range g.Nodes {
		mem := nodeMemoryID(start)
		if mem == "" {
			continue
		}
		// BFS up to maxHops from the memory's document node.
		visited := map[string]bool{start.ID: true}
		frontier := []string{start.ID}
		for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
			var next []string
			for _, id := range frontier {
				for _, nb := range adj[id] {
					if visited[nb] {
						continue
					}
					visited[nb] = true
					next = append(next, nb)
					if n, ok := byID[nb]; ok && isEntity(n) {
						key := mem + "\x00" + n.Label
						if !seen[key] {
							seen[key] = true
							out = append(out, MemLink{MemoryID: mem, EntityName: n.Label})
						}
					}
				}
			}
			frontier = next
		}
	}
	return out
}
