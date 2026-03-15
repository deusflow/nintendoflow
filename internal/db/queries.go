package db

import (
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
func InsertArticle(db *sql.DB, a Article) (int, error) {
	var id int
	err := db.QueryRow(`
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

// RecentTitles returns titles created in the last N hours (for dedup).
func RecentTitles(db *sql.DB, hours int) ([]string, error) {
	rows, err := db.Query(`
		SELECT title_raw FROM articles
		WHERE created_at > NOW() - ($1::int * INTERVAL '1 hour')`, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var titles []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		titles = append(titles, t)
	}
	return titles, rows.Err()
}

// URLExists checks if a source_url is already in the DB.
func URLExists(db *sql.DB, sourceURL string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(1) FROM articles WHERE source_url=$1`, sourceURL).Scan(&count)
	return count > 0, err
}

// HashExists checks dedup by url_hash OR title_hash.
func HashExists(db *sql.DB, urlHash, titleHash string) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(1)
		FROM articles
		WHERE url_hash = $1 OR title_hash = $2`, urlHash, titleHash).Scan(&count)
	return count > 0, err
}

// TopUnposted returns the top N unposted articles ordered by score DESC.
func TopUnposted(db *sql.DB, limit int) ([]Article, error) {
	rows, err := db.Query(`
		SELECT id, source_url, title_raw, image_url, source_name, source_type, score
		FROM articles
		WHERE posted_tg = FALSE
		ORDER BY score DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		var imgURL sql.NullString
		if err := rows.Scan(&a.ID, &a.SourceURL, &a.TitleRaw, &imgURL, &a.SourceName, &a.SourceType, &a.Score); err != nil {
			return nil, err
		}
		if imgURL.Valid {
			a.ImageURL = imgURL.String
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// UpdateBodyUA sets body_ua and ai_provider after rewrite.
func UpdateBodyUA(db *sql.DB, id int, bodyUA, aiProvider string) error {
	_, err := db.Exec(`
		UPDATE articles SET body_ua=$1, ai_provider=$2 WHERE id=$3`,
		bodyUA, aiProvider, id)
	return err
}

// MarkPosted sets posted_tg = true.
func MarkPosted(db *sql.DB, id int) error {
	_, err := db.Exec(`UPDATE articles SET posted_tg=TRUE WHERE id=$1`, id)
	return err
}

// Cleanup deletes articles older than 30 days.
func Cleanup(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM articles WHERE created_at < NOW() - INTERVAL '30 days'`)
	return err
}

// RunMigration creates the articles table if not exists.
func RunMigration(db *sql.DB) error {
	_, err := db.Exec(`
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
