# Deploying CaBrain on `stack_stacknet`

The app is a **single binary** (`cmd/api`) that serves the API + GraphQL + OpenAPI **and**
the built React console (SPA), wired to the live `cabrain` DB. Everything is proven locally;
the only reason `retain`/`recall` don't execute from the Coder workspace is that **TEI
(`tei-embed`/`tei-rerank`) listens only on `stack_stacknet`**. Running the container on that
network resolves `pg`, `tei-embed`, `tei-rerank`, `cognee`, `minio` by name and lights the
whole thing up.

> **Where these commands run.** Build + `docker run` happen on the **stack host** (the box
> that owns `stack_stacknet`), NOT inside the Coder workspace — the workspace has the Docker
> CLI but no daemon socket (`/var/run/docker.sock` absent), so `docker build`/`run` there fail
> with *"failed to connect to the docker API … daemon running?"*. Run everything below on the
> host, or on any machine whose Docker daemon is attached to `stack_stacknet`.

## 1. Env (on-stacknet, internal names)

Point `--env-file` at the host env file (e.g. `/mnt/c/services/cabrain/.env`, which mirrors the
workspace's `~/.env.cabrain`); its `CABRAIN_DATABASE_URL` already targets `pg:5432`. Secrets are
never baked into the image. The columns below are what the **app binary actually reads**
(confirmed by `grep Getenv`); everything else in the env file is inert for this container.

| Var | On-stacknet value | Notes |
|---|---|---|
| `DATABASE_URL` | `postgresql://cabrain:…@pg:5432/cabrain` | **Required.** Mapped from `CABRAIN_DATABASE_URL` (see §2). `togo.yaml` pins `driver: pgx`, so `DB_DRIVER` is optional/redundant. |
| `TEI_EMBEDDINGS_URL` / `TEI_RERANKER_URL` | `http://tei-embed:80` / `http://tei-rerank:80` | **Required** for retain/recall embeds + rerank. |
| `TEI_EMBEDDINGS_DIM` | `1024` (BAAI/bge-m3) | **Required**; must match the vector column dim. |
| `BRAIN_BM25_TOKENIZER` | `cabrain_bm25_tok` (default) → set `cabrain_ml` | Optional. Switch to `cabrain_ml` **after** a superuser runs `infra/grant-bm25.sql` §3 (llmlingua2 multilingual tokenizer). Until then leave unset/default. |
| `COGNEE_API_URL` | `http://cognee:8000` | Optional. Unset ⇒ cognify engine disabled (non-fatal). |
| `COGNEE_ADMIN_EMAIL` / `COGNEE_API_TOKEN` | … | Optional. Login creds for the cognify engine (confirm the auth scheme on-stack; workspace probe got 401). |
| `CACHE_DRIVER` / `REDIS_URL` / `BRAIN_RECALL_CACHE_TTL` | `memory` (default) / — / `30` | Optional L1 recall cache. `memory` (in-process) needs nothing; `redis` needs the cache-redis plugin + `REDIS_URL` (see the Redis section). Redis is **not** on `stack_stacknet` today. |
| `CABRAIN_AGENT_ID` | `claude-code` | Optional. Session identity for grant checks (MCP/API); empty = trusted context. |
| `COLD_STORE_*` | `http://minio:9000`, bucket `cabrain-cold` | Phase 2 cold-tier (MinIO). Not yet read by the binary; safe to leave in the file. |
| `ADDR` / `WEB_DIST` | `:8080` / `/app/web/dist` | **Baked into the image** — do not override. |

## 2. Build + run (on the host)

```bash
# PREREQUISITE — run codegen first. internal/db/gen and internal/graph/gen are
# gitignored (sqlc/gqlgen output), so a clean clone has no generated code and the
# Dockerfile's `go build ./cmd/api` fails with "no matching versions for query latest"
# (it tries to resolve the missing gen packages as modules). Build from a checkout where
# codegen has run so `COPY . .` includes the gen dirs:
togo generate            # sqlc → gqlgen → atlas → OpenAPI (populates internal/**/gen)

# from repo root (monorepo — plugins/ must be in the build context)
docker build -t cabrain:latest .

# Map CABRAIN_DATABASE_URL → DATABASE_URL. --env-file does NOT expand shell vars, so
# source the file into THIS shell first, then the -e mapping resolves.
set -a; . /mnt/c/services/cabrain/.env; set +a

docker run -d --name cabrain \
  --network stack_stacknet \
  --env-file /mnt/c/services/cabrain/.env \
  -e DATABASE_URL="$CABRAIN_DATABASE_URL" \
  -e DB_DRIVER=pgx \
  cabrain:latest
```

The container joins `stack_stacknet`, so `pg`, `tei-embed`, `tei-rerank`, `cognee`, `minio`
resolve by name. `-e DATABASE_URL=…` is the one required remap (the env file only defines
`CABRAIN_DATABASE_URL`); `TEI_*`/`COGNEE_*` come straight from the file. To flip on the
multilingual BM25 tokenizer, add `-e BRAIN_BM25_TOKENIZER=cabrain_ml` **after** the superuser
step in `infra/grant-bm25.sql`.

## 3. Public entry (host/admin action — do not automate)

Point **NPM `proxy_host id=28`** (`cabrain.fadymondy.com`, currently a Cognee placeholder) at
`Forward Hostname/IP = cabrain`, `Forward Port = 8080` (HTTP-only, scheme `http`, matching the
existing chain — the NPM container is on `stack_stacknet`, so it resolves the `cabrain`
container by name). This is a Nginx Proxy Manager admin change; make it in the NPM UI/API — it
is intentionally left manual here.

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

## Infra re-check (2026-07-17)

Three infra-gated items were re-verified against the live stack from the workspace. All
three are **still blocked on a human/superuser/host action** — none is a code change:

- **BM25 tokenizer (A):** `cabrain_ml` still does not exist (`tokenizer_catalog.tokenizer`
  → `{cabrain_bm25_tok, multilang}`, both wrapping the fixed-vocab `cabrain_bm25_model`).
  Creating it as role `cabrain` still fails `permission denied for table tokenizer
  (SQLSTATE 42501)`. **Remaining:** a superuser runs `infra/grant-bm25.sql` §3, then set
  `BRAIN_BM25_TOKENIZER=cabrain_ml`. App stays on the default tokenizer until then.
- **Cognee graph (B):** `POST cognee:8000/api/v1/add` still returns **HTTP 500
  `{"error":"Internal server error","detail":"Missing required pgvector credentials."}`** —
  Cognee's own vector store is still unconfigured, so cognify ingests nothing. The `flowos`
  dataset is only an empty metadata record (`GET …/data` → `[]`, `…/graph` → 500), and
  `entities`/`memory_entities` are both `count = 0`. `brainctl mirror` was therefore **not**
  run (nothing to mirror). **Remaining:** configure Cognee's pgvector credentials on the
  Cognee container (host/admin); then re-add + cognify, then `brainctl mirror flowos`.
- **Container deploy (C):** Docker daemon still absent in the workspace — `docker info`
  fails `dial unix /var/run/docker.sock: connect: no such file or directory` (socket not
  present). `docker build`/`run` cannot execute here. **Remaining:** build + run on the
  stack host per §2, and the NPM `proxy_host id=28` change per §3.

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
- **Cognify engine:** `plugins/brain-cognee` publishes the `Engine` — on retain the brain calls
  Cognee (`POST /api/v1/add` → `POST /api/v1/cognify`, run-in-background) off the hot path to build
  the entity graph. Boots active from `COGNEE_API_URL`. **Auth is deployment-specific:** the token
  is sent as `Authorization: Bearer` by default; override with `COGNEE_AUTH_HEADER` /
  `COGNEE_AUTH_PREFIX` (the workspace probe returned 401, so confirm the scheme on-stack).
  Entity-graph *mirroring* into CaBrain's `entities`/`memory_entities` (so Graph Explorer + 1-hop
  read from Postgres) is the next step — the client exposes `Search` + `DatasetGraphURL` for it.
- **Next (app):** mirror Cognee's graph into `entities`/`memory_entities`; the retain
  ADD/UPDATE/INVALIDATE/NOOP write-decision (§4.1, needs the extraction LLM); cold-tier demotion
  (Phase 2) to light up `memory_recall_archive`.
