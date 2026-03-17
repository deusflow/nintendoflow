ALTER TABLE articles ADD COLUMN IF NOT EXISTS status TEXT;

UPDATE articles
SET status = 'published'
WHERE posted_tg = TRUE AND (status IS NULL OR status = '');

UPDATE articles
SET status = 'pending'
WHERE posted_tg = FALSE AND (status IS NULL OR status = '');

ALTER TABLE articles ALTER COLUMN status SET DEFAULT 'pending';
ALTER TABLE articles ALTER COLUMN status SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_articles_status ON articles(status);

