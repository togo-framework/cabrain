package brain

// The agentic loop behind Store.Chat. Instead of a single recall→stuff→answer shot,
// the model is handed the brain's own organs as callable tools and drives a loop:
//
//	send system+history+user + tools → model returns tool_calls → we EXECUTE them
//	against this Store (recall / search / graph_neighbors / retain), append the
//	results as tool messages, and loop again — until the model returns a final,
//	grounded text answer (or we hit the iteration cap).
//
// Two provider surfaces are supported, both real tool-calling:
//   - OpenAI-compatible /v1/chat/completions (the stack's Ollama with a tool-capable
//     model). This is the default.
//   - Anthropic messages API with tools (preferred when a key is present).
//
// If the model/endpoint rejects tools we return errToolsUnsupported so Store.Chat can
// degrade to single-shot RAG. Citations are exactly the memories the tools surfaced;
// the footprint carries the trace of every tool call (Steps).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// errToolsUnsupported signals that the model/endpoint could not do tool-calling, so
// the caller should fall back to single-shot RAG.
var errToolsUnsupported = errors.New("brain.agent: model does not support tool-calling")

const (
	agentMaxIters   = 5
	agentToolBudget = 10 // memories a single tool call returns to the model
)

// --- shared agent state ------------------------------------------------------

// agentRun accumulates what the agent surfaced across a loop: the citation registry
// (deduped, stably numbered) and the trace of tool calls it made.
type agentRun struct {
	s       *Store
	q       ChatQuery
	cites   []Recalled
	citeIdx map[string]int // memory id → 1-based citation number
	steps   []AgentStep
}

func newAgentRun(s *Store, q ChatQuery) *agentRun {
	return &agentRun{s: s, q: q, citeIdx: map[string]int{}, steps: []AgentStep{}}
}

// register assigns stable citation numbers to freshly surfaced memories and returns
// them tagged with their number, so the model can cite [n] consistently across calls.
func (r *agentRun) register(mems []Recalled) []citedMem {
	out := make([]citedMem, 0, len(mems))
	for _, m := range mems {
		n, ok := r.citeIdx[m.ID]
		if !ok {
			n = len(r.cites) + 1
			r.citeIdx[m.ID] = n
			r.cites = append(r.cites, m)
		}
		out = append(out, citedMem{N: n, M: m})
	}
	return out
}

type citedMem struct {
	N int
	M Recalled
}

// toolResultJSON renders surfaced memories as a compact, numbered JSON payload the
// model reads back — each item carries its citation number so [n] stays consistent.
func toolResultJSON(items []citedMem) string {
	type row struct {
		N       int     `json:"n"`
		Type    string  `json:"type,omitempty"`
		Source  string  `json:"source,omitempty"`
		Score   float64 `json:"score,omitempty"`
		Content string  `json:"content"`
	}
	rows := make([]row, 0, len(items))
	for _, it := range items {
		src := it.M.SourceKind
		if it.M.SourceRef != "" {
			src += "/" + it.M.SourceRef
		}
		if it.M.Namespace != "" {
			src = it.M.Namespace + ":" + src
		}
		rows = append(rows, row{N: it.N, Type: it.M.MemoryType, Source: src, Score: it.M.Score, Content: oneLine(it.M.Content, 900)})
	}
	b, _ := json.Marshal(map[string]any{"count": len(rows), "memories": rows})
	return string(b)
}

