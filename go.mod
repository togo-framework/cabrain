module github.com/togo-framework/cabrain

go 1.26.4

require (
	github.com/99designs/gqlgen v0.17.66
	github.com/danielgtaylor/huma/v2 v2.27.0
	github.com/go-chi/chi/v5 v5.3.0
	github.com/togo-framework/brain v0.0.0
	github.com/togo-framework/brain-cognee v0.0.0
	github.com/togo-framework/brain-tei v0.0.0
	github.com/togo-framework/cache v0.3.0
	github.com/togo-framework/cache-redis v0.0.0
	github.com/togo-framework/db-postgres v0.1.0
	github.com/togo-framework/queue v0.3.0
	github.com/togo-framework/storage v0.2.0
	github.com/togo-framework/togo v0.21.0
	github.com/vektah/gqlparser/v2 v2.5.22
	// SQLite is the built-in default driver. Postgres/MySQL/Mongo drivers come from
	// their db-* PLUGIN (added to internal/plugins by `togo new --db`), which pulls
	// the raw driver transitively — so it isn't a direct dependency of this app.
	modernc.org/sqlite v1.34.1
)

// The brain plugin is developed in-repo (monorepo dev harness). Resolve it
// locally so all go tooling (build, `go mod tidy`, `togo generate`) works
// without the published github.com/togo-framework/brain repo. Removed when the
// harness consumes the published plugin.
replace github.com/togo-framework/brain => ./plugins/brain

replace github.com/togo-framework/brain-tei => ./plugins/brain-tei

replace github.com/togo-framework/brain-cognee => ./plugins/brain-cognee

replace github.com/togo-framework/cache-redis => ./plugins/cache-redis

require (
	github.com/jackc/pgx/v5 v5.7.1
	github.com/togo-framework/auth v0.8.0
	github.com/togo-framework/auth-dev v0.1.0
)

require (
	github.com/agnivade/levenshtein v1.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.5 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.2.1 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/redis/go-redis/v9 v9.7.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	github.com/togo-framework/orm v0.1.0 // indirect
	github.com/togo-framework/providers v0.1.0 // indirect
	github.com/togo-framework/settings v0.1.1 // indirect
	github.com/urfave/cli/v2 v2.27.5 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/mod v0.23.0 // indirect
	golang.org/x/sync v0.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	golang.org/x/tools v0.30.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/gc/v3 v3.0.0-20240107210532-573471604cb6 // indirect
	modernc.org/libc v1.55.3 // indirect
	modernc.org/mathutil v1.6.0 // indirect
	modernc.org/memory v1.8.0 // indirect
	modernc.org/strutil v1.2.0 // indirect
	modernc.org/token v1.1.0 // indirect
)
