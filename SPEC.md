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

---

## 2. High-level architecture

```
                 consumers (Phase 1: Claude Code only)
                 Memory Tool  ·  MCP tools
                          │
        ┌─────────────────▼──────────────────┐
        │  CaBrain plugin (ToGO)              │
        │  ── API: retain · recall · reflect  │
        │  ── forget · improve                │
        │  ── Cognee engine (wrapped)         │
        │  ── salience · reconsolidation      │
        └───────┬───────────────────┬─────────┘
      writes    │                   │  reads
        ┌───────▼──────┐    ┌───────▼────────┐
        │  HOT tier     │    │ Embedding/rerank│
        │  togo-postgres│    │ TEI on RTX 3060 │
        │  VectorChord  │    │ Qwen3-Embedding │
        │  + vchord_bm25│    │ + bge-reranker  │
        └───────┬───────┘    └────────────────┘
                │ sleep (scheduler workers)
        ┌───────▼───────────────────┐
        │  COLD tier                 │
        │  Iceberg/Parquet on R2/GCS │
        │  via data-iceberg/pg_duckdb│
        └────────────────────────────┘
```

**Memory subsystems (the brain map):**
- **Working memory** = the consumer's context window + the MCP gateway (central executive). Not stored by CaBrain; it decides what to page in.
- **Hippocampus (hot tier)** = fresh, individually-stored episodic memories in `togo-postgres`, VectorChord-indexed. Small and fast.
- **Neocortex (cold tier)** = consolidated semantic facts + entity summaries; raw episodics demoted to Iceberg. Unbounded.
- **Amygdala** = `importance` score computed at write time, gating consolidation priority and decay rate.
- **Sleep** = `scheduler` workers doing consolidation, dedup, decay, tiering, index maintenance — always offline, never on the recall path.

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
2. `togo new cabrain` (or add to an existing togo app), database = `togo-postgres`, pointed at `CABRAIN_DATABASE_URL`. Wire embeddings/rerank to `TEI_EMBEDDINGS_URL` / `TEI_RERANKER_URL`, the engine to `COGNEE_API_URL`, the extraction LLM to `EXTRACTION_LLM_*`, and the cold tier to `COLD_STORE_*`.
3. `togo make:plugin cabrain`. Generate the `memories`, `entities`, `memory_entities`, `memory_events`, `namespace_grants` resources → sqlc + Atlas + REST/GraphQL.
4. Implement `retain` (§4.1) and `recall` (§4.2 incl. RRF + rerank + 1-hop expansion), wrapping Cognee as the engine. Benchmark `Qwen3-Embedding-0.6B` vs the `BGE-M3` fallback on real Arabic pairs — embedding dimension is a one-time choice.
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
- **Shared brain vs leakage:** the security boundary is `namespace` + `visibility` + `namespace_grants` + `ontology`. This is what stands between "one brain for the org" and "one agent leaking another's secrets." Treat scoping bugs as Sev-1.

---

## 9. One-line summary

**Build CaBrain as a ToGO plugin wrapping Cognee on `togo-postgres` (VectorChord + BM25 + pgvector), with hippocampal hot / cortical cold tiers, salience-gated sleep consolidation, and reconsolidation-on-recall — expose it as MCP tools + a Claude Code Memory Tool backend, run it in capture mode across every session to build memory mass, and only then attach the fleet (Omnigent/Coder/Autopilot) and the interfaces (live/WhatsApp) as new mouths on the same brain.**
