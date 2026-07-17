package brain

// Live agent: chat with a selected brain. This is NOT a one-shot RAG search chat —
// it is a real **tool-calling agent** that USES the brain's own capabilities in a
// loop. The model is given the brain's organs as tools (recall / search /
// graph_neighbors / retain) and decides which to call; the loop executes them
// against this Store, feeds the results back, and repeats until the model returns a
// grounded final answer. Answers cite the memories the tools actually surfaced, and
// the footprint records the full trace of tool calls (Steps) — the Shape-of-AI
// "Governors/Trust" patterns that make the agent auditable.
//
// The agentic loop (agent.go) drives an OpenAI-compatible tool-calling endpoint (the
// stack's Ollama with a tool-capable model — qwen2.5/llama3.1/silma) via
// /v1/chat/completions. If an Anthropic key is configured it prefers Claude's
// messages API with tools. If tool-calling is disabled (BRAIN_CHAT_LLM_TOOLS=0) or
// the model rejects tools, it degrades gracefully to the classic single-shot RAG
// path (chatFallback) so chat never hard-breaks.
//
// Config, with fallbacks so it works out of the box:
//   BRAIN_CHAT_LLM_URL   (fallback EXTRACTION_LLM_URL)    e.g. http://host.docker.internal:11434
//   BRAIN_CHAT_LLM_MODEL (fallback EXTRACTION_LLM_MODEL)  e.g. silma:9b-instruct
//   BRAIN_CHAT_LLM_KEY   (fallback EXTRACTION_LLM_API_KEY)
//   BRAIN_CHAT_LLM_TOOLS=0 → force single-shot RAG (no agent loop)
//   ANTHROPIC_API_KEY / BRAIN_CHAT_ANTHROPIC_KEY (+ BRAIN_CHAT_ANTHROPIC_MODEL) → prefer Claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// ChatTurn is one message in the conversation history.
type ChatTurn struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// ChatQuery is a chat request against one brain.
type ChatQuery struct {
	Namespace string     `json:"namespace"`
	Message   string     `json:"message"`
	History   []ChatTurn `json:"history"`
	TopK      int        `json:"topK"` // memories to ground on (default 8)
	// Write gates the write-back `retain` tool. Defaults false; the HTTP handler
	// downgrades it to the caller's actual write permission on the brain so a body
	// flag can never escalate a read-only session into a writer.
	Write bool `json:"write"`
}

// ChatAnswer is the grounded answer plus its provenance.
type ChatAnswer struct {
	Answer    string        `json:"answer"`
	Citations []Recalled    `json:"citations"` // the memories the answer is grounded in
	Footprint ChatFootprint `json:"footprint"`
}

// AgentStep is one tool invocation the agent made — the auditable trace of how it
// worked the brain to answer.
type AgentStep struct {
	Tool        string `json:"tool"`           // recall | search | graph_neighbors | retain
	Args        string `json:"args"`           // the arguments the model passed (JSON)
	ResultCount int    `json:"resultCount"`    // memories/rows the tool returned
	Note        string `json:"note,omitempty"` // e.g. decision on retain, or an error
}

// ChatFootprint is the auditable trace: what was searched, how much was found, by which model.
type ChatFootprint struct {
	Namespace  string      `json:"namespace"`
	Query      string      `json:"query"`
	Recalled   int         `json:"recalled"`
	Model      string      `json:"model"`
	Provider   string      `json:"provider"`   // ollama | anthropic
	Mode       string      `json:"mode"`       // agent | rag (single-shot fallback)
	Grounded   bool        `json:"grounded"`   // false ⇒ nothing relevant surfaced (caveat shown)
	Iterations int         `json:"iterations"` // agent-loop turns taken
	Steps      []AgentStep `json:"steps"`      // the trace of tool calls the agent made
	LatencyMs  int         `json:"latencyMs"`
}

func chatLLM() (url, model, key string, ok bool) {
	url = firstEnv("BRAIN_CHAT_LLM_URL", "EXTRACTION_LLM_URL")
	model = firstEnv("BRAIN_CHAT_LLM_MODEL", "EXTRACTION_LLM_MODEL")
	key = firstEnv("BRAIN_CHAT_LLM_KEY", "EXTRACTION_LLM_API_KEY")
	if model == "" {
		model = "llama3.1"
	}
	return url, model, key, url != ""
}

// chatAnthropic returns the Claude messages-API config when a key is present. Ollama
// stays the default/fallback so the agent works with the current stack out of the box.
func chatAnthropic() (key, model string, ok bool) {
	key = firstEnv("BRAIN_CHAT_ANTHROPIC_KEY", "ANTHROPIC_API_KEY")
	model = firstEnv("BRAIN_CHAT_ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-opus-4-8"
	}
	return key, model, key != ""
}

