// Package server wires the togo kernel with both interfaces (Huma REST/OpenAPI
// and gqlgen GraphQL) and returns a ready App. It lives in its own package so
// both cmd/api (serve) and feature tests can boot the exact same stack.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/cabrain/internal/app"
	graphgen "github.com/fadymondy/cabrain/internal/graph/gen"
	"github.com/fadymondy/cabrain/internal/graph/resolvers"
	_ "github.com/fadymondy/cabrain/internal/plugins" // blank-imports installed plugins (togo install)
	"github.com/fadymondy/cabrain/internal/rest"
)

// Boot builds the kernel + app and mounts REST, GraphQL, health, and SSE on the
// kernel router. It does not start listening — the caller calls Kernel.Serve.
func Boot() *app.App {
	k := togo.New()
	a := app.New(context.Background(), k)

	api := humachi.New(k.Router, huma.DefaultConfig("Cabrain API", "0.1.0"))
	rest.RegisterRoutes(api, a)

	gql := handler.NewDefaultServer(graphgen.NewExecutableSchema(graphgen.Config{
		Resolvers: &resolvers.Resolver{App: a},
	}))
	k.Router.Handle(k.Config.GraphQLPath, gql)
	k.Router.Handle("/graphql/play", playground.Handler("Cabrain GraphQL", k.Config.GraphQLPath))

	k.Router.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":"cabrain","togo":%q}`, togoVersion())
	})

	// Resource registry for the admin dashboard — auto-discovers tables from the
	// manifest so generated resources appear in the dashboard without config.
	k.Router.Get("/api/_meta/resources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resourcesJSON())
	})

	if k.Realtime != nil { // realtime feature enabled
		k.Router.Get("/events", k.Realtime.Handler())
	}
	return a
}

// resourcesJSON returns the resource list from togo.resources.yaml as
// {"resources":[{"name","table","fields":[{"name","type","nullable"}]}]} for the
// admin dashboard — fields let the admin build create/edit forms from the resource
// definition (so forms work on an empty table, not just by inferring from rows).
func resourcesJSON() []byte {
	data, err := os.ReadFile("togo.resources.yaml")
	if err != nil {
		return []byte(`{"resources":[]}`)
	}
	type field struct {
		Name     string   `json:"name"`
		Type     string   `json:"type"`
		Nullable bool     `json:"nullable"`
		Enum     []string `json:"enum,omitempty"`     // select options, if the field declares `enum:`
		Relation string   `json:"relation,omitempty"` // related table for a belongs-to, if `relation:`/`rel:`
	}
	type res struct {
		Name   string  `json:"name"`
		Table  string  `json:"table"`
		Fields []field `json:"fields"`
	}
	var out []res
	var cur res
	have := false
	resourceIndent := -1
	flush := func() {
		if have {
			out = append(out, cur)
		}
	}
	// Indentation distinguishes a resource item (`  - name:`) from a field item
	// (`      - name:`); fields carry go-type + nullability for the form.
	for _, ln := range strings.Split(string(data), "\n") {
		indent := len(ln) - len(strings.TrimLeft(ln, " "))
		t := strings.TrimSpace(ln)
		switch {
		case t == "" || strings.HasPrefix(t, "#"):
			// skip
		case strings.HasPrefix(t, "- name:"):
			// The first list item sets the resource-level indent; items at that
			// indent are resources, deeper ones are fields. yaml.v3's absolute
			// indentation varies, so don't hard-code a threshold (the old `<= 3`
			// mis-parsed fields → empty create forms).
			if resourceIndent < 0 {
				resourceIndent = indent
			}
			name := strings.TrimSpace(strings.TrimPrefix(t, "- name:"))
			if indent <= resourceIndent {
				flush()
				cur = res{Name: name}
				have = true
			} else if have {
				cur.Fields = append(cur.Fields, field{Name: name, Type: "string"})
			}
		case strings.HasPrefix(t, "table:") && have:
			cur.Table = strings.TrimSpace(strings.TrimPrefix(t, "table:"))
		case strings.HasPrefix(t, "go:") && len(cur.Fields) > 0:
			cur.Fields[len(cur.Fields)-1].Type = strings.TrimSpace(strings.TrimPrefix(t, "go:"))
		case strings.HasPrefix(t, "null:") && len(cur.Fields) > 0:
			cur.Fields[len(cur.Fields)-1].Nullable = strings.TrimSpace(strings.TrimPrefix(t, "null:")) == "true"
		case (strings.HasPrefix(t, "enum:")) && len(cur.Fields) > 0:
			// `enum: [draft, published]` or `enum: draft,published` → select options.
			raw := strings.TrimSpace(strings.TrimPrefix(t, "enum:"))
			raw = strings.Trim(raw, "[]")
			var vals []string
			for _, v := range strings.Split(raw, ",") {
				if v = strings.TrimSpace(strings.Trim(v, `"'`)); v != "" {
					vals = append(vals, v)
				}
			}
			cur.Fields[len(cur.Fields)-1].Enum = vals
		case (strings.HasPrefix(t, "relation:") || strings.HasPrefix(t, "rel:")) && len(cur.Fields) > 0:
			v := strings.TrimPrefix(strings.TrimPrefix(t, "relation:"), "rel:")
			cur.Fields[len(cur.Fields)-1].Relation = strings.TrimSpace(v)
		}
	}
	flush()
	b, _ := json.Marshal(map[string]any{"resources": out})
	return b
}

// togoVersion reports the togo framework version this app is built against, read
// from the build's module graph (a real codebase variable, not hard-coded).
func togoVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			if d.Path == "github.com/togo-framework/togo" {
				return d.Version
			}
		}
	}
	return "dev"
}

// OpenAPI returns the generated OpenAPI 3.1 document (used by `togo generate`).
func OpenAPI() ([]byte, error) {
	router := chi.NewMux()
	api := humachi.New(router, huma.DefaultConfig("Cabrain API", "0.1.0"))
	rest.RegisterRoutes(api, nil)
	return api.OpenAPI().YAML()
}
