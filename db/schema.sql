-- CaBrain data model — SPEC §3
-- =====================================================================================
-- Source of truth for the schema. Once ToGO is wired, `togo make:plugin cabrain` +
-- sqlc/Atlas will own the generated migrations; this file is what they reconcile
-- against, AND it is the direct-on-Postgres fallback (SPEC §8) if Cognee is dropped.
--
-- Deviations from the *literal* SPEC §3 DDL, and WHY (the spec DDL does not run as-is):
--   [D1] `memories` is PARTITION BY RANGE (valid_at). Postgres requires every UNIQUE/PK
--        constraint on a partitioned table to include the partition key. So the primary
--        key is (id, valid_at), not (id). `id` alone stays UNIQUE-per-partition in
--        practice (gen_random_uuid), and app code keys on `id`.
--   [D2] Because the PK is composite, single-column foreign keys that reference
--        memories(id) are illegal. `superseded_by`, `memory_entities.memory_id`, and
--        `memory_events.memory_id` are therefore plain uuid columns (soft references),
--        enforced in application logic, not by the DB. This is normal for partitioned
--        fact tables and does not weaken scoping (which is on namespace, not FKs).
--   [V1] The BM25 index + multilingual tokenizer syntax is vchord_bm25/pg_tokenizer
--        VERSION-SENSITIVE. The block below is the intended shape; verify the exact
--        API against the installed extensions before first migrate (see NOTE V1).
--   [V2] partman.create_parent signature changed in pg_partman v5. The v5 call is used;
--        the v4 positional form is left commented (see NOTE V2).
-- =====================================================================================

-- ── 3.1 Extensions & roles ───────────────────────────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS vchord        CASCADE;  -- vector index (off-box build, N3)
CREATE EXTENSION IF NOT EXISTS vchord_bm25   CASCADE;  -- BM25 long-text
CREATE EXTENSION IF NOT EXISTS pg_tokenizer  CASCADE;  -- multilingual tokenizer (Arabic, N4)
CREATE EXTENSION IF NOT EXISTS pg_partman    CASCADE;  -- time partitioning

-- Consolidation / sleep plane runs as its own login role so it never shares a
-- connection pool with the latency-critical recall path (N1). Infra already created
-- this role on the live DB; keep IF NOT EXISTS so the migration is idempotent.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cabrain_sleep') THEN
    CREATE ROLE cabrain_sleep LOGIN;
  END IF;
END $$;
-- pg_duckdb, if/when added for telemetry, is enabled per-role here — never globally.

-- ── 3.2 Core table: memories ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS memories (
  id               uuid        NOT NULL DEFAULT gen_random_uuid(),

  -- scoping (F5): every read/write is filtered by these
  namespace        text        NOT NULL,                    -- 'sentra' | 'freshup' | 'orchestra' | ...
  owner_agent_id   text,                                    -- which agent/persona wrote it
  visibility       text        NOT NULL DEFAULT 'private',  -- private | team | global

  -- brain-map classification
  network          text        NOT NULL,                    -- fact | experience | observation | belief
  memory_type      text        NOT NULL DEFAULT 'episodic', -- episodic | semantic | procedural | working
  content          text        NOT NULL,                    -- raw or distilled text
  source_kind      text,                                    -- claude_code | coder_run | whatsapp | slack | chat | manual
  source_ref       text,                                    -- session/thread/run id (provenance)

  -- retrieval
  embedding        vector(1024),                            -- Qwen3-Embedding-0.6B, 1024-dim (locked by infra)

  -- amygdala (salience)
  importance       real        NOT NULL DEFAULT 0.5,        -- [0,1]; gates consolidation + decay
  access_count     int         NOT NULL DEFAULT 0,
  last_accessed_at timestamptz,

  -- temporal / reconsolidation (never hard-delete)
  valid_at         timestamptz NOT NULL DEFAULT now(),
  invalid_at       timestamptz,                             -- set on reconsolidation; row stays queryable
  superseded_by    uuid,                                    -- [D2] soft ref to the replacement memory's id

  -- tiering
  tier             text        NOT NULL DEFAULT 'hot',      -- hot | cold (demoted to Iceberg)

  metadata         jsonb       NOT NULL DEFAULT '{}',

  -- [D1] partition key MUST be in the PK
  PRIMARY KEY (id, valid_at),

  CONSTRAINT memories_visibility_chk  CHECK (visibility  IN ('private','team','global')),
  CONSTRAINT memories_tier_chk        CHECK (tier        IN ('hot','cold')),
  CONSTRAINT memories_network_chk     CHECK (network     IN ('fact','experience','observation','belief')),
  CONSTRAINT memories_memtype_chk     CHECK (memory_type IN ('episodic','semantic','procedural','working')),
  CONSTRAINT memories_importance_chk  CHECK (importance >= 0.0 AND importance <= 1.0)
) PARTITION BY RANGE (valid_at);