// execTool runs one tool call against the Store, records a Step, and returns the text
// payload to feed back to the model. Errors are surfaced to the model as text (so it
// can adapt) rather than aborting the loop.
func (r *agentRun) execTool(ctx context.Context, name, argsJSON string) string {
	args := parseArgs(argsJSON)
	step := AgentStep{Tool: name, Args: strings.TrimSpace(argsJSON)}
	defer func() { r.steps = append(r.steps, step) }()

	switch name {
	case "recall":
		q := argStr(args, "query")
		if q == "" {
			q = r.q.Message
		}
		topK := argInt(args, "topK", r.q.TopK)
		if topK <= 0 || topK > 20 {
			topK = r.q.TopK
		}
		mems, err := r.s.Recall(ctx, RecallQuery{Namespace: r.q.Namespace, Query: q, Limit: topK, ExpandEntity: true})
		if err != nil {
			step.Note = err.Error()
			return `{"error":` + jsonString(err.Error()) + `}`
		}
		step.ResultCount = len(mems)
		return toolResultJSON(r.register(mems))

	case "search":
		q := argStr(args, "query")
		if q == "" {
			q = r.q.Message
		}
		// SECURITY: the HTTP handler only authorized read on r.q.Namespace, so the
		// search stays scoped to this brain regardless of any namespaces the model
		// asks for — never widen ACL from inside the model's reasoning.
		mems, err := r.s.SearchAll(ctx, SearchQuery{Query: q, Namespaces: []string{r.q.Namespace}, Limit: agentToolBudget})
		if err != nil {
			step.Note = err.Error()
			return `{"error":` + jsonString(err.Error()) + `}`
		}
		step.ResultCount = len(mems)
		return toolResultJSON(r.register(mems))

	case "graph_neighbors", "graph":
		ent := argStr(args, "entity")
		if ent == "" {
			ent = argStr(args, "query")
		}
		if ent == "" {
			ent = r.q.Message
		}
		mems, err := r.s.graphNeighbors(ctx, r.q.Namespace, ent, agentToolBudget)
		if err != nil {
			step.Note = err.Error()
			return `{"error":` + jsonString(err.Error()) + `}`
		}
		step.ResultCount = len(mems)
		return toolResultJSON(r.register(mems))

	case "retain":
		if !r.q.Write {
			step.Note = "read-only session"
			return `{"error":"retain is unavailable: this is a read-only session (no write grant on the brain)"}`
		}
		content := argStr(args, "content")
		if strings.TrimSpace(content) == "" {
			step.Note = "empty content"
			return `{"error":"retain requires non-empty content"}`
		}
		res, err := r.s.Retain(ctx, MemoryInput{
			Namespace:    r.q.Namespace,
			Content:      content,
			SourceKind:   "chat",
			OwnerAgentID: "chat-agent:" + r.q.Namespace,
		})
		if err != nil {
			step.Note = err.Error()
			return `{"error":` + jsonString(err.Error()) + `}`
		}
		step.ResultCount = 1
		step.Note = "decision=" + res.Decision
		b, _ := json.Marshal(map[string]any{"stored": true, "id": res.ID, "decision": res.Decision})
		return string(b)

	default:
		step.Note = "unknown tool"
		return `{"error":"unknown tool: ` + name + `"}`
	}
}

