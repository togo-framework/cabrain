// Command brain-mcp is CaBrain's Model Context Protocol server (SPEC §5.1): it
// exposes the six memory tools — memory_retain, memory_recall,
// memory_recall_archive, memory_get, memory_forget, memory_share — over stdio
// JSON-RPC so Claude Code and other agents can use the memory organ.
//
// It is a thin adapter over the brain's REST surface (the same endpoints the
// console uses), so all scoping/validation stays server-side in one place. The
// agent identity travels as the X-Agent-Id header (F5), taken from CABRAIN_AGENT_ID
// — never from tool arguments.
//
//	CABRAIN_API_URL   base URL of the running cabrain app (default http://localhost:8080)
//	CABRAIN_AGENT_ID  this MCP session's agent identity (empty = trusted/no grant checks)
//
// Wire it into .mcp.json:
//
//	{"mcpServers":{"cabrain":{"command":"brain-mcp"}}}
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const protocolVersion = "2024-11-05"

func main() {
	base := env("CABRAIN_API_URL", "http://localhost:8080")
	srv := &server{
		base:      strings.TrimRight(base, "/"),
		agent:     os.Getenv("CABRAIN_AGENT_ID"),
		token:     os.Getenv("CABRAIN_TOKEN"),             // ACL token → per-brain read/write
		defaultNS: os.Getenv("CABRAIN_DEFAULT_NAMESPACE"), // session bound to a brain
		hc:        &http.Client{Timeout: 30 * time.Second},
	}
	srv.serve(os.Stdin, os.Stdout)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// --- JSON-RPC 2.0 over newline-delimited stdio --------------------------------

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type server struct {
	base      string
	agent     string
	token     string
	defaultNS string
	hc        *http.Client
	out       *json.Encoder
}

func (s *server) serve(in io.Reader, out io.Writer) {
	s.out = json.NewEncoder(out)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		s.dispatch(&req)
	}
}

