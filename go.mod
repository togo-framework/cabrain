module github.com/fadymondy/cabrain

go 1.26

require (
	github.com/99designs/gqlgen v0.17.66
	github.com/danielgtaylor/huma/v2 v2.27.0
	github.com/go-chi/chi/v5 v5.1.0
	github.com/togo-framework/togo v0.20.1
	// SQLite is the built-in default driver. Postgres/MySQL/Mongo drivers come from
	// their db-* PLUGIN (added to internal/plugins by `togo new --db`), which pulls
	// the raw driver transitively — so it isn't a direct dependency of this app.
	modernc.org/sqlite v1.34.1
)
