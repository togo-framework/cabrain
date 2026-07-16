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

## Already done / follow-ups

- **Done:** schema migrated to `cabrain` (memories+default partition, entities, memory_entities,
  memory_events, namespace_grants); vector HNSW index; read-API + retain/recall + brain-tei wired.
- **Infra TODO (superuser):** grant `cabrain` on `part_config`/`part_config_sub` so pg_partman
  monthly rollover (Phase 2 tiering) can replace the manual DEFAULT partition.
- **Next (app):** BM25 fusion (create the `cabrain_ml` tokenizer + `content_bm25` column + `bm25`
  index; fuse `<&>`/`to_bm25query` into recall), and the `brain-cognee` engine plugin for the
  entity graph.
