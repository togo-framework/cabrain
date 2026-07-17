package brain

// Live agent: chat with a selected brain. This is retrieval-augmented generation
// grounded in the brain's own memories — recall the top-k relevant memories, then
// ask an LLM to answer ONLY from them. The response carries the memories it used as
// inline **citations** and a **footprint** (the recall query, how many memories were
// pulled, the model) — the Shape-of-AI "Governors/Trust" patterns that make an AI
// answer auditable. When recall is thin the agent says the brain has no memory of it
// (memory-first R3) rather than hallucinating — that miss is also recorded as a gap.
//
// The generator is an OpenAI-compatible endpoint (the stack's Ollama, reused from the
// Cognee extraction LLM). Config, with fallbacks so it works out of the box:
//   BRAIN_CHAT_LLM_URL   (fallback EXTRACTION_LLM_URL)    e.g. http://host.docker.internal:11434
//   BRAIN_CHAT_LLM_MODEL (fallback EXTRACTION_LLM_MODEL)  e.g. silma:9b-instruct
//   BRAIN_CHAT_LLM_KEY   (fallback EXTRACTION_LLM_API_KEY)

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
}

// ChatAnswer is the grounded answer plus its provenance.
type ChatAnswer struct {
	Answer    string        `json:"answer"`
	Citations []Recalled    `json:"citations"` // the memories the answer is grounded in
	Footprint ChatFootprint `json:"footprint"`
}

// ChatFootprint is the auditable trace: what was searched, how much was found, by which model.
type ChatFootprint struct {
	Namespace string `json:"namespace"`
	Query     string `json:"query"`
	Recalled  int    `json:"recalled"`
	Model     string `json:"model"`
	Grounded  bool   `json:"grounded"` // false ⇒ nothing relevant found (caveat shown)
	LatencyMs int    `json:"latencyMs"`
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

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// Chat runs one RAG turn: recall → ground → generate. It records a gap when recall
// comes back empty (the brain couldn't answer), same as a bare recall.
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

	// 1) Retrieve — ground the answer in this brain's memories.
	mems, err := s.Recall(ctx, RecallQuery{Namespace: q.Namespace, Query: q.Message, Limit: q.TopK, ExpandEntity: true})
	if err != nil {
		return nil, errors.New("brain.Chat: recall: " + err.Error())
	}

	fp := ChatFootprint{Namespace: q.Namespace, Query: q.Message, Recalled: len(mems), Model: model, Grounded: len(mems) > 0}

	// 2) Build the grounded prompt.
	msgs := []map[string]string{{"role": "system", "content": chatSystemPrompt(q.Namespace, mems)}}
	for _, h := range q.History {
		role := h.Role
		if role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, map[string]string{"role": role, "content": h.Content})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": q.Message})

	// 3) Generate (OpenAI-compatible chat/completions).
	answer, err := chatComplete(ctx, url, key, model, msgs)
	if err != nil {
		return nil, errors.New("brain.Chat: generate: " + err.Error())
	}
	fp.LatencyMs = int(time.Since(start).Milliseconds())

	return &ChatAnswer{Answer: strings.TrimSpace(answer), Citations: mems, Footprint: fp}, nil
}

// chatSystemPrompt frames the agent as the brain's voice and lists the recalled
// memories as numbered, citable context.
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

// chatComplete calls an OpenAI-compatible /v1/chat/completions endpoint.
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
