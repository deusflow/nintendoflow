package db

import (
	"context"
	"database/sql"
	"time"
)

type Article struct {
	ID          int
	SourceURL   string
	URLHash     string
	TitleHash   string
	ContentHash string
	TitleRaw    string
	TitleUA     string
	BodyUA      string
	ImageURL    string
	SourceName  string
	SourceType  string
	Score       int
	PostedTG    bool
	AIProvider  string
	PublishedAt *time.Time
	CreatedAt   time.Time
}

// InsertArticle inserts a new article and returns its id.
func InsertArticle(ctx context.Context, db *sql.DB, a Article) (int, error) {
	var id int
	err := db.QueryRowContext(ctx, `
		INSERT INTO articles
			(source_url, url_hash, title_hash, content_hash, title_raw, image_url, source_name, source_type, score, published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (source_url) DO UPDATE
		SET score=GREATEST(articles.score, EXCLUDED.score)
		RETURNING id`,
		a.SourceURL, a.URLHash, a.TitleHash, a.ContentHash, a.TitleRaw, nullStr(a.ImageURL),
		a.SourceName, a.SourceType, a.Score, a.PublishedAt,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func FetchRecentURLHashes(ctx context.Context, db *sql.DB, hours int) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT url_hash
		FROM articles
		WHERE created_at > NOW() - ($1::int * INTERVAL '1 hour')
		  AND url_hash IS NOT NULL
		  AND url_hash <> ''`, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]struct{})
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		result[h] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func FetchRecentTitleHashes(ctx context.Context, db *sql.DB, hours int) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT title_hash
		FROM articles
		WHERE created_at > NOW() - ($1::int * INTERVAL '1 hour')
		  AND title_hash IS NOT NULL
		  AND title_hash <> ''`, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]struct{})
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		result[h] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateBodyUA sets body_ua and ai_provider after rewrite.
func UpdateBodyUA(ctx context.Context, db *sql.DB, id int, bodyUA, aiProvider string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE articles SET body_ua=$1, ai_provider=$2 WHERE id=$3`,
		bodyUA, aiProvider, id)
	return err
}

// MarkPosted sets posted_tg = true.
func MarkPosted(ctx context.Context, db *sql.DB, id int) error {
	_, err := db.ExecContext(ctx, `UPDATE articles SET posted_tg=TRUE WHERE id=$1`, id)
	return err
}

// Cleanup deletes articles older than 30 days.
func Cleanup(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `DELETE FROM articles WHERE created_at < NOW() - INTERVAL '30 days'`)
	return err
}

// RunMigration creates the articles table if not exists.
func RunMigration(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
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
    ai_provider  TEXT,
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);
ALTER TABLE articles ADD COLUMN IF NOT EXISTS url_hash TEXT;
ALTER TABLE articles ADD COLUMN IF NOT EXISTS title_hash TEXT;
CREATE INDEX IF NOT EXISTS idx_articles_posted_tg    ON articles(posted_tg);
CREATE INDEX IF NOT EXISTS idx_articles_created_at   ON articles(created_at);
CREATE INDEX IF NOT EXISTS idx_articles_content_hash ON articles(content_hash);
CREATE INDEX IF NOT EXISTS idx_articles_score        ON articles(score DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_articles_url_hash_unique ON articles(url_hash) WHERE url_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_articles_title_hash ON articles(title_hash);
`)
	return err
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
