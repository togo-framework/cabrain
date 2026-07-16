# SPEC.md — CaBrain

> **A general, hardware-elastic memory brain for AI agents, built as a ToGO plugin on `togo-postgres`.**
> Build the brain first. Run it in capture mode across every session until it has real memory mass. Then attach any interface — Claude Code, `live` agents, chat, the Autopilot fleet — as a new mouth on the same brain.

This document is written to be executed by Claude Code, in order, phase by phase. Do not skip phase gates. Each phase ends with an acceptance test that must pass before the next phase begins.

---

## 0. North star (read this first)

CaBrain is **not** an agent, a chatbot, or a coding tool. It is a *memory organ*: a service that ingests everything the org's agents and people say and do, distills it into durable knowledge, and — when asked, or proactively when something matters — returns exactly the right memory so the consumer never re-derives what is already known and never hits a context wall.

The reference model is the human brain, with the biological ceilings deliberately removed:

| Brain property | Kept (dynamics) | Removed (ceiling) |
|---|---|---|
| Two-speed learning (hippocampus → neocortex) | ✅ fast episodic writes, slow semantic consolidation | — |
| Sleep consolidation | ✅ heavy reorganization runs offline as scheduled jobs | — |
| Salience (amygdala) | ✅ importance gates consolidation priority *and* decay rate | — |
| Reconsolidation on recall | ✅ recalled facts that conflict get updated, not appended | — |
| Fixed neuron count | — | ❌ storage scales horizontally + infinite object-store cold tier |
| Lossy forgetting | — | ❌ compress the hot set, but **never delete** — cold tier is perfect + unbounded |
| Single isolated brain | — | ❌ one shared brain across N agents |
| Confabulation | — | ❌ provenance kept; evidence never blurs into inference |

**Engine decision:** CaBrain does not reinvent memory extraction. It **wraps Cognee** (Apache-2.0 — remember/recall/forget/improve, graph+vector, tenant isolation, MCP-native) as its cognify engine, and adds the four dynamics above plus the ToGO-native plugin shell and the Claude Code memory-tool front. If Cognee's pipeline proves too heavy at any point, the schema and tool contracts below are engine-agnostic and can fall back to a direct implementation on the same tables.

**Build order (non-negotiable):**
1. **Phase 1 — the brain + capture.** Standalone CaBrain plugin, single Claude Code consumer via the Memory Tool, capturing session memory. *This is the only phase that must ship before you have anything usable.*
2. **Phase 2 — the sleep loop.** Consolidation, salience, reconsolidation, tiering workers. The brain starts getting *smarter*, not just bigger.
3. **Phase 3 — the fleet seams.** `impl=omnigent`, `exec=coder`, Autopilot — many agents on one brain.
4. **Phase 4 — the interfaces.** `live` + `live-whatsapp` + `live-notify`: conversation capture and the salience-gated proactive push.

---

## 0.1 Project & plugin shape (how CaBrain is packaged)

CaBrain is a **project composed of shippable togo plugins** — nothing is a monolith, so every
capability can be open-sourced and installed independently, the togo way.

- **`cabrain`** — the *project* (this monorepo + dev harness; togo app, module
  `github.com/togo-framework/cabrain`; repo `togo-framework/cabrain`). It hosts the plugins,
  the design docs, and the `generate → migrate → serve` loop that exercises them.
- **`brain`** — the *core memory-organ plugin* (module `github.com/togo-framework/brain`; repo
  `togo-framework/brain`). Owns the schema (§3), the write/read pipelines (§4), the MCP surface
  (§5), and capture (§6). Installable anywhere with `togo install togo-framework/brain`.