// graphNeighbors returns memories linked to an entity via the Cognee entity graph
// (1-hop), scoped to the namespace/hot tier. When the entity tables are absent or
// nothing matches, it falls back to a semantic recall on the entity name so the tool
// is always useful.
func (s *Store) graphNeighbors(ctx context.Context, ns, entity string, limit int) ([]Recalled, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ON (m.id)
		       m.id::text, m.content, m.network, m.memory_type, COALESCE(m.source_kind,''),
		       COALESCE(m.source_ref,''), m.importance, m.valid_at, e.name
		FROM entities e
		JOIN memory_entities me ON me.entity_id = e.id
		JOIN memories m ON m.id = me.memory_id
		WHERE e.name ILIKE '%' || $1 || '%'
		      AND ($2 = '' OR m.namespace = $2)
		      AND m.invalid_at IS NULL AND m.tier = 'hot'
		LIMIT $3`, entity, ns, limit)
	if err != nil {
		// entity graph not populated yet → semantic fallback
		return s.Recall(ctx, RecallQuery{Namespace: ns, Query: entity, Limit: limit, ExpandEntity: true})
	}
	defer rows.Close()
	out := []Recalled{}
	for rows.Next() {
		var r Recalled
		var via string
		if err := rows.Scan(&r.ID, &r.Content, &r.Network, &r.MemoryType, &r.SourceKind,
			&r.SourceRef, &r.Importance, &r.ValidAt, &via); err != nil {
			continue
		}
		r.ViaEntity = via
		out = append(out, r)
	}
	if len(out) == 0 {
		return s.Recall(ctx, RecallQuery{Namespace: ns, Query: entity, Limit: limit, ExpandEntity: true})
	}
	return out, nil
}

// agentSystemPrompt frames the model as the brain's live agent and tells it to work
// the tools before answering.
func agentSystemPrompt(ns string, write bool) string {
	var b strings.Builder
	b.WriteString("You are the live agent of the \"")
	b.WriteString(ns)
	b.WriteString("\" brain — an organ of memory. You do NOT know the answer yourself; the brain does. ")
	b.WriteString("To answer, you MUST use your tools to pull the relevant memories from the brain, then ground your answer strictly in what they return.\n\n")
	b.WriteString("Tools:\n")
	b.WriteString("- recall(query, topK): semantic recall of the most relevant memories for a query. USE THIS FIRST, always, before answering.\n")
	b.WriteString("- search(query): hybrid (vector+keyword) search over this brain when recall is thin or you need a broader sweep.\n")
	b.WriteString("- graph_neighbors(entity): memories connected to a named entity (venture, person, project) via the knowledge graph.\n")
	if write {
		b.WriteString("- retain(content): write a NEW durable memory back to the brain (only when you learn something worth keeping).\n")
	}
	b.WriteString("\nRules:\n")
	b.WriteString("1. Call recall (and search/graph_neighbors as needed) BEFORE writing any answer. Never answer from your own prior knowledge.\n")
	b.WriteString("2. Cite the memories you use inline as [n], matching the \"n\" field in the tool results.\n")
	b.WriteString("3. If the tools return nothing relevant, say plainly that this brain has no memory of it — do NOT invent facts.\n")
	b.WriteString("4. Be concise and specific. Stop calling tools once you have enough to answer.")
	return b.String()
}

// --- argument helpers (tolerant of Ollama/Anthropic quirks) ------------------

// parseArgs decodes a tool-call arguments blob. OpenAI/Ollama send arguments as a
// JSON *string*; some send a JSON object directly. Handle both.
func parseArgs(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		return m
	}
	// arguments delivered as a quoted JSON string → unquote then parse
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			return m
		}
	}
	return map[string]any{}
}

func argStr(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func argInt(m map[string]any, k string, def int) int {
	if v, ok := m[k]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// ============================================================================
// OpenAI-compatible tool-calling loop (default: the stack's Ollama)
// ============================================================================

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

type oaToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function oaFunctionCall `json:"function"`
}

type oaFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAITools builds the OpenAI function schemas for the brain's organs. retain is
// only offered when the session may write.
func openAITools(write bool) []map[string]any {
	tool := func(name, desc string, props map[string]any, required []string) map[string]any {
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": desc,
				"parameters": map[string]any{
					"type":       "object",
					"properties": props,
					"required":   required,
				},
			},
		}
	}
	tools := []map[string]any{
		tool("recall", "Semantically recall the most relevant memories from the brain for a query. Call this first, before answering.",
			map[string]any{
				"query": map[string]any{"type": "string", "description": "what to recall"},
				"topK":  map[string]any{"type": "integer", "description": "how many memories (1-20)"},
			}, []string{"query"}),
		tool("search", "Hybrid vector+keyword search over this brain. Use when recall is thin or you need a broader sweep.",
			map[string]any{
				"query": map[string]any{"type": "string", "description": "search text"},
			}, []string{"query"}),
		tool("graph_neighbors", "Return memories connected to a named entity (venture, person, project) via the knowledge graph.",
			map[string]any{
				"entity": map[string]any{"type": "string", "description": "the entity name"},
			}, []string{"entity"}),
	}
	if write {
		tools = append(tools, tool("retain", "Write a NEW durable memory back into the brain. Use only when you learned something worth keeping.",
			map[string]any{
				"content": map[string]any{"type": "string", "description": "the memory to store"},
			}, []string{"content"}))
	}
	return tools
}

// runAgent drives the OpenAI-compatible tool-calling loop.
func (s *Store) runAgent(ctx context.Context, q ChatQuery, url, model, key string, start time.Time) (*ChatAnswer, error) {
	run := newAgentRun(s, q)
	tools := openAITools(q.Write)

	msgs := []oaMessage{{Role: "system", Content: agentSystemPrompt(q.Namespace, q.Write)}}
	for _, h := range q.History {
		role := h.Role
		if role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, oaMessage{Role: role, Content: h.Content})
	}
	msgs = append(msgs, oaMessage{Role: "user", Content: q.Message})

	iterations := 0
	answer := ""
	for i := 0; i < agentMaxIters; i++ {
		iterations++
		asst, err := oaChatWithTools(ctx, url, key, model, msgs, tools)
		if err != nil {
			// Only iteration 0 can prove tools are unsupported; after we've already
			// looped, a tool-schema error would be a genuine failure.
			if errors.Is(err, errToolsUnsupported) {
				return nil, err
			}
			return nil, err
		}
		msgs = append(msgs, asst) // record the assistant turn (with any tool_calls)
		if len(asst.ToolCalls) == 0 {
			answer = strings.TrimSpace(asst.Content)
			break
		}
		for _, tc := range asst.ToolCalls {
			result := run.execTool(ctx, tc.Function.Name, tc.Function.Arguments)
			msgs = append(msgs, oaMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: result})
		}
	}

	// Hit the cap while still calling tools → force a final answer with no tools.
	if answer == "" {
		final, err := oaChatWithTools(ctx, url, key, model, msgs, nil)
		if err != nil {
			return nil, err
		}
		answer = strings.TrimSpace(final.Content)
	}

	return run.answer(answer, model, "ollama", iterations, start), nil
}

// answer packages the loop result into the ChatAnswer + footprint.
func (r *agentRun) answer(text, model, provider string, iterations int, start time.Time) *ChatAnswer {
	if r.cites == nil {
		r.cites = []Recalled{}
	}
	return &ChatAnswer{
		Answer:    text,
		Citations: r.cites,
		Footprint: ChatFootprint{
			Namespace: r.q.Namespace, Query: r.q.Message, Recalled: len(r.cites),
			Model: model, Provider: provider, Mode: "agent", Grounded: len(r.cites) > 0,
			Iterations: iterations, Steps: r.steps, LatencyMs: int(time.Since(start).Milliseconds()),
		},
	}
}

// oaChatWithTools posts one turn to an OpenAI-compatible /v1/chat/completions and
// returns the assistant message. When `tools` is nil the request is a plain
// completion. A 400/422 that names tools/functions is mapped to errToolsUnsupported.
func oaChatWithTools(ctx context.Context, baseURL, key, model string, msgs []oaMessage, tools []map[string]any) (oaMessage, error) {
	payload := map[string]any{
		"model":       model,
		"messages":    msgs,
		"temperature": 0.2,
		"stream":      false,
	}
	if len(tools) > 0 {
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return oaMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	cl := &http.Client{Timeout: 120 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return oaMessage{}, err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message oaMessage `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return oaMessage{}, err
	}
	if resp.StatusCode >= 400 {
		msg := out.Error.Message
		if len(tools) > 0 && looksLikeToolsUnsupported(msg, resp.StatusCode) {
			return oaMessage{}, errToolsUnsupported
		}
		if msg != "" {
			return oaMessage{}, errors.New(msg)
		}
		return oaMessage{}, fmt.Errorf("llm http %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return oaMessage{}, errors.New("llm returned no choices")
	}
	return out.Choices[0].Message, nil
}

