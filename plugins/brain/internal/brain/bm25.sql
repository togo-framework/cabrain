-- CaBrain BM25 layer — vchord_bm25 0.3.0 + pg_tokenizer 0.1.1 (CONFIRMED API).
-- Kept separate from schema.sql so a tokenizer-config problem can never block the
-- core schema. Applied by ApplyBM25 (idempotent) at migrate; populated on retain
-- (content_bm25 = tokenize(content,'cabrain_ml')) and fused into recall.
--
-- Multilingual (Arabic + Latin + CJK) via unicode_segmentation — infra acceptance
-- §5.2 verified an Arabic query matched only the Arabic row. This is NOT the English
-- tokenizer (SPEC N4).

-- vchord_bm25 puts its type/opclass in schema bm25_catalog and pg_tokenizer puts
-- its functions in tokenizer_catalog. They ARE on the role's default search_path,
-- but a batched DDL Exec doesn't always inherit it, so pin it here and additionally
-- schema-qualify the type — belt and suspenders, so ApplyBM25 never fails on an
-- "type bm25vector does not exist" lookup.
SET search_path TO public, bm25_catalog, tokenizer_catalog;

-- 1. Tokenizer. create_tokenizer errors if the name already exists, so swallow the
--    duplicate on re-run (there is no CREATE ... IF NOT EXISTS for tokenizers).
DO $$
BEGIN
  PERFORM tokenizer_catalog.create_tokenizer('cabrain_ml', $t$
pre_tokenizer = "unicode_segmentation"
$t$);
EXCEPTION WHEN OTHERS THEN
  -- already exists (or benign config re-declaration) — leave the existing tokenizer.
  NULL;
END $$;

-- 2. BM25 vector column on the partitioned parent (propagates to partitions).
ALTER TABLE memories ADD COLUMN IF NOT EXISTS content_bm25 bm25_catalog.bm25vector;

-- 3. BM25 index. Named 'memories_bm25' — to_bm25query references it by name.
CREATE INDEX IF NOT EXISTS memories_bm25
  ON memories USING bm25 (content_bm25 bm25_catalog.bm25_ops);
