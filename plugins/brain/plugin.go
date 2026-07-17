// Package brain is the CaBrain memory-organ togo plugin (a mini-app). It
// self-registers on blank-import via `togo install`. Backend logic lives in
// internal/brain; UI in web/. Provider integrations (TEI embeddings/rerank,
// the Cognee cognify engine, the object-store cold tier) plug in behind
// interfaces as their own driver plugins (brain-tei, brain-cognee, …).
package brain

import (
	"net/http"
	"os"

	"github.com/togo-framework/togo"

	"github.com/togo-framework/brain/internal/brain"
)

const Name = "brain"

// consoleAuthGuard is the shape of the togo auth service we need: its session
// middleware. We depend on it STRUCTURALLY (no import of the auth package) so
// the brain plugin stays decoupled from the auth plugin — *auth.Service already
// satisfies this. The service is resolved lazily off the kernel at request time,
// so provider boot-order never matters.
type consoleAuthGuard interface {
	Middleware(next http.Handler) http.Handler
}

// authRequired reports whether the human console login gate is enforced. Off by
// default so local/dev and the running instance stay reachable unauthenticated;
// set CABRAIN_REQUIRE_AUTH=1 (or true) to enforce.
func authRequired() bool {
	v := os.Getenv("CABRAIN_REQUIRE_AUTH")
	return v == "1" || v == "true"
}

// consoleAuth wraps a console admin/management handler with the human login gate.
// When CABRAIN_REQUIRE_AUTH is set, the auth plugin's JWT/session middleware must
// pass (valid Bearer token or session cookie) before the handler runs; otherwise
// the handler is served as-is. This is COMPLEMENTARY to the MCP token ACL
// (X-Cabrain-Token) enforced inside the handlers — that governs MCP agents and is
// left untouched. Fails closed (503) if enforcement is on but auth isn't active.
func consoleAuth(k *togo.Kernel, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authRequired() {
			h(w, r)
			return
		}
		if v, ok := k.Get("auth"); ok {
			if g, ok := v.(consoleAuthGuard); ok {
				g.Middleware(h).ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"auth_unavailable","message":"CABRAIN_REQUIRE_AUTH is set but the auth plugin is not active"}}`))
	}
}

func init() {
	togo.RegisterProviderFunc(Name, togo.PriorityLate, func(k *togo.Kernel) error {
		svc := brain.New(k)
		// gate wraps a console admin/management endpoint with the human login gate.
		gate := func(h http.HandlerFunc) http.HandlerFunc { return consoleAuth(k, h) }

		// Health + the console read-API (always safe; defensive when the schema
		// isn't live). retain/recall return a structured "needs DB + brain-tei"
		// error until Blocker B clears. Read + core memory ops stay open here —
		// the MCP token ACL (X-Cabrain-Token) governs their per-brain access.
		k.Router.Get("/api/brain/ping", svc.Ping)
		k.Router.Get("/api/brain/events", svc.Events) // realtime SSE (multi-user live updates)
		k.Router.Get("/api/brain/stats", svc.Stats)
		k.Router.Get("/api/brain/activity", svc.Activity)
		k.Router.Get("/api/brain/namespaces", svc.Namespaces)
		k.Router.Get("/api/brain/graph", svc.Graph)
		k.Router.Post("/api/brain/recall", svc.Recall)
		k.Router.Post("/api/brain/search", svc.Search) // cross-brain search engine
		k.Router.Post("/api/brain/retain", svc.Retain)
		// Point-lookup + lifecycle (SPEC §5.1) — pure SQL, work before brain-tei.
		k.Router.Get("/api/brain/memory", svc.Get)
		k.Router.Post("/api/brain/forget", svc.Forget)
		k.Router.Post("/api/brain/share", svc.Share)
		// Knowledge gaps (missed questions → actionable index).
		k.Router.Get("/api/brain/gaps", svc.Gaps)
		k.Router.Post("/api/brain/gaps/resolve", gate(svc.ResolveGap))
		// Brain administration: details, export/import (portability), delete, edit.
		k.Router.Get("/api/brain/brain", svc.BrainDetail)
		k.Router.Get("/api/brain/export", svc.Export)
		k.Router.Post("/api/brain/import", gate(svc.Import))
		k.Router.Post("/api/brain/brain/delete", gate(svc.DeleteBrain))
		k.Router.Post("/api/brain/memory/edit", gate(svc.EditMemory))
		// ACL: access tokens + per-brain grants (admin-only management).
		k.Router.Get("/api/brain/tokens", gate(svc.ListTokens))
		k.Router.Post("/api/brain/tokens", gate(svc.CreateToken))
		k.Router.Post("/api/brain/tokens/revoke", gate(svc.RevokeToken))
		k.Router.Post("/api/brain/grant", gate(svc.GrantBrain))
		k.Router.Post("/api/brain/grant/revoke", gate(svc.RevokeGrant))
		// Session launcher: mint a scoped token + Claude Code config for a brain.
		k.Router.Post("/api/brain/session", gate(svc.Session))
		k.Set(Name, svc)
		if k.Log != nil {
			k.Log.Info("plugin active", "plugin", Name, "consoleAuth", authRequired())
		}
		return nil
	})
}
