-- CaBrain BM25 — infra provisioning for the app role (`cabrain`). Run as a
-- SUPERUSER (or the extension owner) on the cabrain database.
--
-- STATUS (verified 2026-07-17 against the live cabrain DB):
--   ✓ Grants below are APPLIED — `cabrain` can now USE vchord_bm25 + pg_tokenizer.
--   ✓ brainctl bm25 / bm25-test pass: content_bm25 column + memories_default_bm25
--     index build, and an Arabic BM25 query ranks the Arabic row at -0.7616.
--   ⚠ The tokenizer currently in use, `cabrain_bm25_tok`, is a FIXED-VOCAB PROBE
--     (a custom model built from a tiny sample table). It only tokenizes words in
--     that sample — general English/Arabic terms outside it produce empty tokens.
--     Provision a PRODUCTION multilingual tokenizer (step 3) for real recall.

\set app_role cabrain

-- 1. Use the extension schemas + call their functions (tokenize / to_bm25query /
--    the bm25 type + opclass). These are what the app role needs at runtime.
GRANT USAGE ON SCHEMA bm25_catalog      TO :app_role;
GRANT USAGE ON SCHEMA tokenizer_catalog TO :app_role;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA bm25_catalog      TO :app_role;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA tokenizer_catalog TO :app_role;

-- 2. Read the tokenizer/model catalog (tokenize() looks up the tokenizer by name).
--    The app role stays READ-ONLY on these — it does NOT create tokenizers.
GRANT SELECT ON ALL TABLES IN SCHEMA tokenizer_catalog TO :app_role;

-- 3. PRODUCTION TOKENIZER (superuser). `llmlingua2` is preloaded and multilingual
--    (list_preload_models() → {llmlingua2}). Create a general-purpose tokenizer and
--    point the app at it with BRAIN_BM25_TOKENIZER=cabrain_ml. Adjust the config to
--    your pg_tokenizer build's model API if needed:
--
--   SET search_path TO public, bm25_catalog, tokenizer_catalog;
--   SELECT tokenizer_catalog.create_text_analyzer('cabrain_ml_analyzer', $$
--     pre_tokenizer = "unicode_segmentation"
--   $$);
--   SELECT tokenizer_catalog.create_tokenizer('cabrain_ml', $$
--     model = "llmlingua2"
--     text_analyzer = "cabrain_ml_analyzer"
--   $$);
--   GRANT SELECT ON ALL TABLES IN SCHEMA tokenizer_catalog TO cabrain;  -- re-grant new rows
--
--   Then set BRAIN_BM25_TOKENIZER=cabrain_ml in the app env and re-run
--   `brainctl bm25-test` — "cluster pods" should now score English rows too.

-- NOTE on partitioning: the app builds the bm25 index on the DEFAULT PARTITION
-- (memories_default_bm25), because a bm25 index on the partitioned parent is an
-- empty template that breaks to_bm25query. Phase 2 (partman monthly partitions)
-- must add a bm25 index per new partition. No superuser action needed for that —
-- `cabrain` owns the tables.
