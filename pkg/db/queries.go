package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	StatusPending   = "pending"
	StatusPublished = "published"
	StatusRejected  = "rejected"
	StatusNeedsEdit = "needs_edit"
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
	VideoURL    string
	ImageURL    string
	SourceName  string
	SourceType  string
	ArticleType string
	Score       int
	PostedTG    bool
	AIProvider  string
	Status      string
	PublishedAt *time.Time
	CreatedAt   time.Time
}

type ModerationEditSession struct {
	ChatID           int64
	UserID           int64
	ArticleID        int
	PreviewMessageID int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// InsertArticle inserts a new article and returns its id.
func InsertArticle(ctx context.Context, db *sql.DB, a Article) (int, error) {
	status := a.Status
	if status == "" {
		status = StatusPending
	}
	var id int
	err := db.QueryRowContext(ctx, `
		INSERT INTO articles
			(source_url, url_hash, title_hash, content_hash, title_raw, video_url, image_url, source_name, source_type, article_type, score, status, published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (source_url) DO UPDATE
		SET score=GREATEST(articles.score, EXCLUDED.score),
			article_type=COALESCE(NULLIF(articles.article_type,''), EXCLUDED.article_type),
			status=CASE
				WHEN articles.status='published' THEN articles.status
				ELSE EXCLUDED.status
			END
		RETURNING id`,
		a.SourceURL, a.URLHash, a.TitleHash, a.ContentHash, a.TitleRaw, nullStr(a.VideoURL), nullStr(a.ImageURL),
		a.SourceName, a.SourceType, nullStr(a.ArticleType), a.Score, status, a.PublishedAt,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func GetArticleByID(ctx context.Context, db *sql.DB, id int) (Article, error) {
	var a Article
	err := db.QueryRowContext(ctx, `
		SELECT id, source_url, COALESCE(url_hash, ''), COALESCE(title_hash, ''), COALESCE(content_hash, ''), title_raw, COALESCE(title_ua, ''),
		       COALESCE(body_ua, ''), COALESCE(video_url, ''), COALESCE(image_url, ''), source_name, source_type, COALESCE(article_type, 'news'), score,
		       posted_tg, COALESCE(ai_provider, ''), COALESCE(status, ''), published_at, created_at
		FROM articles
		WHERE id=$1`, id).
		Scan(
			&a.ID,
			&a.SourceURL,
			&a.URLHash,
			&a.TitleHash,
			&a.ContentHash,
			&a.TitleRaw,
			&a.TitleUA,
			&a.BodyUA,
			&a.VideoURL,
			&a.ImageURL,
			&a.SourceName,
			&a.SourceType,
			&a.ArticleType,
			&a.Score,
			&a.PostedTG,
			&a.AIProvider,
			&a.Status,
			&a.PublishedAt,
			&a.CreatedAt,
		)
	if err != nil {
		return Article{}, err
	}
	return a, nil
}

func UpdateArticleStatus(ctx context.Context, db *sql.DB, id int, status string) error {
	switch status {
	case StatusPending, StatusPublished, StatusRejected, StatusNeedsEdit:
	default:
		return fmt.Errorf("invalid article status: %s", status)
	}
	_, err := db.ExecContext(ctx, `UPDATE articles SET status=$1 WHERE id=$2`, status, id)
	return err
}

func UpsertModerationEditSession(ctx context.Context, db *sql.DB, session ModerationEditSession) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM moderation_edit_sessions
		WHERE article_id=$1 AND NOT (chat_id=$2 AND user_id=$3)
	`, session.ArticleID, session.ChatID, session.UserID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO moderation_edit_sessions (chat_id, user_id, article_id, preview_message_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (chat_id, user_id)
		DO UPDATE SET
			article_id = EXCLUDED.article_id,
			preview_message_id = EXCLUDED.preview_message_id,
			updated_at = NOW()
	`, session.ChatID, session.UserID, session.ArticleID, session.PreviewMessageID); err != nil {
		return err
	}

	return tx.Commit()
}

func GetModerationEditSession(ctx context.Context, db *sql.DB, chatID, userID int64) (ModerationEditSession, error) {
	var session ModerationEditSession
	err := db.QueryRowContext(ctx, `
		SELECT chat_id, user_id, article_id, preview_message_id, created_at, updated_at
		FROM moderation_edit_sessions
		WHERE chat_id=$1 AND user_id=$2
	`, chatID, userID).Scan(
		&session.ChatID,
		&session.UserID,
		&session.ArticleID,
		&session.PreviewMessageID,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		return ModerationEditSession{}, err
	}
	return session, nil
}

func DeleteModerationEditSession(ctx context.Context, db *sql.DB, chatID, userID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM moderation_edit_sessions WHERE chat_id=$1 AND user_id=$2`, chatID, userID)
	return err
}

func DeleteModerationEditSessionsByArticle(ctx context.Context, db *sql.DB, articleID int) error {
	_, err := db.ExecContext(ctx, `DELETE FROM moderation_edit_sessions WHERE article_id=$1`, articleID)
	return err
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

// FetchRecentDedupTexts returns combined title/body texts for near-duplicate checks.
// Only pending/published rows are considered to avoid using rejected noise.
func FetchRecentDedupTexts(ctx context.Context, db *sql.DB, hours int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT title_raw, COALESCE(body_ua, '')
		FROM articles
		WHERE created_at > NOW() - ($1::int * INTERVAL '1 hour')
		  AND status IN ($2, $3)
		ORDER BY created_at DESC
		LIMIT 500`, hours, StatusPending, StatusPublished)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]string, 0, 128)
	for rows.Next() {
		var titleRaw, bodyUA string
		if err := rows.Scan(&titleRaw, &bodyUA); err != nil {
			return nil, err
		}
		result = append(result, titleRaw+"\n"+bodyUA)
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

func UpdateBodyUAOnly(ctx context.Context, db *sql.DB, id int, bodyUA string) error {
	_, err := db.ExecContext(ctx, `UPDATE articles SET body_ua=$1 WHERE id=$2`, bodyUA, id)
	return err
}

// MarkPosted sets posted_tg = true.
func MarkPosted(ctx context.Context, db *sql.DB, id int) error {
	_, err := db.ExecContext(ctx, `UPDATE articles SET posted_tg=TRUE, status=$2 WHERE id=$1`, id, StatusPublished)
	return err
}

// Cleanup deletes articles older than 30 days.
func Cleanup(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `DELETE FROM articles WHERE created_at < NOW() - INTERVAL '30 days'`)
	return err
}

