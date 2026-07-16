-- Grant the CaBrain app role (cabrain) the privileges it needs to USE the
-- vchord_bm25 + pg_tokenizer stack. MUST be run as a superuser (or the extension
-- owner) on the cabrain database — the `cabrain` role owns the DB but NOT the
-- extension catalog schemas, so it cannot grant these to itself.
--
-- Symptom without this: `brainctl bm25` / `ApplyBM25` fail with
--   ERROR: permission denied for schema bm25_catalog   (SQLSTATE 42501)
-- and recall silently degrades to vector-only (no lexical BM25 fusion).
--
-- The infra §5.2 BM25 acceptance passed because it ran as the superuser; the
-- application connects as `cabrain`, which needs these grants.
--
--   psql "$CABRAIN_SUPERUSER_URL" -f infra/grant-bm25.sql
--     (or: docker exec -i pg psql -U postgres -d cabrain -f - < infra/grant-bm25.sql)

\set app_role cabrain

-- 1. Use the extension schemas (resolve the bm25vector type + functions).
GRANT USAGE ON SCHEMA bm25_catalog      TO :app_role;
GRANT USAGE ON SCHEMA tokenizer_catalog TO :app_role;

-- 2. Call the tokenizer + BM25 query functions (tokenize, to_bm25query, and
--    create_tokenizer for the one-time cabrain_ml setup).
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA tokenizer_catalog TO :app_role;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA bm25_catalog      TO :app_role;

-- 3. create_tokenizer / tokenize read + write pg_tokenizer's config tables. Grant
--    read on all, and write on the config tables so the app can (re)create the
--    'cabrain_ml' tokenizer. If your pg_tokenizer build stores tokenizers as a
--    global catalog only a superuser may write, instead run the tokenizer + column
--    + index DDL below AS the superuser once, and give the app only SELECT/USAGE.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES    IN SCHEMA tokenizer_catalog TO :app_role;
GRANT USAGE, SELECT                  ON ALL SEQUENCES  IN SCHEMA tokenizer_catalog TO :app_role;

-- 4. (Fallback) If you prefer to keep the app role read-only on the catalogs, run
--    the one-time BM25 setup here as the superuser, then the app only needs the
--    USAGE + EXECUTE grants above:
--
--   SET search_path TO public, bm25_catalog, tokenizer_catalog;
--   SELECT tokenizer_catalog.create_tokenizer('cabrain_ml',
--            $t$ pre_tokenizer = "unicode_segmentation" $t$);
--   ALTER TABLE memories ADD COLUMN IF NOT EXISTS content_bm25 bm25_catalog.bm25vector;
--   CREATE INDEX IF NOT EXISTS memories_bm25
--     ON memories USING bm25 (content_bm25 bm25_catalog.bm25_ops);

-- After this: `brainctl bm25 && brainctl bm25-test` should succeed as the app role.
