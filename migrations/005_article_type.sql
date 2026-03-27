ALTER TABLE articles
    ADD COLUMN IF NOT EXISTS article_type TEXT;

UPDATE articles
SET article_type = 'news'
WHERE article_type IS NULL OR article_type = '';

ALTER TABLE articles
    ALTER COLUMN article_type SET DEFAULT 'news';

ALTER TABLE articles
    ALTER COLUMN article_type SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_articles_article_type
    ON articles(article_type);

