# CaBrain — Execution Plan (Phase 1)

> Companion to `SPEC.md`. This maps the spec onto **this** environment and sequences the work
> around two hard blockers. Decisions locked this session:
> **(1)** build on **ToGO** (repo/CLI pointer still needed), **(2)** **wait for the infra agent** —
> do not provision infra inline (spec §1.5), **(3)** this session produces the plan only.

---

## 0. Environment truth (verified 2026-07-16)

| Thing | State | Consequence |
|---|---|---|
| `/home/coder/caBrain` | empty, **not** a git repo | `git init` is step 0.1 |
| `togo` CLI / ToGO framework | **absent everywhere on disk** | **BLOCKER A (still fully open)** — need repo URL or install cmd |
| §1.5 env vars | **partial — see §0.1 below** | **BLOCKER B clearing** — awaiting finalizer + final `.env` |
| `INFRA-CaBrain.md` | not yet delivered | part of Blocker B |
| Postgres | **LIVE**: `cabrain` DB + `cabrain_sleep` role on shared `stack-togo-postgres:latest` | reachable **only from inside `stack_stacknet`** — see §0.2 |
| Go 1.x | installed | ready for ToGO/sqlc once Blocker A clears |
| Docker client | installed, daemon down | infra agent's concern, not ours |

### 0.1 Infra status (from infra agent, 2026-07-16 — preview, not yet finalized)

| Component | State | Value / note |
|---|---|---|
| Postgres | **LIVE** | `postgresql://cabrain:***@pg:5432/cabrain`; sleep role `cabrain_sleep`; shared PG re-imaged to `stack-togo-postgres:latest` for the extensions |
| Cold tier | **LIVE (MinIO)** | S3-compatible, bucket `cabrain-cold` — substitutes for R2/GCS; `data-iceberg`/pg_duckdb path unchanged |
| TEI embeddings | **downloading model** | `Qwen/Qwen3-Embedding-0.6B` (1024-dim) → confirms `vector(1024)` in schema |
| TEI reranker | **downloading model** | `BAAI/bge-reranker-v2-m3` |
| Cognee | **wired, waiting on TEI** | `http://cognee:8000`, token set |
| Extraction LLM | **Ollama container coming up** | `mistral:7b-instruct` placeholder → override to `gpt-oss:20b` |
| NPM routes | **pending** | `cabrain.fadymondy.com`, `cognee.fadymondy.com` (added after Cognee healthy) |

**Deviations from spec (accepted):** host is P920 WSL2/Docker Desktop (not Proxmox); NPM (not Caddy); Ollama containerized; one shared Postgres; cold tier MinIO (not R2/GCS). None change the CaBrain build contracts.

### 0.2 New open question — where does the CaBrain app run on the network?

All infra hostnames (`pg`, `tei-embed`, `tei-rerank`, `cognee`, `ollama`, `minio`) resolve **only inside the `stack_stacknet` Docker network**. So the CaBrain ToGO app must either **run as a container on `stack_stacknet`** (clean, matches the stack), or run on the WSL2 host against **published ports** (needs infra to expose them). This decides how `togo new`/deploy is configured and how I benchmark recall latency (N1). **Recommend: run CaBrain as a stack container on `stack_stacknet`.** Needs your confirmation — interacts with Blocker A (the ToGO app scaffold).

**Two inputs unblock everything below:**
- **A — ToGO pointer:** repo URL + how to install the `togo` CLI (and which togo app to add the plugin to, if any).
- **B — Infra outputs:** the §1.5 env values + confirmation the infra acceptance checks pass
  (DB reachable w/ `vchord`,`vchord_bm25`,`pg_tokenizer`,`pgvector`,`pg_partman`; TEI embeds; Cognee `/health`; cold store writable).

Until both land, no step past 0.x runs. This plan is ordered so that the moment each blocker clears, there is unambiguous next work.

---

## 1. Workstreams by what unblocks them

### Track 0 — needs nothing (can start the instant you say "go")
- **0.1** `git init` in `caBrain/`; commit `SPEC.md` + this `PLAN.md`.
- **0.2** Write the **schema DDL** (spec §3) as a reviewable `.sql` file — pure Postgres, engine/infra-independent. Includes the Arabic-tokenizer BM25 index (`tokenizer='multilingual'`, **not** default English), `pg_partman` monthly partitioning, hybrid indexes. This is the contract; ToGO's `make:plugin` will consume/reconcile it.
- **0.3** Write the **tool contracts** (spec §5.1) as typed request/response schemas: `memory_retain`, `memory_recall`, `memory_recall_archive`, `memory_get`, `memory_forget`, `memory_share`. Engine-agnostic — survives even if we later drop Cognee (§8 fallback).
- **0.4** Draft the **capture-hook design** (§6): which Claude Code turns become `retain` calls, `<private>` redaction, `source_kind='claude_code'` + `source_ref=<session id>`. Design only; wiring needs a live `retain`.