// RunMigration applies versioned SQL files from the migrations directory.
func RunMigration(ctx context.Context, db *sql.DB) error {
	paths, err := filepath.Glob(filepath.Join("migrations", "*.sql"))
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no migration files found in %s", filepath.Join("migrations", "*.sql"))
	}
	sort.Strings(paths)

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}
		sqlText := strings.TrimSpace(string(raw))
		if sqlText == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("apply migration %s: %w", path, err)
		}
	}
	return nil
}

// ListPublishedArticles returns latest published articles for the web archive.
func ListPublishedArticles(ctx context.Context, db *sql.DB, limit, offset int) ([]Article, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, source_url, COALESCE(url_hash, ''), COALESCE(title_hash, ''), COALESCE(content_hash, ''), title_raw, COALESCE(title_ua, ''),
		       COALESCE(body_ua, ''), COALESCE(video_url, ''), COALESCE(image_url, ''), source_name, source_type, COALESCE(article_type, 'news'), score,
		       posted_tg, COALESCE(ai_provider, ''), COALESCE(status, ''), published_at, created_at
		FROM articles
		WHERE status=$1
		ORDER BY COALESCE(published_at, created_at) DESC, id DESC
		LIMIT $2 OFFSET $3`, StatusPublished, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles := make([]Article, 0, limit)
	for rows.Next() {
		var a Article
		if err := rows.Scan(
			&a.ID,
			&a.SourceURL,
			&a.URLHash,
			&a.TitleHash,
			&a.ContentHash,
			&a.TitleRaw,
			&a.TitleUA,
			&a.BodyUA,
			&a.VideoURL,
			&a.ImageURL,
			&a.SourceName,
			&a.SourceType,
			&a.ArticleType,
			&a.Score,
			&a.PostedTG,
			&a.AIProvider,
			&a.Status,
			&a.PublishedAt,
			&a.CreatedAt,
		); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return articles, nil
}

// CountPublishedArticles returns total published rows for pagination.
func CountPublishedArticles(ctx context.Context, db *sql.DB) (int, error) {
	var total int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM articles WHERE status=$1`, StatusPublished).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total, nil
}

// GetPublishedArticleByID returns one published article by id.
func GetPublishedArticleByID(ctx context.Context, db *sql.DB, id int) (Article, error) {
	var a Article
	err := db.QueryRowContext(ctx, `
		SELECT id, source_url, COALESCE(url_hash, ''), COALESCE(title_hash, ''), COALESCE(content_hash, ''), title_raw, COALESCE(title_ua, ''),
		       COALESCE(body_ua, ''), COALESCE(video_url, ''), COALESCE(image_url, ''), source_name, source_type, COALESCE(article_type, 'news'), score,
		       posted_tg, COALESCE(ai_provider, ''), COALESCE(status, ''), published_at, created_at
		FROM articles
		WHERE id=$1 AND status=$2`, id, StatusPublished).
		Scan(
			&a.ID,
			&a.SourceURL,
			&a.URLHash,
			&a.TitleHash,
			&a.ContentHash,
			&a.TitleRaw,
			&a.TitleUA,
			&a.BodyUA,
			&a.VideoURL,
			&a.ImageURL,
			&a.SourceName,
			&a.SourceType,
			&a.ArticleType,
			&a.Score,
			&a.PostedTG,
			&a.AIProvider,
			&a.Status,
			&a.PublishedAt,
			&a.CreatedAt,
		)
	if err != nil {
		return Article{}, err
	}
	return a, nil
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
