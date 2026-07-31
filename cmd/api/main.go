// Command api is the cabrain HTTP entrypoint. It boots the shared togo stack
// (Huma REST + OpenAPI and gqlgen GraphQL on the kernel) and serves it.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/togo-framework/cabrain/internal/server"
)

// ensureAuthSecret guarantees a >=32-byte AUTH_SECRET before Boot(), so the togo
// auth plugin's fail-closed production check can never silently take /api/auth/*
// offline after a bare restart when /deploy/.env forgot to set one. Priority:
// env → persistent file → generate+persist. Env value always wins.
func ensureAuthSecret() {
	if s := os.Getenv("AUTH_SECRET"); len(s) >= 32 {
		return
	}
	candidates := []string{}
	if m := os.Getenv("MEDIA_DIR"); m != "" {
		candidates = append(candidates, m)
	}
	if c := os.Getenv("COLD_STORE_LOCAL_DIR"); c != "" {
		candidates = append(candidates, c)
	}
	candidates = append(candidates,
		"/app/data",
		"/app/web/dist-media",
		"/var/lib/cabrain",
		"/tmp",
	)
	var secretFile string
	for _, d := range candidates {
		if err := os.MkdirAll(d, 0700); err == nil {
			secretFile = filepath.Join(d, ".auth_secret")
			break
		}
	}
	if secretFile != "" {
		if b, err := os.ReadFile(secretFile); err == nil {
			v := strings.TrimSpace(string(b))
			if len(v) >= 32 {
				os.Setenv("AUTH_SECRET", v)
				fmt.Printf("→ loaded AUTH_SECRET from %s (env was empty/short)\n", secretFile)
				return
			}
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("cannot generate AUTH_SECRET: " + err.Error())
	}
	v := hex.EncodeToString(buf)
	os.Setenv("AUTH_SECRET", v)
	if secretFile != "" {
		if err := os.WriteFile(secretFile, []byte(v), 0600); err == nil {
			fmt.Printf("→ generated AUTH_SECRET → %s (stable across restarts)\n", secretFile)
			return
		}
	}
	fmt.Println("→ generated ephemeral AUTH_SECRET (no persistent dir writable; sessions reset on restart)")
}

// serveSPA serves the built frontend (web/dist) from the same binary: real files
// are served directly, unknown paths fall back to index.html (client routing).
// Enabled by WEB_DIST; API/GraphQL/docs routes are registered first so they win.
func serveSPA(router interface {
	Handle(pattern string, h http.Handler)
}, dist string) {
	fs := http.FileServer(http.Dir(dist))
	index := filepath.Join(dist, "index.html")
	router.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An /api/* (or /graphql) request that reached the SPA catch-all is a
		// genuinely UNREGISTERED backend route — the real API/GraphQL handlers are
		// more specific and win before this. Never answer it with the HTML shell:
		// that returns text/html with 200, and the frontend's fetch(...).json()
		// then dies on `Unexpected token '<', "<!doctype "...`. Return proper 404
		// JSON so callers see a clean error (and the missing route is visible, not
		// silently masked).
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/graphql" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such API route"}}`))
			return
		}
		p := filepath.Join(dist, filepath.Clean(r.URL.Path))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			// Vite fingerprints these (index-7vWwHpKc.js), so a given URL's bytes never
			// change — cache them hard. index.html is handled below and must NOT get
			// this.
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fs.ServeHTTP(w, r)
			return
		}
		// A request for a static asset (any path with a non-.html extension —
		// /assets/index-STALE.js, a font, a source map) that doesn't exist must
		// return 404, NOT the HTML shell. Serving index.html here makes the browser
		// try to parse HTML as a JS module: "Expected a JavaScript-or-Wasm module
		// script but the server responded with a MIME type of text/html". Only real
		// client-routes (extensionless / .html) fall back to index.html so deep
		// links still work.
		if ext := filepath.Ext(r.URL.Path); ext != "" && ext != ".html" {
			http.NotFound(w, r)
			return
		}
		// index.html is the ONLY unfingerprinted file, and it is what names the current
		// asset bundle. Served with no Cache-Control (as it was), browsers fall back to
		// heuristic caching off Last-Modified and keep re-using a stale shell — which
		// then loads the PREVIOUS bundle long after a deploy. That is why a shipped UI
		// fix kept appearing not to land: the API was correct, the new bundle was on
		// disk, and the browser never asked for the shell again. Must-revalidate.
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		http.ServeFile(w, r, index)
	}))
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "openapi" {
		b, err := server.OpenAPI()
		if err != nil {
			panic(err)
		}
		os.Stdout.Write(b)
		return
	}

	ensureAuthSecret()

	a := server.Boot()
	defer a.Kernel.Close()
	k := a.Kernel
	if dist := os.Getenv("WEB_DIST"); dist != "" {
		serveSPA(k.Router, dist)
		fmt.Printf("→ serving frontend from %s\n", dist)
	}
	fmt.Printf("→ cabrain listening on %s  (GraphQL %s · REST %s · docs %s)\n",
		k.Config.Addr, k.Config.GraphQLPath, k.Config.RESTPath, k.Config.DocsPath)
	if err := k.Serve(context.Background()); err != nil {
		panic(err)
	}
}
