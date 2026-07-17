# Data sources (connectors)

A **data source** is a configured connector bound to a brain (namespace). On sync it
pulls documents from an external system, chunks them, and retains each chunk into the
brain through the normal §4.1 write-decision (auto-embed + BM25 + secret redaction).
Connectors self-register by `kind`, so new source types are added as plugins without
touching the core (`brain.RegisterConnector("notion", …)`).

Core: `plugins/brain/internal/brain/datasource.go` (model + lifecycle) and
`connectors.go` (built-in kinds).

## Kinds

| kind       | pull/push | config keys |
|------------|-----------|-------------|
| `text`     | pull      | `content`, `title?` |
| `markdown` | pull      | `content`, `title?` (same path as text) |
| `crawler`  | pull      | `url` |
| `github`   | pull      | `repo` (`owner/name`), `branch?` (default `main`), `path?`, `ext?` (default `.md`), `token?` |
| `sql`      | pull      | `driver?` (default `pgx`), `dsn`, `query`, `refColumn?`, `titleColumn?` |
| `webhook`  | **push**  | `secret` (auto-generated) — data arrives at `POST /api/brain/ingest/{id}` |

Secret-bearing config keys (`secret`, `token`, `password`, `apiKey`, `dsn`) are
redacted to `••••` on every read (list/get). The stored value is used server-side only.

## REST surface

| method + path | auth | body / query |
|---|---|---|
| `GET  /api/brain/datasources?namespace=` | canRead | — |
| `POST /api/brain/datasources` | canWrite | `{namespace, kind, name, config}` |
| `POST /api/brain/datasources/sync` | canWrite | `{id}` |
| `POST /api/brain/datasources/delete` | canWrite | `{id}` |
| `POST /api/brain/ingest/{id}` | **X-Webhook-Secret** header (NOT the ACL) | `{content, sourceRef?, metadata?}` |

MCP tools (mirror the REST): `datasource_list`, `datasource_create`,
`datasource_sync`, `datasource_delete`.

## High-value use case — ingest the FlowOS hub activity tables into `flowos`

The FlowOS hub Postgres holds **github activity** and **claude activity** tables that
were never ingested into the `flowos` brain. The `sql` connector pulls them: each row
becomes one document (`col: value` lines), chunked and retained.

> The hub DSN is a **secret** the owner holds — never hardcode it. Use the placeholder
> below and let the owner run the sync with real credentials (e.g. store the DSN in the
> brain's secrets vault, or pass it at create time from an env var).

### github_activity

```jsonc
// POST /api/brain/datasources   (canWrite on flowos)
{
  "namespace": "flowos",
  "kind": "sql",
  "name": "hub-github-activity",
  "config": {
    "driver": "pgx",
    "dsn": "postgres://USER:PASSWORD@HUB_HOST:5432/HUB_DB?sslmode=require",  // owner-supplied secret
    "query": "SELECT id, repo, actor, action, created_at FROM github_activity ORDER BY created_at DESC LIMIT 5000",
    "refColumn": "id",
    "titleColumn": "repo"
  }
}
```

### claude_activity

```jsonc
{
  "namespace": "flowos",
  "kind": "sql",
  "name": "hub-claude-activity",
  "config": {
    "driver": "pgx",
    "dsn": "postgres://USER:PASSWORD@HUB_HOST:5432/HUB_DB?sslmode=require",  // owner-supplied secret
    "query": "SELECT id, actor, action, detail, created_at FROM claude_activity ORDER BY created_at DESC LIMIT 5000",
    "refColumn": "id"
  }
}
```

Then trigger it (owner, with the real DSN in place):

```bash
# create returns {"id": "<uuid>", ...}
curl -sS -H "X-Cabrain-Token: $CABRAIN_TOKEN" -H 'Content-Type: application/json' \
  -X POST "$CABRAIN_API_URL/api/brain/datasources" -d @github-activity.json

curl -sS -H "X-Cabrain-Token: $CABRAIN_TOKEN" -H 'Content-Type: application/json' \
  -X POST "$CABRAIN_API_URL/api/brain/datasources/sync" -d '{"id":"<uuid>"}'
# -> {"ingested": N, "status": "ok"}
```

Each sync re-runs the query (capped at 5000 rows/run). Re-running re-retains; the
§4.1 write-decision de-dupes near-identical rows, so periodic syncs mostly add deltas.
`refColumn` sets each memory's `source_ref` (provenance) so a row is traceable back to
its `github_activity.id`.

## Push example — webhook

```bash
# 1. create a webhook source (secret is auto-generated, redacted on read)
curl -sS -H "X-Cabrain-Token: $CABRAIN_TOKEN" -H 'Content-Type: application/json' \
  -X POST "$CABRAIN_API_URL/api/brain/datasources" \
  -d '{"namespace":"flowos","kind":"webhook","name":"ci-events"}'
# the owner reads config.secret from the datasources row to configure the sender

# 2. sender pushes content (authenticated by the secret, NOT the ACL token)
curl -sS -H "X-Webhook-Secret: whk_…" -H 'Content-Type: application/json' \
  -X POST "$CABRAIN_API_URL/api/brain/ingest/<uuid>" \
  -d '{"content":"deploy 1234 succeeded on main","sourceRef":"ci/deploy/1234"}'
```

## Schema note

`datasources` is pinned to the `public` schema (`plugins/brain/internal/brain/schema.sql`),
alongside `memories`. On the live instance the app runs with
`search_path=cabrain_auth,public`; an **unqualified** `CREATE TABLE datasources` would
land in `cabrain_auth` (that is where `secrets` ended up) and — if a `public.datasources`
also existed — an unqualified `IF NOT EXISTS` would create a second, empty
`cabrain_auth.datasources` that *shadows* public in every unqualified read. Pinning the
DDL to `public.datasources` keeps it next to `memories` and makes the migration
idempotent regardless of search_path. `Migrate` runs via `cmd/brainctl migrate` (not at
API boot), so on a fresh DB apply it there; the live table was applied additively.
