package brain

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Realtime SSE fan-out — a tiny in-process hub so the dashboard (and any client)
// gets live updates when brain actions happen over MCP or from other users. No
// external realtime provider needed. Endpoint: GET /api/brain/events.

type hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newHub() *hub { return &hub{subs: map[chan string]struct{}{}} }

// publish fans an event out to all subscribers (non-blocking; drops on a full sub).
func (h *hub) publish(event string, payload map[string]any) {
	if h == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["ts"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(payload)
	msg := "event: " + event + "\ndata: " + string(b) + "\n\n"
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Events is the SSE stream endpoint. Clients: new EventSource('/api/brain/events').
func (s *Service) Events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan string, 32)
	s.hub.mu.Lock()
	s.hub.subs[ch] = struct{}{}
	s.hub.mu.Unlock()
	defer func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, ch)
		s.hub.mu.Unlock()
	}()

	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprint(w, msg)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