// toolsEnabled reports whether the agent loop should run. BRAIN_CHAT_LLM_TOOLS=0
// forces the classic single-shot RAG path.
func toolsEnabled() bool { return strings.TrimSpace(os.Getenv("BRAIN_CHAT_LLM_TOOLS")) != "0" }

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// Chat runs the live agent. It prefers the tool-calling loop (agent.go) and degrades
// to single-shot RAG (chatFallback) when tools are disabled or unsupported, so the
// endpoint never hard-breaks.
func (s *Store) Chat(ctx context.Context, q ChatQuery) (*ChatAnswer, error) {
	url, model, key, ok := chatLLM()
	if !ok {
		return nil, errors.New("brain.Chat: no LLM configured (set BRAIN_CHAT_LLM_URL / EXTRACTION_LLM_URL)")
	}
	if strings.TrimSpace(q.Message) == "" {
		return nil, errors.New("brain.Chat: empty message")
	}
	if q.TopK <= 0 || q.TopK > 20 {
		q.TopK = 8
	}
	start := time.Now()

	if !toolsEnabled() {
		return s.chatFallback(ctx, q, url, model, key, start, "tools-disabled")
	}

	// Prefer Claude (messages API + tools) when a key is present; keep Ollama as the
	// default/fallback. An Anthropic failure degrades to the Ollama agent, then RAG.
	if antKey, antModel, antOK := chatAnthropic(); antOK {
		if ans, err := s.runAnthropicAgent(ctx, q, antKey, antModel, start); err == nil {
			return ans, nil
		}
		// fall through to the Ollama agent on any Anthropic error
	}

	ans, err := s.runAgent(ctx, q, url, model, key, start)
	if err == nil {
		return ans, nil
	}
	// The model/endpoint rejected tool-calling → degrade to single-shot RAG.
	if errors.Is(err, errToolsUnsupported) {
		return s.chatFallback(ctx, q, url, model, key, start, "tools-unsupported")
	}
	return nil, errors.New("brain.Chat: " + err.Error())
}

// chatFallback is the classic single-shot RAG turn: recall → ground → generate. Used
// when tool-calling is disabled or the model can't do it. `why` is recorded on the
// footprint so the UI/operator can see why the agent degraded.
func (s *Store) chatFallback(ctx context.Context, q ChatQuery, url, model, key string, start time.Time, why string) (*ChatAnswer, error) {
	mems, err := s.Recall(ctx, RecallQuery{Namespace: q.Namespace, Query: q.Message, Limit: q.TopK, ExpandEntity: true})
	if err != nil {
		return nil, errors.New("brain.Chat: recall: " + err.Error())
	}
	fp := ChatFootprint{
		Namespace: q.Namespace, Query: q.Message, Recalled: len(mems), Model: model,
		Provider: "ollama", Mode: "rag", Grounded: len(mems) > 0, Steps: []AgentStep{},
	}
	// The single recall is still a (degenerate) step in the trace, so the footprint
	// shape stays consistent whether or not the agent loop ran.
	fp.Steps = append(fp.Steps, AgentStep{Tool: "recall", Args: `{"query":` + jsonString(q.Message) + `}`, ResultCount: len(mems), Note: "single-shot RAG (" + why + ")"})

	msgs := []map[string]string{{"role": "system", "content": chatSystemPrompt(q.Namespace, mems)}}
	for _, h := range q.History {
		role := h.Role
		if role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, map[string]string{"role": role, "content": h.Content})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": q.Message})

	answer, err := chatComplete(ctx, url, key, model, msgs)
	if err != nil {
		return nil, errors.New("brain.Chat: generate: " + err.Error())
	}
	fp.LatencyMs = int(time.Since(start).Milliseconds())
	return &ChatAnswer{Answer: strings.TrimSpace(answer), Citations: mems, Footprint: fp}, nil
}

// chatSystemPrompt frames the agent as the brain's voice and lists the recalled
// memories as numbered, citable context (single-shot RAG fallback).
func chatSystemPrompt(ns string, mems []Recalled) string {
	var b strings.Builder
	b.WriteString("You are the living memory of the \"")
	b.WriteString(ns)
	b.WriteString("\" brain — a retrieval-augmented assistant. Answer the user's question USING ONLY the memories listed below. ")
	b.WriteString("Cite the memories you use inline as [1], [2], … matching their numbers. ")
	b.WriteString("If the memories do not contain the answer, say plainly that this brain has no memory of it — do NOT invent facts. Be concise and specific.\n\n")
	if len(mems) == 0 {
		b.WriteString("MEMORIES: (none matched this question)\n")
		return b.String()
	}
	b.WriteString("MEMORIES:\n")
	for i, m := range mems {
		src := m.SourceKind
		if m.SourceRef != "" {
			src += "/" + m.SourceRef
		}
		fmt.Fprintf(&b, "[%d] (%s·%s%s) %s\n", i+1, m.Network, m.MemoryType, tern(src != "", " · "+src, ""), oneLine(m.Content, 800))
	}
	return b.String()
}

func tern(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// jsonString marshals a string to a safe JSON literal (with surrounding quotes).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// chatComplete calls an OpenAI-compatible /v1/chat/completions endpoint (no tools) —
// used by the single-shot RAG fallback.
func chatComplete(ctx context.Context, baseURL, key, model string, msgs []map[string]string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    msgs,
		"temperature": 0.2,
		"stream":      false,
	})
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	cl := &http.Client{Timeout: 90 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		if out.Error.Message != "" {
			return "", errors.New(out.Error.Message)
		}
		return "", fmt.Errorf("llm http %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("llm returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}