### Track A — unblocked by the ToGO pointer (Blocker A)
- **A.1** Install `togo`; verify version.
- **A.2** `togo new cabrain` (or add plugin to the named existing app), DB pointed at `CABRAIN_DATABASE_URL`.
- **A.3** `togo make:plugin cabrain`; generate resources `memories`, `entities`, `memory_entities`, `memory_events`, `namespace_grants` → sqlc + Atlas + REST/GraphQL. Reconcile generated DDL against Track 0.2.

### Track B — unblocked by infra outputs (Blocker B)
- **B.1** Load §1.5 env; run the infra acceptance checks ourselves before trusting them.
- **B.2** **Embedding one-way door (do this before any bulk write):** benchmark `Qwen3-Embedding-0.6B` (1024-dim) vs `BGE-M3` on a few hundred **real Arabic** pairs (Sentra/Orchestra). Dimension is chosen **once** — changing it later means re-embedding everything (§8). Lock the `vector(N)` width only after this.

### Track A+B — needs both (the actual brain)
- **AB.1** Implement `retain` (§4.1): embed → recall neighbors → small-model ADD/UPDATE/INVALIDATE/NOOP decision → **compute `importance` at write time** (novelty + explicit flag + `source_kind` weight + recency; do **not** leave 0.5) → insert episodic/hot → populate entity graph → emit `memory_events`.
- **AB.2** Implement `recall` (§4.2): scoped hybrid retrieval (vector + BM25) fused with **RRF**, `+0.15·importance` nudge, top-20 → **rerank** (`bge-reranker-v2-m3`) → **1-hop entity expansion** → bump `access_count`/`last_accessed_at` → emit `op='recall'`. Guard **N1**: no DuckDB, no inline LLM, no cold tier on this path.
- **AB.3** Expose MCP tools (§5.1) + the **Claude Code Memory Tool backend** (§5.2): `view/read → memory_recall`, `write/str_replace → memory_retain`, scoped to project namespace.
- **AB.4** Wire **capture mode** (§6) to one Claude Code consumer (this one). Start accumulating episodics across real workstreams.

---

## 2. Phase 1 gate (target for this build)

> Fresh session calls `memory_recall` for "what did we decide about X", gets the **actual prior decision
> with provenance**, and runs past the old compaction point without losing the "why". **p95 recall < 300 ms.**

Explicitly out of scope for Phase 1 (later phases, do **not** build now): `reflect`/sleep consolidation, reconsolidation-on-recall workers, decay, cold-tier demotion, the fleet seams (Omnigent/Coder/Autopilot), live/WhatsApp interfaces.

---

## 3. Risks carried from spec §8 (watch from day one)

- **Salience left flat** = the biggest quality risk. `importance` is load-bearing in both `retain` and `recall`; treat the write-time formula as a tunable, never a constant.
- **N1 recall latency** — the hot path must stay lean. Any temptation to call an LLM or touch cold storage inline is a design bug.
- **Scoping = Sev-1.** `namespace` + `visibility` + `namespace_grants` enforced in SQL from the first multi-writer moment; `ontology` layered on at Phase 3.
- **Cognee weight vs N1** — if its write latency threatens the recall path, fall back to the direct implementation on these same tables (contracts in Track 0.3 make this a swap, not a rewrite).

---

## 4. What I need from you to proceed

1. **Blocker A (still the real gate):** ToGO repo URL + `togo` install command (and target app, if adding to an existing one). Infra is nearly ready; ToGO is not started.
2. **Blocker B:** the finalizer ping + final `.env` bundle + `INFRA-CaBrain.md`. Postgres/MinIO already live.
3. **App network placement (§0.2):** confirm CaBrain runs as a container on `stack_stacknet` (recommended) vs host + published ports.
4. **Embedding lock:** infra has committed to **Qwen3-Embedding-0.6B / 1024-dim** by downloading it (schema `vector(1024)` matches). Spec §8 wanted a Qwen3-vs-BGE-M3 benchmark on real Arabic pairs *before* locking. Confirm: **accept Qwen3 as locked**, or still run the benchmark before any bulk `retain`? (Changing dimension later = re-embed everything.)
5. **Green light on Track 0** (schema DDL + tool contracts + capture-hook design) — the only work runnable with neither blocker fully cleared, if you want momentum now.
