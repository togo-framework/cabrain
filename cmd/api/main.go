// Command api is the cabrain HTTP entrypoint. It boots the shared togo stack
// (Huma REST + OpenAPI and gqlgen GraphQL on the kernel) and serves it.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/togo-framework/cabrain/internal/server"
)

// serveSPA serves the built frontend (web/dist) from the same binary: real files
// are served directly, unknown paths fall back to index.html (client routing).
// Enabled by WEB_DIST; API/GraphQL/docs routes are registered first so they win.
func serveSPA(router interface {
	Handle(pattern string, h http.Handler)
}, dist string) {
	fs := http.FileServer(http.Dir(dist))
	index := filepath.Join(dist, "index.html")
	router.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dist, filepath.Clean(r.URL.Path))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
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
