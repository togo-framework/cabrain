# brain

The **CaBrain memory organ** as a [togo](https://github.com/togo-framework/togo) plugin — a
hardware-elastic memory brain for AI agents on `togo-postgres` (VectorChord + BM25 + pgvector):
hippocampal *hot* / cortical *cold* tiers, a Redis L1 cache, salience-gated sleep consolidation,
and reconsolidation-on-recall. Exposed as MCP tools + a Claude Code Memory Tool backend.

```bash
togo install togo-framework/brain
```

## What it does

- **retain** (§4.1) — embed → recall neighbors → ADD/UPDATE/INVALIDATE/NOOP write decision →
  compute salience → store as hot episodic → populate the entity graph.
- **recall** (§4.2) — scoped hybrid retrieval (dense vector + BM25 fused with RRF + a salience
  nudge) → rerank → 1-hop entity expansion. Hot-tier only; p95 < 300 ms (N1).
- **reconsolidation on recall** (§4.4) — contradicted facts are superseded, never duplicated.
- Never hard-deletes; `forget` soft-invalidates and history stays queryable.

## Architecture

`brain` owns its schema (`internal/brain/schema.sql`, applied by `Migrate`) and its queries in Go
— the togo plugin-schema convention. Providers are separate driver plugins registered on the
`Service` via provider seams (`Embedder`, `Reranker`, `Engine`):

| Provider plugin | Role |
|---|---|
| `brain-tei` | embeddings + rerank (TEI → Qwen3-Embedding-0.6B, bge-reranker-v2-m3) |
| `brain-cognee` | cognify engine — entity/graph extraction |
| `brain-cold-*` | cold-tier demotion (Iceberg/Parquet on S3/MinIO) — Phase 2 |
| `brain-redis` *(optional)* | L1 working-memory cache |

Developed in the [`cabrain`](https://github.com/togo-framework/cabrain) project (dev harness);
this repo is the split-out, installable plugin. Design lives in the cabrain project (`SPEC.md`).

## Status

Phase 1: schema + data layer + provider seams in place; `retain`/`recall` execution and `migrate`
land once the provisioned `cabrain` DB (vchord stack) and the `brain-tei` provider are live.