func (s *server) reply(id json.RawMessage, result any) {
	_ = s.out.Encode(rpcResp{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *server) fail(id json.RawMessage, code int, msg string) {
	_ = s.out.Encode(rpcResp{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *server) dispatch(req *rpcReq) {
	switch req.Method {
	case "initialize":
		s.reply(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cabrain-brain", "version": "0.1.0"},
		})
	case "notifications/initialized", "notifications/cancelled":
		// notifications: no response
	case "ping":
		s.reply(req.ID, map[string]any{})
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": toolDefs})
	case "tools/call":
		s.callTool(req)
	default:
		if len(req.ID) > 0 {
			s.fail(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

// --- tool dispatch ------------------------------------------------------------

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *server) callTool(req *rpcReq) {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.fail(req.ID, -32602, "invalid params")
		return
	}
	args := map[string]any{}
	if len(p.Arguments) > 0 {
		_ = json.Unmarshal(p.Arguments, &args)
	}
	// Session bound to a brain: default the namespace when the caller omits it.
	if s.defaultNS != "" {
		if v, ok := args["namespace"]; !ok || v == nil || v == "" {
			args["namespace"] = s.defaultNS
		}
	}

	var (
		body any
		code int
		err  error
	)
	switch p.Name {
	case "memory_retain":
		body, code, err = s.post("/api/brain/retain", map[string]any{
			"namespace":      args["namespace"],
			"content":        args["content"],
			"sourceKind":     args["source_kind"],
			"sourceRef":      args["source_ref"],
			"visibility":     args["visibility"],
			"importanceHint": args["importance_hint"],
		})
	case "memory_recall":
		body, code, err = s.post("/api/brain/recall", map[string]any{
			"namespace":     args["namespace"],
			"query":         args["query"],
			"limit":         args["limit"],
			"expandEntity":  args["expand_entities"],
			"minImportance": args["min_importance"],
		})
	case "memory_recall_archive":
		// Phase 2: cold-tier deep recall is stubbed until cold demotion exists.
		s.toolResult(req.ID, map[string]any{
			"error": map[string]string{"code": "unavailable",
				"message": "memory_recall_archive: cold tier not yet provisioned (Phase 2)"}}, false)
		return
	case "memory_get":
		body, code, err = s.get("/api/brain/memory", url.Values{
			"namespace": {str(args["namespace"])}, "id": {str(args["id"])}})
	case "memory_forget":
		body, code, err = s.post("/api/brain/forget", map[string]any{
			"namespace": args["namespace"], "id": args["id"], "reason": args["reason"]})
	case "memory_share":
		body, code, err = s.post("/api/brain/share", map[string]any{
			"namespace":      args["namespace"],
			"granteeAgentId": args["grantee_agent_id"],
			"canRead":        args["can_read"],
			"canWrite":       args["can_write"],
		})
	case "memory_gaps":
		qv := url.Values{}
		if v := str(args["namespace"]); v != "" {
			qv.Set("namespace", v)
		}
		if v := str(args["status"]); v != "" {
			qv.Set("status", v)
		}
		if v := str(args["limit"]); v != "" {
			qv.Set("limit", v)
		}
		body, code, err = s.get("/api/brain/gaps", qv)
	case "memory_resolve_gap":
		body, code, err = s.post("/api/brain/gaps/resolve", map[string]any{
			"id": args["id"], "status": args["status"], "resolution": args["resolution"]})
	case "brain_list":
		body, code, err = s.get("/api/brain/namespaces", nil)
	case "brain_details":
		body, code, err = s.get("/api/brain/brain", url.Values{"namespace": {str(args["namespace"])}})
	case "memory_edit":
		body, code, err = s.post("/api/brain/memory/edit", map[string]any{
			"namespace": args["namespace"], "id": args["id"], "content": args["content"],
			"importance": args["importance"], "metadata": args["metadata"]})
	case "brain_delete":
		body, code, err = s.post("/api/brain/brain/delete", map[string]any{
			"namespace": args["namespace"], "confirm": args["confirm"]})
	case "brain_grant":
		body, code, err = s.post("/api/brain/grant", map[string]any{
			"agentId": args["agentId"], "namespace": args["namespace"],
			"canRead": args["canRead"], "canWrite": args["canWrite"]})
	case "brain_revoke_grant":
		body, code, err = s.post("/api/brain/grant/revoke", map[string]any{
			"agentId": args["agentId"], "namespace": args["namespace"]})
	case "brain_create_token":
		body, code, err = s.post("/api/brain/tokens", map[string]any{
			"agentId": args["agentId"], "label": args["label"], "isAdmin": args["isAdmin"]})
	case "brain_tokens":
		qv := url.Values{}
		if b, _ := args["includeRevoked"].(bool); b {
			qv.Set("includeRevoked", "1")
		}
		body, code, err = s.get("/api/brain/tokens", qv)
	case "secret_list":
		body, code, err = s.get("/api/brain/secrets", url.Values{"namespace": {str(args["namespace"])}})
	case "secret_store":
		body, code, err = s.post("/api/brain/secrets", map[string]any{
			"namespace": args["namespace"], "name": args["name"], "value": args["value"], "kind": args["kind"]})
	case "secret_reveal":
		body, code, err = s.post("/api/brain/secrets/reveal", map[string]any{
			"namespace": args["namespace"], "name": args["name"]})
	case "secret_delete":
		body, code, err = s.post("/api/brain/secrets/delete", map[string]any{
			"namespace": args["namespace"], "name": args["name"]})
	default:
		s.fail(req.ID, -32602, "unknown tool: "+p.Name)
		return
	}

	if err != nil {
		s.toolResult(req.ID, map[string]any{"error": map[string]string{
			"code": "unavailable", "message": err.Error()}}, true)
		return
	}
	// A non-2xx REST response carries a structured error body already; surface it
	// as an error tool result so the agent sees permission_denied / not_found / etc.
	s.toolResult(req.ID, body, code >= 400)
}

// toolResult wraps a JSON payload as an MCP tool result (text content).
func (s *server) toolResult(id json.RawMessage, payload any, isErr bool) {
	b, _ := json.MarshalIndent(payload, "", "  ")
	s.reply(id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": isErr,
	})
}

// --- REST client --------------------------------------------------------------

func (s *server) post(path string, payload map[string]any) (any, int, error) {
	// Drop nil keys so absent optional args don't override server defaults.
	clean := map[string]any{}
	for k, v := range payload {
		if v != nil {
			clean[k] = v
		}
	}
	b, _ := json.Marshal(clean)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.base+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req)
}

func (s *server) get(path string, q url.Values) (any, int, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	return s.do(req)
}

func (s *server) do(req *http.Request) (any, int, error) {
	if s.agent != "" {
		req.Header.Set("X-Agent-Id", s.agent)
	}
	if s.token != "" {
		req.Header.Set("X-Cabrain-Token", s.token) // ACL: per-brain read/write
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body any
	if json.Unmarshal(raw, &body) != nil {
		body = map[string]any{"raw": string(raw)}
	}
	return body, resp.StatusCode, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