-- [V2] pg_partman v5: monthly native partitions; old/invalidated partitions are the
-- demotion unit for the cold tier. Verify partman version before running.
SELECT partman.create_parent(
  p_parent_table    => 'public.memories',
  p_control         => 'valid_at',
  p_interval        => '1 month',
  p_type            => 'range'
);
-- v4 fallback (if the installed partman is < 5.0):
--   SELECT partman.create_parent('public.memories', 'valid_at', 'native', 'monthly');

-- Hybrid retrieval indexes (created on the partitioned parent → propagate to partitions).
--
-- Dense vector: pgvector HNSW is the working default. For the SPEC's off-box index build
-- + horizontal scale (N3), swap to VectorChord's vchordrq index once the hot set is large:
--   CREATE INDEX memories_vec ON memories USING vchordrq (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS memories_vec
  ON memories USING hnsw (embedding vector_cosine_ops);

-- [V1] BM25 long-text, Arabic-capable. vchord_bm25 + pg_tokenizer wiring is version
-- sensitive. Intended shape (SPEC §3.2):
--   CREATE INDEX memories_bm25 ON memories USING bm25 (id, content)
--     WITH (tokenizer='multilingual');
-- Newer vchord_bm25 requires an explicit tokenizer/model first, e.g.:
--   SELECT create_tokenizer('multilingual', $$ pre_tokenizer = "icu" $$);   -- Arabic via ICU
--   ALTER TABLE memories ADD COLUMN content_bm25 bm25vector;                  -- generated from content
--   CREATE INDEX memories_bm25 ON memories USING bm25 (content_bm25 bm25_ops);
-- ACTION: confirm the exact API on the installed vchord_bm25 before first migrate, then
-- pin ONE form here. Must NOT fall back to the default English tokenizer (N4).

CREATE INDEX IF NOT EXISTS memories_ns
  ON memories (namespace, tier) WHERE invalid_at IS NULL;   -- hot scoped scans
CREATE INDEX IF NOT EXISTS memories_sal
  ON memories (importance DESC, last_accessed_at);          -- salience / decay ordering

-- ── 3.3 Supporting tables ────────────────────────────────────────────────────────────

-- Entity graph (spreading activation on recall; Cognee owns population). Not partitioned,
-- so a normal single-column PK + real FKs are fine here.
CREATE TABLE IF NOT EXISTS entities (
  id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  namespace text NOT NULL,
  name      text NOT NULL,
  summary   text,                                           -- consolidated "what we know about X"
  embedding vector(1024),
  UNIQUE (namespace, name)
);

-- memory ↔ entity edges. memory_id is a soft ref [D2] (memories is partitioned);
-- entity_id is a real FK. Orphan-edge cleanup for demoted/removed memories is handled
-- by the sleep workers, not ON DELETE CASCADE.
CREATE TABLE IF NOT EXISTS memory_entities (
  memory_id uuid NOT NULL,                                  -- [D2] -> memories.id (soft)
  entity_id uuid NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
  PRIMARY KEY (memory_id, entity_id)
);
CREATE INDEX IF NOT EXISTS memory_entities_entity ON memory_entities (entity_id);

-- Append-only telemetry (OLAP; consolidation candidates, usage, cost).
CREATE TABLE IF NOT EXISTS memory_events (
  id         bigserial PRIMARY KEY,
  ts         timestamptz NOT NULL DEFAULT now(),
  namespace  text,
  op         text NOT NULL,                                 -- retain|recall|reflect|forget|reconsolidate|demote
  memory_id  uuid,                                          -- [D2] soft ref
  agent_id   text,
  latency_ms int,
  metadata   jsonb NOT NULL DEFAULT '{}',
  CONSTRAINT memory_events_op_chk
    CHECK (op IN ('retain','recall','recall_archive','reflect','forget','reconsolidate','demote','share'))
);
CREATE INDEX IF NOT EXISTS memory_events_ns_ts ON memory_events (namespace, ts DESC);

-- Namespace access grants (the claim/share model; enforced in SQL, F5).
CREATE TABLE IF NOT EXISTS namespace_grants (
  agent_id  text NOT NULL,
  namespace text NOT NULL,
  can_read  boolean NOT NULL DEFAULT true,
  can_write boolean NOT NULL DEFAULT true,
  PRIMARY KEY (agent_id, namespace)
);
