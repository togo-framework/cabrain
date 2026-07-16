# CaBrain — decision log (deltas on top of SPEC.md)

Running ADR-style log. SPEC.md is the original contract; this file records decisions
and reality-checks made during the build. Newest at top.

---

## D6 — brain owns its schema + queries in Go (togo plugin-schema pattern) (2026-07-16)

Confirmed from the real togo-framework plugins in the module cache: a plugin that needs
tables provisions them **at runtime in Go** — `k.SQL()` + `db.ExecContext("CREATE TABLE IF
NOT EXISTS …")`, dialect-portable via `k.Dialect().Placeholder(n)` (see `cache.cache_entries`,
`settings.settings`). Plugins do **not** ship into the host app's sqlc/atlas/`make:resource`
codegen. This is the self-contained, publishable pattern.

**Adopted for `brain`:**
- **Schema in Go.** `internal/brain/schema.sql` (the full SPEC §3 DDL) is `go:embed`ed and run
  by `Migrate()`. Ships with the plugin; no host codegen coupling.
- **Hand-written pgx queries** (retain/recall) — not sqlc. The exotic types (`vector(1024)`,
  bm25, partitioning) don't round-trip through the app's sqlite-sqlc flow anyway; hand SQL is
  both necessary and idiomatic here (matches cache/settings).
- **Consumer surface is MCP tools + retain/recall handlers** (SPEC §5), not generic CRUD REST
  on `memories`. So `make:resource`'s CRUD/GraphQL scaffolding is not the right tool for these
  tables. (If a resource genuinely needs admin CRUD later, add it then.)
- **Provider seams as interfaces** (`Embedder`, `Reranker`, `Engine`) so `brain-tei` /
  `brain-cognee` register drivers — mirrors togo's driver-registry plugins (D3).
- The app-level `sqlc.yaml` (engine sqlite) is left untouched; `brain` doesn't use it.

This overrides the literal SPEC §7-step-3 ("generate the resources → sqlc + Atlas + REST/GraphQL")
where it conflicts: the engine-agnostic contracts (schema + tools) are what's load-bearing, and
the plugin-native Go path realizes them correctly on postgres.

## D5 — Live infra reality vs SPEC/§4 bundle (2026-07-16)

Empirically probed from the Coder workspace (pgx). What is **actually reachable** and true:

- **Postgres** at `host.docker.internal:5432` (workspace superuser; creds in the stack, not here)
  is **PostgreSQL 16.14
  (Debian), vanilla** — installed extensions: only `plpgsql`; **none** of `vector`, `pg_search`,
  `vchord`, `vchord_bm25`, `pg_tokenizer`, `pg_partman`, `pg_duckdb`, `pg_cron` are even *available*.
  Databases present: `fadymondy, flowos, flowos_live, flowos_ref, flowos_v2, postgres, togo`.
  **No `cabrain` database, no `cabrain_sleep` role.**
- **Redis** `host.docker.internal:6379` — reachable. **NATS** `:4222` — reachable.
- The `pg:5432` internal hostname from the §4 bundle is **not** reachable from the workspace
  (it's inside Docker net `stack_stacknet`).

**Conclusion:** the extension-equipped CaBrain DB described in the §4 bundle (dedicated `cabrain`
DB + `cabrain_sleep` role on `stack-togo-postgres:latest` with the vchord stack) is **not yet live
/ not reachable** — consistent with the infra note that the finalizer hasn't completed. The
services-page image string (`PG17 + pg_duckdb + pg_search + vector + pg_cron`) does not match the
reachable server either, so treat it as aspirational until the finalizer confirms.

**Actions:**
- Keep `db/schema.sql` on the **VectorChord** stack per SPEC §3/§8 (deliberate choice over ParadeDB
  for Arabic `pg_tokenizer` BM25). Do **not** rewrite for ParadeDB based on the services page.
- **Blocker B stands** for live `migrate`/`serve` and for anything needing vector/BM25/partman.
- Resource build (task #9) proceeds regardless — **sqlc validates schema statically, no DB**.
- When the finalizer lands: connect to the real `cabrain` DB, run the §3 extension checks, then migrate.

## D4 — Redis L1 working-memory cache (NEW tier; extends SPEC §2/N1)

Add a Redis fast get/set tier in front of the PG hot tier — the "working-memory cache" in the
brain map, below the context window and above the hippocampal hot tier. Redis is already live
(`host.docker.internal:6379`).

- **What it caches (recall N1 path):** (a) recent `recall` query→result sets (short TTL),
  (b) hot memory rows by id, (c) content→embedding for write-time dedup. Never the source of truth.
- **PG remains authoritative.** Redis is cache-aside / write-through; a cold Redis only costs
  latency, never correctness. Invalidate on `retain`/`reconsolidate`/`forget` of touched keys.
- **How — two options:**
  1. **App-level cache-aside via `togo-framework/cache` + `cache-redis`** (RECOMMENDED for Phase 1):
     idiomatic, portable, no PG extension. The brain service checks Redis, falls back to PG,
     populates Redis. Ships as the brain using togo's cache abstraction.
  2. **`redis_fdw` / PG↔Redis connector** (the "pg_redis" idea): query/sync Redis from PG via a
     foreign data wrapper. More coupling, needs the extension in togo-postgres. **Revisit** if the
     app-level cache proves insufficient or a SQL-side join to cached data is needed.
- **Packaging:** either reuse `togo-framework/cache-redis`, or a thin `brain-redis` provider plugin
  wrapping it with brain's key schema + invalidation. Decide when wiring recall.

## D3 — Provider decomposition: one plugin per provider

Everything ships as a togo plugin (OSS). CaBrain = the `cabrain` **project** composed of plugins:
- **`brain`** (`github.com/togo-framework/brain`) — the memory organ: schema, retain/recall, MCP
  tools, capture. Providers behind interfaces.
- **`brain-tei`** — TEI embeddings + rerank driver (Qwen3-Embedding-0.6B / bge-reranker-v2-m3).
- **`brain-cognee`** — Cognee cognify-engine driver (graph+vector extraction).
- **`brain-cold-*`** — cold-tier driver (Iceberg/Parquet on MinIO/S3), Phase 2.
- (optional) **`brain-redis`** — L1 cache driver (D4).
Design `brain` with provider seams now; build each provider plugin as its consumer (retain/recall)
lands. Mirrors togo's own driver-plugin pattern (`ai-openai`, `storage-s3`, `cache-redis`, …).

## D2 — Repo shape: monorepo project + split plugin (2 repos)

- **`togo-framework/cabrain`** — the project/dev-harness (togo app, module
  `github.com/togo-framework/cabrain`); hosts `plugins/brain` (+ future plugins), SPEC/PLAN/docs.
- **`togo-framework/brain`** — the publishable plugin, split from `plugins/brain`.
- Wired locally via `require github.com/togo-framework/brain v0.0.0` +
  `replace … => ./plugins/brain` (works with all go tooling incl. `togo generate`); blank-imported
  in `internal/plugins/local.go` (out of the DO-NOT-EDIT `plugins.gen.go`).

## D1 — Engine-agnostic contracts first (Track 0)

`db/schema.sql`, `contracts/tools.md`, `docs/capture-mode.md` are the source of truth and the
direct-on-Postgres fallback (SPEC §8), independent of ToGO and any live infra.
