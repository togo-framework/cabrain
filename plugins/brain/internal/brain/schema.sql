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
  embedding        vector(1024),                            -- BAAI/bge-m3, 1024-dim, multilingual (locked by infra; TEI 1.6)

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

-- [V2] Partitioning. The table stays PARTITION BY RANGE (valid_at); a DEFAULT
-- partition catches all rows so writes work immediately with no partman dependency.
CREATE TABLE IF NOT EXISTS memories_default PARTITION OF memories DEFAULT;
--
-- pg_partman 5.4.3 monthly rollover + retention (the cold-tier demotion unit, Phase 2)
-- is added later — it needs the `cabrain` role granted on partman's config tables
-- (part_config / part_config_sub), which only a superuser/the partman installer can do
-- (INFRA TODO). Then, to split the default into monthly partitions:
--   SELECT public.create_parent(p_parent_table => 'public.memories',
--     p_control => 'valid_at', p_interval => '1 month', p_type => 'range');

-- Hybrid retrieval indexes (created on the partitioned parent → propagate to partitions).
--
-- Dense vector: pgvector HNSW is the working default. For the SPEC's off-box index build
-- + horizontal scale (N3), swap to VectorChord's vchordrq index once the hot set is large:
--   CREATE INDEX memories_vec ON memories USING vchordrq (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS memories_vec
  ON memories USING hnsw (embedding vector_cosine_ops);

-- [V1] BM25 long-text, Arabic-capable — CONFIRMED API for vchord_bm25 0.3.0 +
-- pg_tokenizer 0.1.1 on the live cabrain DB. Applied by bm25.sql (separate, so a
-- tokenizer-config issue never blocks the core schema), and populated on the retain
-- path (content_bm25 = tokenize(content, 'cabrain_ml')). Shape:
--   SELECT create_tokenizer('cabrain_ml', $$ model = "llmlingua2" $$);  -- see infra/grant-bm25.sql (superuser)
--   ALTER TABLE memories ADD COLUMN content_bm25 bm25vector;   -- populated per-write via tokenize()
--   CREATE INDEX memories_bm25 ON memories USING bm25 (content_bm25 bm25_ops);
-- Recall ranks with:  content_bm25 <&> to_bm25query('memories_bm25', tokenize($q,'cabrain_ml'))
-- (lower = better). Verified multilingual/Arabic by infra §5.2. NOT the English tokenizer (N4).

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
    CHECK (op IN ('retain','recall','recall_archive','reflect','forget','reconsolidate','demote','share','search'))
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

-- Knowledge gaps: every recall that comes back EMPTY is a question the brain
-- couldn't answer. We record it (deduped, counted) so the operator can act on it —
-- index the missing knowledge — over MCP / chat / the dashboard.
CREATE TABLE IF NOT EXISTS memory_gaps (
  id          bigserial PRIMARY KEY,
  namespace   text NOT NULL,
  query       text NOT NULL,                 -- the original query text
  norm_query  text NOT NULL,                 -- normalized, for dedup
  hits        int  NOT NULL DEFAULT 1,        -- how many times it's been asked
  status      text NOT NULL DEFAULT 'open',   -- open | indexed | dismissed
  resolution  text,                           -- note when resolved
  first_seen  timestamptz NOT NULL DEFAULT now(),
  last_seen   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (namespace, norm_query),
  CONSTRAINT memory_gaps_status_chk CHECK (status IN ('open','indexed','dismissed'))
);
CREATE INDEX IF NOT EXISTS memory_gaps_status ON memory_gaps (status, last_seen DESC);

-- Access tokens (ACL). A token identifies a caller (agent_id); its brain access is
-- namespace_grants(agent_id, namespace, can_read, can_write). Admin tokens bypass
-- grants. Presented over MCP/REST as the X-Cabrain-Token header. Enforcement is on
-- when CABRAIN_REQUIRE_TOKEN=1 (else a tokenless caller is the trusted local console).
CREATE TABLE IF NOT EXISTS brain_tokens (
  token        text PRIMARY KEY,
  agent_id     text NOT NULL,
  label        text,
  is_admin     boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at   timestamptz
);
CREATE INDEX IF NOT EXISTS brain_tokens_agent ON brain_tokens (agent_id);

-- Per-brain secrets vault. Secrets found while retaining (API keys, passwords,
-- .env values, connection strings, private keys) are moved OUT of memories.content
-- into here, encrypted (AES-256-GCM; nonce||ciphertext in value_enc), and the
-- content is redacted to a `[secret:<name>]` reference so raw values never enter
-- the vector index or a recall response. Reveal is ACL-gated (write/admin on the
-- brain). Namespace-scoped like every other brain object. On the live instance this
-- table resolves under the isolated `cabrain_auth` schema via search_path.
CREATE TABLE IF NOT EXISTS secrets (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  namespace   text NOT NULL,
  name        text NOT NULL,
  value_enc   bytea NOT NULL,                 -- AES-256-GCM (nonce||ciphertext)
  hint        text,                           -- non-reversible masked preview
  kind        text,                           -- api_key|password|env|token|private_key|connection_string|credential
  source_ref  text,                           -- memory id / session that introduced it
  created_by  text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (namespace, name)
);
CREATE INDEX IF NOT EXISTS secrets_ns ON secrets (namespace, name);

-- Data sources (connectors). A configured connector instance bound to a brain
-- (namespace). On sync the connector's Fetch() emits Documents which are chunked
-- and retained (same §4.1 write-decision as any retain). Built-in kinds: text,
-- markdown, crawler, github, sql, plus the push-only "webhook". config is free-form
-- per-kind JSON (url/repo/dsn/query/… and, for webhook, a shared `secret`). Secrets
-- inside config are redacted on read (see redactDatasourceSecrets).
--
-- SCHEMA PLACEMENT: pinned to `public` (same schema as `memories`), NOT bare. On the
-- live instance the app runs with search_path=cabrain_auth,public, and an UNQUALIFIED
-- `CREATE TABLE IF NOT EXISTS datasources` would land in cabrain_auth (first writable
-- schema in the path — that is exactly where `secrets` ended up). Worse, if a
-- public.datasources already exists, an unqualified IF NOT EXISTS still creates a
-- SECOND, empty cabrain_auth.datasources that then SHADOWS public in every unqualified
-- read (verified empirically). Pinning to public keeps datasources alongside memories
-- and makes this file idempotent under brainctl migrate regardless of search_path. The
-- app's own unqualified queries (ListDatasources, …) resolve to public since
-- cabrain_auth has no datasources table.
CREATE TABLE IF NOT EXISTS public.datasources (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  namespace    text NOT NULL,
  kind         text NOT NULL,                 -- text|markdown|crawler|github|sql|webhook
  name         text NOT NULL,
  config       jsonb NOT NULL DEFAULT '{}',
  status       text NOT NULL DEFAULT 'idle',  -- idle|syncing|ok|error
  cursor       text,                          -- incremental resume point
  last_error   text,
  doc_count    int NOT NULL DEFAULT 0,
  last_sync_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS datasources_ns ON public.datasources (namespace);
