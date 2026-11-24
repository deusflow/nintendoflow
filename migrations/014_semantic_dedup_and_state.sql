-- 014_semantic_dedup_and_state.sql
-- Adds multi-layer semantic dedup fields, editorial category/franchise metadata,
-- generic bot_state table (for feed health monitoring & run metrics),
-- and ai_translation_cache table.

ALTER TABLE articles ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN IF NOT EXISTS franchise_tag TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN IF NOT EXISTS title_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN IF NOT EXISTS story_cluster_key TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN IF NOT EXISTS concrete_anchor TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN IF NOT EXISTS audience_score INT NOT NULL DEFAULT 0;
ALTER TABLE articles ADD COLUMN IF NOT EXISTS embedding JSONB;

CREATE INDEX IF NOT EXISTS idx_articles_title_norm ON articles(title_norm) WHERE title_norm <> '';
CREATE INDEX IF NOT EXISTS idx_articles_cluster_key ON articles(story_cluster_key) WHERE story_cluster_key <> '';
CREATE INDEX IF NOT EXISTS idx_articles_category_created ON articles(category, created_at DESC);

CREATE TABLE IF NOT EXISTS bot_state (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ai_translation_cache (
    url_hash      TEXT PRIMARY KEY,
    payload_json  TEXT NOT NULL,
    provider      TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ai_cache_created_at ON ai_translation_cache(created_at DESC);