- **provider plugins** — one plugin per external dependency, registered on `brain` behind
  interfaces (driver-registry pattern, like togo's own `ai-openai` / `storage-s3` / `cache-redis`):

  | Plugin | Provides | Interface |
  |---|---|---|
  | `brain-tei` | embeddings + rerank (TEI → Qwen3-Embedding-0.6B, bge-reranker-v2-m3) | `Embedder`, `Reranker` |
  | `brain-cognee` | cognify engine — entity/graph extraction | `Engine` |
  | `brain-cold-*` | cold-tier demotion (Iceberg/Parquet on S3/MinIO) — Phase 2 | (cold store) |
  | `brain-redis` *(optional)* | L1 working-memory cache (§2.1) | via `togo-framework/cache` |

  Absent a provider, the dependent op returns a clear "install `brain-<x>`" error — never a silent
  degrade. This keeps `brain` engine-agnostic (SPEC §8): swapping Cognee, the embedder, or the cold
  store is a plugin swap, not a rewrite.

**Plugin owns its schema in Go.** Per the togo plugin convention (see `cache`/`settings`), `brain`
ships its DDL embedded (`internal/brain/schema.sql`, applied by `Migrate`) and writes its own
queries — it does **not** route the exotic postgres schema (`vector`, BM25, partitioning) through
the app-level sqlc/atlas `make:resource` flow. This supersedes the literal "generate the resources
→ sqlc + Atlas" wording in §7 where they conflict; the load-bearing artifacts are the schema (§3)
and the tool contracts (§5.1), which the plugin-native Go path realizes on postgres.

---

## 1. Requirements

### Functional
- **F1** Ingest arbitrary memory items (a Claude Code session turn, a Coder run transcript, a WhatsApp thread, a decision) via one `retain` path.
- **F2** Return the most relevant memory for a query via `recall`, using **hybrid retrieval** (dense vector + BM25 long-text) fused with Reciprocal Rank Fusion, reranked.
- **F3** Distill raw episodic memory into durable semantic facts + entity summaries via `reflect` (consolidation), on a schedule ("sleep").
- **F4** Update contradicted facts on recall (reconsolidation) instead of accumulating contradictions.
- **F5** Scope every memory by namespace + visibility so N agents share one brain without cross-scope leakage.
- **F6** Expose the whole surface as **MCP tools** and as a **Claude Code Memory Tool backend**, so no consumer is ever coupled to CaBrain internals.
- **F7** Run in **capture mode**: passively record all consumer sessions into episodic memory with minimal friction.

### Non-functional
- **N1 — Recall latency:** p95 `recall` < 300 ms on the hot tier. The retrieval path must never invoke DuckDB, never call an LLM inline, and never touch the cold tier synchronously.
- **N2 — Unlimited capacity:** raw size must never be the constraint. Hot tier stays small via consolidation + demotion; cold tier is Parquet/Iceberg on object storage.
- **N3 — Elastic:** scales across hardware — VectorChord index build off-box, partitioning, and object-store cold tier, all inherited from the ToGO/`togo-postgres` substrate.
- **N4 — Multilingual:** first-class Arabic + English (Egyptian Arabic context). Tokenizer and embedding model must handle Arabic well.
- **N5 — Self-hosted:** no dependency on any hosted memory API. Embeddings/rerank run locally on the RTX 3060 via TEI.
- **N6 — Governed:** per-agent spend/cost visibility (`ai-agentops`) and permission-aware data access (`ontology`) from the moment more than one agent connects.

### Constraints
- Substrate: **ToGO** microkernel + `togo-postgres` (VectorChord/pgvector + `pg_search`) + Atlas migrations + sqlc.
- Hardware: Proxmox host (dual Xeon Gold 6138, 128 GB RAM, RTX 3060 12 GB) for the self-hosted embedding/rerank/consolidation plane. Cold tier on R2 or GCS.
- Team: small; favor generated code and reused ToGO plugins over bespoke infra.

---

## 1.5 Infra inputs (provisioned separately)

Infrastructure is **not** provisioned by this spec. A separate infra agent stands it up and returns the outputs below (full brief + acceptance checks in `INFRA-CaBrain.md`). The CaBrain build reads them as environment variables and never hardcodes a host, port, or credential.

| Env var | What it is |
|---|---|
| `CABRAIN_DATABASE_URL` | Postgres DSN for `togo-postgres` with all required extensions preinstalled |
| `COGNEE_API_URL` | Cognee engine REST base (e.g. `http://cognee:8000`) |
| `COGNEE_API_TOKEN` | Cognee auth token, if set (optional) |
| `TEI_EMBEDDINGS_URL` / `TEI_EMBEDDINGS_MODEL` | Embeddings endpoint + model (default `Qwen3-Embedding-0.6B`, 1024-dim) |
| `TEI_RERANKER_URL` / `TEI_RERANKER_MODEL` | Rerank endpoint + model (`bge-reranker-v2-m3`) |
| `EXTRACTION_LLM_PROVIDER` / `_URL` / `_MODEL` / `_API_KEY` | Structured-output LLM for cognify (Ollama or API) |
| `COLD_STORE_ENDPOINT` / `_BUCKET` / `_REGION` / `_ACCESS_KEY` / `_SECRET_KEY` | S3-compatible object store for the Iceberg/Parquet cold tier |

Secrets arrive via the app's env/secret mechanism from the infra agent; the build never embeds credential values. If any output is missing, stop and request it — do not fall back to provisioning infra inline.

### 1.5.1 As-provisioned (infra §4 bundle + accepted deviations)

The actual environment differs from the spec's assumptions in ways that don't change the CaBrain
contracts (full detail in `docs/decisions.md` D5). Accepted:

- **Host:** P920 workstation, WSL2 + Docker Desktop — **not** Proxmox. Reverse proxy is **NPM**
  (Nginx Proxy Manager), not Caddy. From the workspace, stack services resolve at
  `host.docker.internal` (Postgres :5432, Redis :6379, NATS :4222); the CaBrain app runs as a
  container on Docker net `stack_stacknet`, where infra uses internal names (`pg`, `tei-embed`,
  `cognee`, `ollama`, `minio`).
- **Cold tier:** MinIO (S3-compatible, bucket `cabrain-cold`) substitutes for R2/GCS — the
  `data-iceberg`/`pg_duckdb` path is unchanged.
- **Extraction LLM:** Ollama as a stack container (`mistral:7b-instruct` placeholder → `gpt-oss:20b`).
- **Postgres:** one shared PG re-imaged to give CaBrain its extensions, with a dedicated `cabrain`
  DB + `cabrain_sleep` role.
- **Also live and usable:** **Redis** (L1 cache, §2.1) and **NATS**.

**Status (as of build):** the finalizer had not completed — TEI models still downloading, Cognee
waiting on TEI, the `cabrain` DB not yet reachable from the workspace (the workspace-reachable
Postgres is a vanilla PG16 without the required extensions). So live `migrate`/`serve` and
retain/recall **execution** stay gated (Blocker B) until the finalized `.env` + `INFRA-CaBrain.md`
land and the §3 extension checks pass. Schema-static work (build, sqlc, codegen) proceeds regardless.

---

## 2. High-level architecture

```
                 consumers (Phase 1: Claude Code only)
                 Memory Tool  ·  MCP tools
                          │
        ┌─────────────────▼──────────────────┐
        │  brain plugin (togo)                │
        │  ── API: retain · recall · reflect  │
        │  ── forget · improve                │
        │  ── engine: brain-cognee (wrapped)  │
        │  ── salience · reconsolidation      │
        └──┬────────┬───────────────┬─────────┘
   reads   │ writes │               │  reads
   ┌───────▼─┐ ┌────▼─────────┐ ┌───▼────────────┐
   │ L1 cache│ │  HOT tier     │ │ brain-tei      │
   │ Redis   │ │  togo-postgres│ │ TEI · RTX 3060 │
   │ (§2.1)  │ │  VectorChord  │ │ Qwen3-Embedding│
   │         │ │  + vchord_bm25│ │ + bge-reranker │
   └─────────┘ └───────┬───────┘ └────────────────┘
                │ sleep (scheduler workers)
        ┌───────▼───────────────────┐
        │  COLD tier                 │
        │  Iceberg/Parquet on R2/GCS │
        │  via data-iceberg/pg_duckdb│
        └────────────────────────────┘
```

**Memory subsystems (the brain map):**
- **Working memory** = the consumer's context window + the MCP gateway (central executive). Not stored by CaBrain; it decides what to page in.
- **L1 cache (Redis)** = a fast get/set layer in front of the hot tier (§2.1). Caches recent recall result-sets, hot rows by id, and content→embedding for dedup. Never authoritative — a cold Redis costs latency, never correctness.
- **Hippocampus (hot tier)** = fresh, individually-stored episodic memories in `togo-postgres`, VectorChord-indexed. Small and fast.
- **Neocortex (cold tier)** = consolidated semantic facts + entity summaries; raw episodics demoted to Iceberg. Unbounded.
- **Amygdala** = `importance` score computed at write time, gating consolidation priority and decay rate.
- **Sleep** = `scheduler` workers doing consolidation, dedup, decay, tiering, index maintenance — always offline, never on the recall path.

### 2.1 L1 cache tier (Redis) — protecting N1

A Redis fast-path sits between working memory and the PG hot tier (Redis is live on the stack).
It exists to keep p95 `recall` well under 300 ms (N1) and to cut redundant embedding calls.

- **Caches:** (a) recent `recall` query→result sets (short TTL), (b) hot memory rows by id,
  (c) content→embedding for write-time dedup. **PG remains the source of truth**; Redis is
  cache-aside / write-through and is invalidated on `retain`/`reconsolidate`/`forget` of touched keys.
- **How:** app-level cache-aside via **`togo-framework/cache` + `cache-redis`** (Phase-1 default —
  idiomatic, portable, no PG extension). The `redis_fdw` / PG↔Redis-connector approach ("pg_redis")
  is a **revisit** option if a SQL-side join against cached data is ever needed.
- **Packaging:** reuse `cache-redis`, or a thin `brain-redis` plugin wrapping it with brain's key
  schema + invalidation. Never on the write-correctness path — only latency.

---

## 3. Data model

Create these on `togo-postgres`. The base image must expose `vchord`, `vchord_bm25`, `pg_tokenizer`, `pgvector`, and `pg_partman`. Use `pg_tokenizer`'s multilingual/ICU tokenizer for BM25 so Arabic tokenizes correctly (**not** the default English tokenizer).

### 3.1 Extensions & roles

```sql
CREATE EXTENSION IF NOT EXISTS vchord CASCADE;        -- vector index (billions/node, off-box build)
CREATE EXTENSION IF NOT EXISTS vchord_bm25 CASCADE;   -- BM25 long-text
CREATE EXTENSION IF NOT EXISTS pg_tokenizer CASCADE;  -- multilingual tokenizer (Arabic)
CREATE EXTENSION IF NOT EXISTS pg_partman CASCADE;    -- time partitioning

-- Analytics/consolidation plane runs as a separate role so it never shares
-- a connection with the latency-critical recall path (see N1).
CREATE ROLE cabrain_sleep LOGIN;
-- If/when pg_duckdb is added for telemetry, enable it per-role here, never globally.
```

### 3.2 Core table: `memories`

```sql
CREATE TABLE memories (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  -- scoping (F5): every read/write is filtered by these
  namespace      text NOT NULL,                      -- e.g. 'sentra', 'freshup', 'orchestra'
  owner_agent_id text,                               -- which agent/persona wrote it
  visibility     text NOT NULL DEFAULT 'private',    -- private | team | global

  -- brain-map classification
  network        text NOT NULL,                      -- fact | experience | observation | belief
  memory_type    text NOT NULL DEFAULT 'episodic',   -- episodic | semantic | procedural | working
  content        text NOT NULL,                      -- the raw or distilled text
  source_kind    text,                               -- claude_code | coder_run | whatsapp | slack | chat | manual
  source_ref     text,                               -- session id / thread id / run id (provenance)

  -- retrieval
  embedding      vector(1024),                       -- Qwen3-Embedding-0.6B / BGE-M3, 1024-dim

  -- amygdala (salience)
  importance     real NOT NULL DEFAULT 0.5,          -- [0,1]; drives consolidation + decay
  access_count   int  NOT NULL DEFAULT 0,
  last_accessed_at timestamptz,

  -- temporal / reconsolidation (never hard-delete)
  valid_at       timestamptz NOT NULL DEFAULT now(),
  invalid_at     timestamptz,                        -- set on reconsolidation; row stays queryable
  superseded_by  uuid REFERENCES memories(id),       -- the reconsolidated replacement

  -- tiering
  tier           text NOT NULL DEFAULT 'hot',        -- hot | cold (demoted to Iceberg)

  metadata       jsonb NOT NULL DEFAULT '{}'
) PARTITION BY RANGE (valid_at);

-- pg_partman: monthly partitions; old/invalidated partitions are the demotion unit.
SELECT partman.create_parent('public.memories', 'valid_at', 'native', 'monthly');

-- hybrid retrieval indexes (hot tier)
CREATE INDEX memories_vec  ON memories USING hnsw (embedding vector_cosine_ops);
CREATE INDEX memories_bm25 ON memories USING bm25 (id, content)
  WITH (tokenizer='multilingual');                   -- Arabic-capable
CREATE INDEX memories_ns   ON memories (namespace, tier) WHERE invalid_at IS NULL;
CREATE INDEX memories_sal  ON memories (importance DESC, last_accessed_at);
```

### 3.3 Supporting tables

```sql
-- entity graph (spreading activation on recall; Cognee owns population)
CREATE TABLE entities (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  namespace text NOT NULL,
  name text NOT NULL,
  summary text,                                       -- the consolidated "what we know about X"
  embedding vector(1024),
  UNIQUE (namespace, name)
);
CREATE TABLE memory_entities (
  memory_id uuid REFERENCES memories(id) ON DELETE CASCADE,
  entity_id uuid REFERENCES entities(id) ON DELETE CASCADE,
  PRIMARY KEY (memory_id, entity_id)
);

-- append-only telemetry (OLAP; consolidation candidates, usage, cost)
CREATE TABLE memory_events (
  id bigserial PRIMARY KEY,
  ts timestamptz NOT NULL DEFAULT now(),
  namespace text,
  op text NOT NULL,                                   -- retain | recall | reflect | forget | reconsolidate | demote
  memory_id uuid,
  agent_id text,
  latency_ms int,
  metadata jsonb NOT NULL DEFAULT '{}'
);

-- namespace access grants (the claim/share model)
CREATE TABLE namespace_grants (
  agent_id text NOT NULL,
  namespace text NOT NULL,
  can_read boolean NOT NULL DEFAULT true,
  can_write boolean NOT NULL DEFAULT true,
  PRIMARY KEY (agent_id, namespace)
);
```

---

## 4. Core logic

### 4.1 `retain` — the write pipeline (do not insert raw)

On every write:
1. Embed `content` (TEI → Qwen3-Embedding-0.6B, 1024-dim).
2. `recall` the top-k similar existing memories in the same namespace.
3. Ask a **small** model (via `togo-framework/ai`, cheap model) to classify the write as **ADD / UPDATE / INVALIDATE / NOOP** against those neighbors (Mem0-style write decision — this is what keeps the store clean).
4. Compute `importance` at write time from: novelty (distance to nearest neighbor), explicit flag, `source_kind` weight, and reference recency. **This is the highest-leverage field — do not leave it at 0.5.**
5. Insert as `network='experience'`, `memory_type='episodic'`, `tier='hot'`. Populate the entity graph (Cognee).
6. Emit a `memory_events` row (`op='retain'`).

### 4.2 `recall` — the read path (must stay lean, N1)

```sql
-- hybrid retrieval: vector + BM25 fused with RRF, scoped, hot tier, no invalidated rows
WITH vec AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY embedding <=> $query_vec) AS r
  FROM memories
  WHERE namespace = $ns AND invalid_at IS NULL AND tier = 'hot'
  ORDER BY embedding <=> $query_vec LIMIT 40
),
txt AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY score DESC) AS r
  FROM (
    SELECT id, bm25_score(memories_bm25, $query_text) AS score
    FROM memories
    WHERE content @@@ $query_text AND namespace = $ns AND invalid_at IS NULL AND tier = 'hot'
    LIMIT 40
  ) t
)
SELECT m.*,
       COALESCE(1.0/(60+vec.r),0) + COALESCE(1.0/(60+txt.r),0)
       + 0.15 * m.importance                                   -- salience nudge
       AS score
FROM memories m
LEFT JOIN vec ON vec.id = m.id
LEFT JOIN txt ON txt.id = m.id
WHERE vec.id IS NOT NULL OR txt.id IS NOT NULL
ORDER BY score DESC LIMIT 20;
```

Then: **rerank** the top 20 → top N with `bge-reranker-v2-m3` (TEI), optionally **expand one hop** through `memory_entities` to pull associated memories (pattern completion + spreading activation), bump `access_count`/`last_accessed_at`, and emit `op='recall'`.

Cold-tier deep recall ("what did we know a year ago") is a **separate, explicit** tool call (`recall_archive`) that reads Iceberg/Parquet — never folded into the hot path.

### 4.3 `reflect` — sleep consolidation (Phase 2, scheduled)

Runs as a `togo-framework/scheduler` job as role `cabrain_sleep`. Never on the recall path.
- **Consolidate:** cluster recent episodics per namespace, summarize into `network='fact'`/`memory_type='semantic'` rows + update entity summaries (Cognee `reflect`/`improve`).
- **Dedup:** merge near-duplicates above a cosine threshold (pattern separation in reverse).
- **Decay:** lower `importance` over time, weighted so salient memories decay slowest.
- **Demote (tiering):** export old/invalidated partitions to Iceberg on R2/GCS via `data-iceberg`, set `tier='cold'`, drop the local hot rows. Hot set stays small → indexes stay fast.
- **Index maintenance.**

### 4.4 Reconsolidation on recall (F4)

When `recall` surfaces a fact that the current context contradicts, do **not** append a contradiction. Instead: create the corrected memory, set the old row's `invalid_at = now()` and `superseded_by` = new id. History stays queryable; the current answer is clean. This is the anti-rot mechanism — run it on recall, not only on write.

---

## 5. Consumer surface (F6)

### 5.1 MCP tools

Expose via the ToGO MCP server:

| Tool | Purpose |
|---|---|
| `memory_retain` | write a memory (runs §4.1) |
| `memory_recall` | hybrid retrieval (runs §4.2) |
| `memory_recall_archive` | explicit cold-tier deep recall |
| `memory_get` | fetch by id (+ provenance) |
| `memory_forget` | soft-invalidate (F, never hard-delete) |
| `memory_share` | grant a namespace to an agent |

Scoping is enforced in SQL via `namespace_grants`; when >1 agent connects, also enforce via the `ontology` permission graph.

### 5.2 Claude Code Memory Tool backend (the "never compact" front)

Anthropic's Memory Tool is **client-side** — Claude requests file operations, your app executes them. CaBrain implements that backend so a Claude Code session's memory calls land in `togo-postgres` instead of flat files:
- `view`/`read` memory path → `memory_recall` (scoped to the project namespace, hybrid + rerank).
- `write`/`str_replace` → `memory_retain`.
- Pair with server-side compaction: compaction keeps the active window small, CaBrain preserves the "why" (decisions, rejected approaches) that compaction drops.

Borrow the proven hook pattern from `claude-mem`/Supermemory (both permissively licensed): proactively compact *before* degradation (~80%), project-scoped tags, `<private>` redaction, auto-continue.

---

## 6. Capture mode (F7 — this is what fills the brain)

The point of Phase 1 is not a clever demo; it is **memory mass**. Run every session through capture:
- A hook on the Claude Code consumer records each meaningful turn (decisions, file reads, rejected approaches, learnings) as a `retain` with `source_kind='claude_code'`, `source_ref=<session id>`.
- Redact anything in `<private>` tags before storage.
- No consolidation yet (that's Phase 2) — just accumulate high-fidelity episodics.
- Run it across all your active workstreams (Sentra, FreshUp, Orchestra, ToGO itself) so the brain sees real, varied signal.

**You cannot evaluate recall quality until the brain has captured real sessions.** Capture first, tune later.

---

## 7. Execution plan (phase gates)

### Phase 1 — the brain + capture  ← *ship this first*
1. **Infra first.** Confirm the infra agent's outputs are available (see `INFRA-CaBrain.md`) and its acceptance checks pass — DB reachable with the required extensions present, TEI returns an embedding, Cognee `/health` responds, cold store writable. Load the outputs (§1.5) as env. **Do not provision infra in this phase.**
2. **Project + plugin scaffold (done).** `cabrain` togo app (harness, `--db togo-postgres`) hosts the
   **`brain`** plugin (`togo make:plugin`), wired via `require`+`replace`. Config (DB, TEI, Cognee,
   extraction LLM, cold store, Redis) comes from `.env`/`togo.yaml`, never hard-coded (§1.5).
3. **Schema in the plugin (done).** `brain` embeds the §3 DDL (`internal/brain/schema.sql`) and applies
   it with `Migrate` — the togo plugin-schema convention (§0.1), not the app-level sqlc/atlas
   `make:resource` flow (which can't carry `vector`/BM25/partitioning). Queries are hand-written pgx.
4. **Providers as plugins.** Stand up **`brain-tei`** (`Embedder`+`Reranker`) and **`brain-cognee`**
   (`Engine`) as driver plugins registered on `brain` (§0.1). Then implement `retain` (§4.1) and
   `recall` (§4.2 incl. RRF + rerank + 1-hop expansion) against them, with the **Redis L1 cache**
   (§2.1) on the read path. Benchmark `Qwen3-Embedding-0.6B` vs the `BGE-M3` fallback on real Arabic
   pairs — embedding dimension is a one-time choice (locked to 1024 by infra pending that benchmark).
5. Expose MCP tools (§5.1) + the Claude Code Memory Tool backend (§5.2).
6. Wire capture mode (§6) to one Claude Code consumer.

**Gate 1 (must pass):** With capture running on a real project, a fresh Claude Code session starts, calls `memory_recall` for "what did we decide about X", and gets back the actual prior decision *with provenance* — and the session runs past the point where it would previously have compacted, without losing the "why". Measure p95 recall latency < 300 ms.

### Phase 2 — the sleep loop
8. Implement `reflect` (§4.3) + reconsolidation (§4.4) as `scheduler` jobs under role `cabrain_sleep`.
9. Add salience computation refinement + decay.
10. Add cold-tier demotion via `data-iceberg` and `memory_recall_archive`.

**Gate 2:** After a "sleep" run, episodics from many sessions have collapsed into semantic facts + entity summaries; a contradicted fact is correctly superseded (not duplicated); the hot tier shrank while cold recall still returns the old raw event on explicit request.

### Phase 3 — the fleet seams
11. `togo install` `providers`, `omnigent` (impl), `coder` (exec), `autopilot`, `ontology`, `ai-agentops`, `ai-gateway`.
12. Give each Omnigent sub-agent the same MCP tool surface; enforce namespace scoping via `ontology`. Feed Coder run transcripts into `retain`.

**Gate 3:** An Autopilot issue is implemented by an Omnigent-orchestrated, Coder-isolated agent that was *fed relevant memory from CaBrain at the start* and *wrote its learnings back at the end* — and the next issue benefits. `ai-agentops` shows per-agent token/cost.

### Phase 4 — the interfaces
13. `togo install` `live`, `live-whatsapp`, `live-notify`.
14. Add live channels as a `retain` source (conversation capture). Implement the **salience-gated proactive push**: a sleep worker that, when a wake-worthy memory event fires (contradiction, stale-after-refactor, high-importance pattern), pushes through `live-notify` to Slack/WhatsApp/web-push.
15. Harden the live agent as the most constrained persona: read-scoped recall + issue-filing only; **no deploy/delete/spend without human confirmation**, enforced via Omnigent policies + `ontology`, never via prompt.

**Gate 4:** From WhatsApp, you brief the org ("the RTL layout breaks on the feed page"); the live agent recalls context, files an issue, the fleet executes, and the PR link is pushed back to the same thread. Separately, the brain proactively pings you about a real contradiction it found during sleep.

---

## 8. Trade-offs & what to revisit

- **Wrapping Cognee vs building direct:** wrapping buys the proven cognify/graph pipeline fast; the cost is a heavier dependency. Mitigation: the schema and tool contracts here are engine-agnostic, so a direct implementation on the same tables is a fallback, not a rewrite. **Revisit** if Cognee's write latency threatens N1 or its graph build dominates cost.
- **VectorChord vs pgvector/ParadeDB:** chosen for the Arabic-capable BM25 tokenizer, native (non-planner-hooking) operators that coexist with `pg_duckdb`, and off-box index build for horizontal scale. **Revisit** the tokenizer choice after benchmarking real Arabic recall; ParadeDB remains correct for Sentra's separate search product, not for the brain.
- **Local embeddings (RTX 3060) vs API:** zero per-token cost + full data control + Arabic strength, at the cost of running TEI. **Revisit** the model (`Qwen3` vs `BGE-M3`) only after benchmarking on a few hundred real Sentra/Orchestra Arabic memory pairs. Note: **changing embedding dimension later means re-embedding everything** — pick once.
- **Salience as a static default:** the biggest quality risk if left flat. **Revisit** the write-time importance formula continuously; it gates both what gets consolidated first and what decays slowest.
- **Unlimited retention vs cost:** "never delete" is real via the cold tier, but object-store + a growing fleet of frontier-model agents is not free. `ai-gateway` spend caps + `ai-agentops` metering are load-bearing from Phase 3, not optional.
- **Plugin decomposition (`brain` + `brain-*`) vs one plugin:** splitting each provider into its own
  plugin (§0.1) buys independent shipping, engine-agnosticism (swap = plugin swap), and clean OSS
  boundaries — at the cost of more repos and an interface seam per provider. Chosen because it matches
  togo's driver-plugin grain and makes the Cognee fallback (§8, first bullet) a real swap. **Revisit**
  only if the seam overhead outweighs the modularity (it won't for the providers named).
- **Redis L1 cache vs PG-only:** the cache (§2.1) protects N1 and cuts embedding calls, but adds an
  invalidation surface — a stale entry after `retain`/`reconsolidate`/`forget` would serve outdated
  recall. Mitigation: PG stays authoritative, TTLs short, explicit invalidation on write. **Revisit**
  the `redis_fdw` ("pg_redis") option only if a SQL-side join to cached data is needed; the app-level
  cache is simpler and sufficient for Phase 1.
- **Shared brain vs leakage:** the security boundary is `namespace` + `visibility` + `namespace_grants` + `ontology`. This is what stands between "one brain for the org" and "one agent leaking another's secrets." Treat scoping bugs as Sev-1.

---

## 9. One-line summary

**Build CaBrain as the `cabrain` project of togo plugins — the `brain` organ plugin on `togo-postgres` (VectorChord + BM25 + pgvector) with a Redis L1 cache, hippocampal hot / cortical cold tiers, salience-gated sleep consolidation, and reconsolidation-on-recall, plus one provider plugin per dependency (`brain-tei`, `brain-cognee`, `brain-cold-*`) — expose it as MCP tools + a Claude Code Memory Tool backend, run it in capture mode across every session to build memory mass, and only then attach the fleet (Omnigent/Coder/Autopilot) and the interfaces (live/WhatsApp) as new mouths on the same brain.**
