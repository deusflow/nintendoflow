CREATE TABLE IF NOT EXISTS articles (
    id           SERIAL PRIMARY KEY,
    source_url   TEXT UNIQUE NOT NULL,
    url_hash     TEXT,
    title_hash   TEXT,
    content_hash TEXT NOT NULL,
    title_raw    TEXT NOT NULL,
    title_ua     TEXT,
    body_ua      TEXT,
    image_url    TEXT,
    source_name  TEXT NOT NULL,
    source_type  TEXT NOT NULL DEFAULT 'media',
    score        INT DEFAULT 0,
    posted_tg    BOOLEAN DEFAULT FALSE,
    status       TEXT NOT NULL DEFAULT 'pending',
    ai_provider  TEXT,
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_articles_posted_tg    ON articles(posted_tg);
CREATE INDEX IF NOT EXISTS idx_articles_status       ON articles(status);
CREATE INDEX IF NOT EXISTS idx_articles_created_at   ON articles(created_at);
CREATE INDEX IF NOT EXISTS idx_articles_content_hash ON articles(content_hash);
CREATE INDEX IF NOT EXISTS idx_articles_score        ON articles(score DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_articles_url_hash_unique ON articles(url_hash) WHERE url_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_articles_title_hash ON articles(title_hash);

