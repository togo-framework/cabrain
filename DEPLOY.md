# Deploying CaBrain on `stack_stacknet`

The app is a **single binary** (`cmd/api`) that serves the API + GraphQL + OpenAPI **and**
the built React console (SPA), wired to the live `cabrain` DB. Everything is proven locally;
the only reason `retain`/`recall` don't execute from the Coder workspace is that **TEI
(`tei-embed`/`tei-rerank`) listens only on `stack_stacknet`**. Running the container on that
network resolves `pg`, `tei-embed`, `tei-rerank`, `cognee`, `minio` by name and lights the
whole thing up.

## 1. Env (on-stacknet, internal names)

Use `/mnt/c/services/cabrain/.env` (the DATABASE_URL there already targets `pg:5432`). The app
reads these; secrets are never baked into the image:

| Var | On-stacknet value |
|---|---|
| `DATABASE_URL` | `postgresql://cabrain:…@pg:5432/cabrain` (from `CABRAIN_DATABASE_URL`) |
| `TEI_EMBEDDINGS_URL` / `TEI_RERANKER_URL` | `http://tei-embed:80` / `http://tei-rerank:80` |
| `TEI_EMBEDDINGS_DIM` | `1024` (BAAI/bge-m3) |
| `COGNEE_API_URL` / `COGNEE_API_TOKEN` | `http://cognee:8000` / … |
| `COLD_STORE_*` | `http://minio:9000`, bucket `cabrain-cold` |
| `ADDR` / `WEB_DIST` | `:8080` / `/app/web/dist` (set in the image) |
| `DB_DRIVER` | `pgx` |

## 2. Build + run

```bash
# from repo root (monorepo — plugins/ must be in the build context)
docker build -t cabrain:latest .

docker run -d --name cabrain \
  --network stack_stacknet \
  --env-file /mnt/c/services/cabrain/.env \
  -e DATABASE_URL="$CABRAIN_DATABASE_URL" \
  -e DB_DRIVER=pgx \
  cabrain:latest
```

(`--env-file` sets `CABRAIN_DATABASE_URL`; map it to `DATABASE_URL`, or add a `DATABASE_URL`
line to the env file. `TEI_*`/`COGNEE_*`/`COLD_STORE_*` come straight from the file.)

## 3. Public entry

Point **NPM `proxy_host id=28`** (`cabrain.fadymondy.com`, currently a Cognee placeholder) at
the `cabrain` container on port **8080**. HTTP-only, matching the existing chain.

## 4. Verify

```bash
curl -s http://cabrain:8080/api/brain/ping            # {"plugin":"brain","status":"ok"}
curl -s http://cabrain:8080/api/brain/stats           # {"ready":true, ...}
curl -s -XPOST http://cabrain:8080/api/brain/retain \
  -H 'content-type: application/json' \
  -d '{"namespace":"demo","content":"first memory","sourceKind":"manual"}'   # → {"id":…,"decision":"add"}
```

On-stacknet the `retain` embed call reaches `tei-embed` and succeeds — the same request that
fails with `lookup tei-embed: no such host` from the workspace.

## Redis L1 working-memory cache (SPEC §2.1, D4)

Recall does **cache-aside over the kernel `Cache`** (driver-agnostic), keyed by a
per-namespace epoch that every retain bumps (instant, scan-free invalidation).
Postgres stays authoritative — the cache only skips a repeat embed+query.

- **Today (no config):** `CACHE_DRIVER=memory` → an in-process L1 (still skips repeated
  identical recalls). Zero extra infra.
- **Shared Redis L1:** install the redis cache driver and switch config — the brain code
  is unchanged:
  ```bash
  togo install togo-framework/cache-redis     # registers the "redis" cache driver
  # .env:
  CACHE_DRIVER=redis
  REDIS_URL=redis://<redis-host>:6379/0        # or the cache plugin's REDIS_* keys
  BRAIN_RECALL_CACHE_TTL=30                     # seconds; 0 disables recall caching
  ```
- **Stack reality (checked 2026-07-16):** Redis is **not** in the `stack_stacknet`
  compose (services: pg, tei-embed, tei-rerank, minio, cognee, ollama) and `:6379` was not
  usably reachable from the workspace (TCP connects, peer closes with no reply). So the
  Redis L1 stays optional until Redis is attached to `stack_stacknet`; the in-process L1 is
  the default and needs nothing.

## MCP tools (SPEC §5.1)

`cmd/brain-mcp` is a stdio MCP server exposing the six memory tools
(`memory_retain`, `memory_recall`, `memory_recall_archive`, `memory_get`,
`memory_forget`, `memory_share`) — a thin adapter over the brain REST surface, so
scoping/validation stay server-side. Verified end-to-end against the live DB:
share/get/forget execute; retain/recall reach the TEI boundary and return a clean
`unavailable`. Wire it for an agent:

```bash
go install ./cmd/brain-mcp     # → $GOBIN/brain-mcp on PATH
```
```jsonc
// .mcp.json (or Claude Code MCP config)
{"mcpServers":{"cabrain":{"command":"brain-mcp",
  "env":{"CABRAIN_API_URL":"http://localhost:8080","CABRAIN_AGENT_ID":"claude-code"}}}}
```
`CABRAIN_AGENT_ID` is the session identity used for grant checks (F5); empty = the
trusted/no-enforcement context (namespace scoping still isolates data).

## Already done / follow-ups

- **Done:** schema migrated to `cabrain` (memories+default partition, entities, memory_entities,
  memory_events, namespace_grants); vector HNSW index; read-API + retain/recall + brain-tei wired;
  **BM25 fusion (hybrid recall) code-complete** — `content_bm25` column + `memories_bm25` index +
  `cabrain_ml` tokenizer, fused with the vector path via RRF in `recallSQL`, with a transparent
  vector-only fallback (`recallVecSQL`) when the BM25 layer is absent. Verified against the live DB
  with `brainctl` (schema applies; BM25 objects create + rank once granted). L1 recall cache wired +
  unit-tested.
- **Infra TODO (superuser) — BM25:** the `cabrain` app role lacks `USAGE` on `bm25_catalog` /
  `tokenizer_catalog` (the infra §5.2 BM25 test ran as superuser). Run **`infra/grant-bm25.sql`**
  as a superuser on the `cabrain` DB, then `brainctl bm25 && brainctl bm25-test`. Until then recall
  runs vector-only (no lexical fusion) — non-fatal, `ErrBM25Skipped`.
- **Infra TODO (superuser) — partman:** grant `cabrain` on `part_config`/`part_config_sub` so
  pg_partman monthly rollover (Phase 2 tiering) can replace the manual DEFAULT partition.
- **Ops CLI:** `cmd/brainctl` (`inspect` | `migrate` | `bm25` | `bm25-test`) — connects with
  `DATABASE_URL` (pgx); use it on-stack to apply/verify the BM25 layer.
- **Next (app):** 1-hop entity expansion in recall, the `brain-cognee` engine plugin for the entity
  graph, MCP tools (§5.1), and the capture-mode hook (§6).
