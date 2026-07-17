-- CaBrain BM25 layer — vchord_bm25 0.3.0 + pg_tokenizer 0.1.1 (CONFIRMED against
-- the live cabrain DB). Kept separate from schema.sql so a BM25 issue can never
-- block the core schema. Applied by ApplyBM25 (idempotent).
--
-- TWO hard-won facts from the live DB:
--   1. The TOKENIZER is created by infra, not here. The app role (`cabrain`) has
--      SELECT on tokenizer_catalog but not INSERT, so it cannot create_tokenizer.
--      Infra provides a multilingual tokenizer (unicode_segmentation); its name is
--      configurable via BRAIN_BM25_TOKENIZER (default 'cabrain_bm25_tok'). See
--      infra/grant-bm25.sql for provisioning a production tokenizer (llmlingua2).
--   2. `memories` is PARTITIONED. A bm25 index on the partitioned PARENT is an empty
--      template relation, and to_bm25query('<parent index>', …) fails with
--      "could not open file" because the rows live in the child partition. So the
--      bm25 index is created on the DEFAULT PARTITION (memories_default) and recall
--      references it by that concrete name. Phase 2 (partman monthly partitions)
--      must add a bm25 index per new partition + union them in recall.

SET search_path TO public, bm25_catalog, tokenizer_catalog;

-- 1. BM25 vector column on the partitioned parent (propagates to all partitions).
ALTER TABLE memories ADD COLUMN IF NOT EXISTS content_bm25 bm25_catalog.bm25vector;

-- 2. Drop any bm25 index on the PARENT (empty template that breaks to_bm25query),
--    then build it on the concrete default partition.
DROP INDEX IF EXISTS memories_bm25;
CREATE INDEX IF NOT EXISTS memories_default_bm25
  ON memories_default USING bm25 (content_bm25 bm25_catalog.bm25_ops);