// looksLikeToolsUnsupported spots the Ollama/OpenAI "model does not support tools"
// class of error so we degrade to RAG instead of hard-failing.
func looksLikeToolsUnsupported(msg string, status int) bool {
	m := strings.ToLower(msg)
	if strings.Contains(m, "does not support tools") ||
		strings.Contains(m, "does not support function") ||
		strings.Contains(m, "tools are not supported") ||
		strings.Contains(m, "tool calls are not supported") ||
		(strings.Contains(m, "tool") && strings.Contains(m, "not support")) {
		return true
	}
	// Some backends 400/422 the unknown `tools` field outright.
	return (status == 400 || status == 422) && strings.Contains(m, "tools")
}

// ============================================================================
// Anthropic messages-API tool-calling loop (preferred when a key is present)
// ============================================================================

type antTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func anthropicTools(write bool) []antTool {
	schema := func(props map[string]any, req []string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	tools := []antTool{
		{Name: "recall", Description: "Semantically recall the most relevant memories from the brain for a query. Call this first, before answering.",
			InputSchema: schema(map[string]any{
				"query": map[string]any{"type": "string", "description": "what to recall"},
				"topK":  map[string]any{"type": "integer", "description": "how many memories (1-20)"},
			}, []string{"query"})},
		{Name: "search", Description: "Hybrid vector+keyword search over this brain. Use when recall is thin.",
			InputSchema: schema(map[string]any{
				"query": map[string]any{"type": "string", "description": "search text"},
			}, []string{"query"})},
		{Name: "graph_neighbors", Description: "Memories connected to a named entity via the knowledge graph.",
			InputSchema: schema(map[string]any{
				"entity": map[string]any{"type": "string", "description": "the entity name"},
			}, []string{"entity"})},
	}
	if write {
		tools = append(tools, antTool{Name: "retain", Description: "Write a NEW durable memory back into the brain.",
			InputSchema: schema(map[string]any{
				"content": map[string]any{"type": "string", "description": "the memory to store"},
			}, []string{"content"})})
	}
	return tools
}

// antContent is one content block (text | tool_use | tool_result) in a message.
type antContent struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use (assistant)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result (user)
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type antMessage struct {
	Role    string       `json:"role"`
	Content []antContent `json:"content"`
}

// runAnthropicAgent drives the same tool loop over Claude's messages API. Errors here
// are non-fatal to Store.Chat — it falls through to the Ollama agent.
func (s *Store) runAnthropicAgent(ctx context.Context, q ChatQuery, key, model string, start time.Time) (*ChatAnswer, error) {
	run := newAgentRun(s, q)
	tools := anthropicTools(q.Write)
	sys := agentSystemPrompt(q.Namespace, q.Write)

	msgs := []antMessage{}
	for _, h := range q.History {
		role := h.Role
		if role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, antMessage{Role: role, Content: []antContent{{Type: "text", Text: h.Content}}})
	}
	msgs = append(msgs, antMessage{Role: "user", Content: []antContent{{Type: "text", Text: q.Message}}})

	iterations := 0
	answer := ""
	for i := 0; i < agentMaxIters; i++ {
		iterations++
		reply, stop, err := anthropicTurn(ctx, key, model, sys, msgs, tools)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, antMessage{Role: "assistant", Content: reply})

		var results []antContent
		for _, c := range reply {
			switch c.Type {
			case "text":
				if s := strings.TrimSpace(c.Text); s != "" {
					answer = s
				}
			case "tool_use":
				res := run.execTool(ctx, c.Name, string(c.Input))
				results = append(results, antContent{Type: "tool_result", ToolUseID: c.ID, Content: res})
			}
		}
		if stop != "tool_use" || len(results) == 0 {
			break
		}
		answer = "" // more tools requested; the real answer comes after
		msgs = append(msgs, antMessage{Role: "user", Content: results})
	}

	return run.answer(strings.TrimSpace(answer), model, "anthropic", iterations, start), nil
}

// anthropicTurn posts one turn to /v1/messages and returns the reply content blocks
// plus the stop_reason.
func anthropicTurn(ctx context.Context, key, model, sys string, msgs []antMessage, tools []antTool) ([]antContent, string, error) {
	payload := map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"system":     sys,
		"messages":   msgs,
		"tools":      tools,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	cl := &http.Client{Timeout: 120 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	var out struct {
		Content    []antContent `json:"content"`
		StopReason string       `json:"stop_reason"`
		Error      struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 400 {
		if out.Error.Message != "" {
			return nil, "", errors.New(out.Error.Message)
		}
		return nil, "", fmt.Errorf("anthropic http %d", resp.StatusCode)
	}
	return out.Content, out.StopReason, nil
}
