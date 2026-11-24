package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/dedup"
)

const (
	StatusPending         = "pending"
	StatusPublished       = "published"
	StatusRejected        = "rejected"
	StatusNeedsEdit       = "needs_edit"
	StatusQuarantined     = "quarantined"
	StatusQuarantineRetry = "quarantine_retry"
)

type Article struct {
	ID                 int
	SourceURL          string
	URLHash            string
	TitleHash          string
	ContentHash        string
	TitleRaw           string
	TitleUA            string
	BodyUA             string
	BodyThreads        string
	VideoURL           string
	ImageURL           string
	SourceName         string
	SourceType         string
	ArticleType        string
	Category           string
	FranchiseTag       string
	TitleNorm          string
	StoryClusterKey    string
	ConcreteAnchor     string
	AudienceScore      int
	Embedding          []float32
	Score              int
	PostedTG           bool
	PostedThreads      bool
	TgMessageID        int
	AIProvider         string
	Status             string
	EventTag           string
	PublishedAt        *time.Time
	QuarantineAttempts int
	CreatedAt          time.Time
}

type ModerationEditSession struct {
	ChatID           int64
	UserID           int64
	ArticleID        int
	PreviewMessageID int
	EditTarget       string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// InsertArticle inserts a new article and returns its id.
func InsertArticle(ctx context.Context, db *sql.DB, a Article) (int, error) {
	status := a.Status
	if status == "" {
		status = StatusPending
	}
	var embParam interface{}
	if len(a.Embedding) > 0 {
		if raw, err := json.Marshal(a.Embedding); err == nil {
			embParam = string(raw)
		}
	}
	var id int
	err := db.QueryRowContext(ctx, `
		INSERT INTO articles
			(source_url, url_hash, title_hash, content_hash, title_raw, video_url, image_url, source_name, source_type, article_type, score, status, published_at, event_tag, category, franchise_tag, title_norm, story_cluster_key, concrete_anchor, audience_score, embedding)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21::jsonb)
		ON CONFLICT (source_url) DO UPDATE
		SET score=GREATEST(articles.score, EXCLUDED.score),
			video_url=COALESCE(NULLIF(EXCLUDED.video_url,''), articles.video_url),
			image_url=COALESCE(NULLIF(EXCLUDED.image_url,''), articles.image_url),
			article_type=COALESCE(NULLIF(articles.article_type,''), EXCLUDED.article_type),
			category=COALESCE(NULLIF(EXCLUDED.category,''), articles.category),
			franchise_tag=COALESCE(NULLIF(EXCLUDED.franchise_tag,''), articles.franchise_tag),
			title_norm=COALESCE(NULLIF(EXCLUDED.title_norm,''), articles.title_norm),
			story_cluster_key=COALESCE(NULLIF(EXCLUDED.story_cluster_key,''), articles.story_cluster_key),
			concrete_anchor=COALESCE(NULLIF(EXCLUDED.concrete_anchor,''), articles.concrete_anchor),
			audience_score=GREATEST(articles.audience_score, EXCLUDED.audience_score),
			embedding=COALESCE(EXCLUDED.embedding, articles.embedding),
			status=CASE
				WHEN articles.status IN ('published', 'rejected') THEN articles.status
				ELSE EXCLUDED.status
			END,
			event_tag=COALESCE(NULLIF(articles.event_tag,''), EXCLUDED.event_tag)
		RETURNING id`,
		a.SourceURL, a.URLHash, a.TitleHash, a.ContentHash, a.TitleRaw, nullStr(a.VideoURL), nullStr(a.ImageURL),
		a.SourceName, a.SourceType, nullStr(a.ArticleType), a.Score, status, a.PublishedAt, nullStr(a.EventTag),
		a.Category, a.FranchiseTag, a.TitleNorm, a.StoryClusterKey, a.ConcreteAnchor, a.AudienceScore, embParam,
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
		       COALESCE(body_ua, ''), COALESCE(body_threads, ''), COALESCE(video_url, ''), COALESCE(image_url, ''), source_name, source_type, COALESCE(article_type, 'news'), score,
		       posted_tg, posted_threads, COALESCE(tg_message_id, 0), COALESCE(ai_provider, ''), COALESCE(status, ''), COALESCE(event_tag, ''), published_at, created_at
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
			&a.BodyThreads,
			&a.VideoURL,
			&a.ImageURL,
			&a.SourceName,
			&a.SourceType,
			&a.ArticleType,
			&a.Score,
			&a.PostedTG,
			&a.PostedThreads,
			&a.TgMessageID,
			&a.AIProvider,
			&a.Status,
			&a.EventTag,
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
	case StatusPending, StatusPublished, StatusRejected, StatusNeedsEdit, StatusQuarantined, StatusQuarantineRetry:
	default:
		return fmt.Errorf("invalid article status: %s", status)
	}
	_, err := db.ExecContext(ctx, `UPDATE articles SET status=$1 WHERE id=$2`, status, id)
	return err
}

// GetQuarantineAttempts returns the current quarantine attempts and status for a source URL.
func GetQuarantineAttempts(ctx context.Context, db *sql.DB, sourceURL string) (int, string, error) {
	if db == nil {
		return 0, "", nil
	}
	var attempts int
	var status string
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(quarantine_attempts, 0), COALESCE(status, '')
		FROM articles
		WHERE source_url = $1`, sourceURL).Scan(&attempts, &status)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return attempts, status, nil
}

// RecordQuarantineAttempt saves or updates a quarantined article and its retry attempts.
// If permanent is true, the status is set to StatusQuarantined.
// If permanent is false, the status is set to StatusQuarantineRetry so it can be retried in subsequent runs.
func RecordQuarantineAttempt(ctx context.Context, db *sql.DB, a Article, attempts int, permanent bool) error {
	if db == nil {
		return nil
	}
	status := StatusQuarantineRetry
	if permanent {
		status = StatusQuarantined
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO articles
			(source_url, url_hash, title_hash, content_hash, title_raw, video_url, image_url, source_name, source_type, score, status, published_at, quarantine_attempts)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (source_url) DO UPDATE
		SET quarantine_attempts = EXCLUDED.quarantine_attempts,
			status = EXCLUDED.status`,
		a.SourceURL, a.URLHash, a.TitleHash, a.ContentHash, a.TitleRaw,
		nullStr(a.VideoURL), nullStr(a.ImageURL), a.SourceName, a.SourceType,
		a.Score, status, a.PublishedAt, attempts,
	)
	return err
}

// InsertQuarantinedArticle stores an article placed into quarantine due to missing or unverifiable date.
func InsertQuarantinedArticle(ctx context.Context, db *sql.DB, a Article) (int, error) {
	if db == nil {
		return 0, nil
	}
	a.Status = StatusQuarantined
	a.QuarantineAttempts = 1
	err := RecordQuarantineAttempt(ctx, db, a, 1, true)
	return a.ID, err
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
		INSERT INTO moderation_edit_sessions (chat_id, user_id, article_id, preview_message_id, edit_target, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		ON CONFLICT (chat_id, user_id)
		DO UPDATE SET
			article_id = EXCLUDED.article_id,
			preview_message_id = EXCLUDED.preview_message_id,
			edit_target = EXCLUDED.edit_target,
			updated_at = NOW()
	`, session.ChatID, session.UserID, session.ArticleID, session.PreviewMessageID, session.EditTarget); err != nil {
		return err
	}

	return tx.Commit()
}

func GetModerationEditSession(ctx context.Context, db *sql.DB, chatID, userID int64) (ModerationEditSession, error) {
	var session ModerationEditSession
	err := db.QueryRowContext(ctx, `
		SELECT chat_id, user_id, article_id, preview_message_id, edit_target, created_at, updated_at
		FROM moderation_edit_sessions
		WHERE chat_id=$1 AND user_id=$2
	`, chatID, userID).Scan(
		&session.ChatID,
		&session.UserID,
		&session.ArticleID,
		&session.PreviewMessageID,
		&session.EditTarget,
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

// CleanupExpiredModerationEditSessions removes sessions older than the given TTL.
func CleanupExpiredModerationEditSessions(ctx context.Context, db *sql.DB, ttl time.Duration) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM moderation_edit_sessions WHERE updated_at < NOW() - ($1 * INTERVAL '1 second')`,
		int64(ttl.Seconds()),
	)
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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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

// FetchRecentDedupTexts returns raw English titles for near-duplicate checks.
// Only pending/published rows are considered to avoid using rejected noise.
func FetchRecentDedupTexts(ctx context.Context, db *sql.DB, hours int) ([]string, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT title_raw
		FROM articles
		WHERE created_at > NOW() - ($1::int * INTERVAL '1 hour')
		  AND status IN ($2, $3)
		  AND title_raw IS NOT NULL
		  AND title_raw <> ''
		ORDER BY created_at DESC
		LIMIT 500`, hours, StatusPending, StatusPublished)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make([]string, 0, 128)
	for rows.Next() {
		var titleRaw string
		if err := rows.Scan(&titleRaw); err != nil {
			return nil, err
		}
		result = append(result, titleRaw)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// FetchRecentPublishedTitles returns titles of published and pending-moderation articles in the last 'hours'
// so same-day runs do not re-select an unmoderated story already awaiting review.
func FetchRecentPublishedTitles(ctx context.Context, db *sql.DB, hours int) ([]string, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT title_raw
		FROM articles
		WHERE created_at > NOW() - ($1::int * INTERVAL '1 hour')
		  AND (status IN ($2, $3, $4) OR posted_tg = true)
		  AND title_raw IS NOT NULL
		  AND title_raw <> ''
		ORDER BY created_at DESC
		LIMIT 120`, hours, StatusPublished, StatusPending, StatusNeedsEdit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

// FetchRecentStories loads multi-tier semantic dedup metadata for recent pending, published, and recently rejected stories.
func FetchRecentStories(ctx context.Context, db *sql.DB, hours int) ([]dedup.RecentStory, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id,
		       COALESCE(title_raw, ''),
		       COALESCE(title_norm, ''),
		       COALESCE(content_hash, ''),
		       COALESCE(category, ''),
		       COALESCE(franchise_tag, ''),
		       COALESCE(story_cluster_key, ''),
		       COALESCE(concrete_anchor, ''),
		       embedding,
		       COALESCE(status, ''),
		       created_at
		FROM articles
		WHERE (
			(created_at > NOW() - ($1::int * INTERVAL '1 hour') AND status IN ($2, $3, $4))
			OR (created_at > NOW() - INTERVAL '48 hours' AND status = $5 AND COALESCE(story_cluster_key, '') <> '')
		)
		ORDER BY created_at DESC
		LIMIT 300`, hours, StatusPublished, StatusPending, StatusNeedsEdit, StatusRejected)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var stories []dedup.RecentStory
	for rows.Next() {
		var s dedup.RecentStory
		var embBytes []byte
		if err := rows.Scan(
			&s.ID, &s.TitleRaw, &s.TitleNorm, &s.ContentHash,
			&s.Category, &s.FranchiseTag, &s.StoryClusterKey, &s.ConcreteAnchor,
			&embBytes, &s.Status, &s.CreatedAt,
		); err != nil {
			return nil, err
		}
		if len(embBytes) > 0 {
			var vec []float32
			if err := json.Unmarshal(embBytes, &vec); err == nil {
				s.Embedding = vec
			}
		}
		stories = append(stories, s)
	}
	return stories, rows.Err()
}

// HasContentHash checks if an identical content_hash was already stored within lookback hours.
func HasContentHash(ctx context.Context, db *sql.DB, contentHash string, hours int) (bool, error) {
	if db == nil || strings.TrimSpace(contentHash) == "" {
		return false, nil
	}
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM articles
			WHERE content_hash = $1
			  AND created_at > NOW() - ($2::int * INTERVAL '1 hour')
			  AND status IN ($3, $4, $5)
		)`, contentHash, hours, StatusPublished, StatusPending, StatusNeedsEdit).Scan(&exists)
	return exists, err
}

// FetchRecentCategoryHistory returns the categories of the most recent N non-highlight articles.
func FetchRecentCategoryHistory(ctx context.Context, db *sql.DB, limit int) ([]string, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(category, '')
		FROM articles
		WHERE COALESCE(source_type, '') <> 'highlight'
		  AND status IN ($1, $2, $3)
		  AND COALESCE(category, '') <> ''
		ORDER BY created_at DESC
		LIMIT $4`, StatusPublished, StatusPending, StatusNeedsEdit, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var cats []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, rows.Err()
}

// FetchRecentFranchiseCounts counts how often each franchise_tag appeared within lookback hours.
func FetchRecentFranchiseCounts(ctx context.Context, db *sql.DB, hours int) (map[string]int, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT franchise_tag, COUNT(*)
		FROM articles
		WHERE created_at > NOW() - ($1::int * INTERVAL '1 hour')
		  AND status IN ($2, $3, $4)
		  AND COALESCE(franchise_tag, '') <> ''
		GROUP BY franchise_tag`, hours, StatusPublished, StatusPending, StatusNeedsEdit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]int)
	for rows.Next() {
		var tag string
		var cnt int
		if err := rows.Scan(&tag, &cnt); err != nil {
			return nil, err
		}
		counts[tag] = cnt
	}
	return counts, rows.Err()
}

// GetState retrieves a string value by key from bot_state.
func GetState(ctx context.Context, db *sql.DB, key string) (string, error) {
	if db == nil {
		return "", nil
	}
	var val string
	err := db.QueryRowContext(ctx, `SELECT value FROM bot_state WHERE key=$1`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// SetState upserts a key/value pair into bot_state.
func SetState(ctx context.Context, db *sql.DB, key, value string) error {
	if db == nil {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO bot_state (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE
		SET value=EXCLUDED.value, updated_at=NOW()`, key, value)
	return err
}

// GetCachedAITranslation retrieves a cached AI JSON response if fresh within ttlHours.
func GetCachedAITranslation(ctx context.Context, db *sql.DB, urlHash string, ttlHours int) (string, string, bool, error) {
	if db == nil || urlHash == "" {
		return "", "", false, nil
	}
	var payload, provider string
	err := db.QueryRowContext(ctx, `
		SELECT payload_json, provider
		FROM ai_translation_cache
		WHERE url_hash = $1
		  AND created_at > NOW() - ($2::int * INTERVAL '1 hour')`, urlHash, ttlHours).Scan(&payload, &provider)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return payload, provider, true, nil
}

// SaveCachedAITranslation stores an AI JSON response keyed by url_hash.
func SaveCachedAITranslation(ctx context.Context, db *sql.DB, urlHash, payloadJSON, provider string) error {
	if db == nil || urlHash == "" || payloadJSON == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO ai_translation_cache (url_hash, payload_json, provider, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (url_hash) DO UPDATE
		SET payload_json=EXCLUDED.payload_json, provider=EXCLUDED.provider, created_at=NOW()`, urlHash, payloadJSON, provider)
	return err
}


// UpdateBodies sets body_ua, body_threads and ai_provider after rewrite.
func UpdateBodies(ctx context.Context, db *sql.DB, id int, bodyUA, bodyThreads, aiProvider string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE articles SET body_ua=$1, body_threads=$2, ai_provider=$3 WHERE id=$4`,
		bodyUA, bodyThreads, aiProvider, id)
	return err
}

func UpdateBodyUAOnly(ctx context.Context, db *sql.DB, id int, bodyUA string) error {
	_, err := db.ExecContext(ctx, `UPDATE articles SET body_ua=$1 WHERE id=$2`, bodyUA, id)
	return err
}

// UpdateVideoURL updates video_url (e.g. with cached Telegram file_id).
func UpdateVideoURL(ctx context.Context, db *sql.DB, id int, videoURL string) error {
	_, err := db.ExecContext(ctx, `UPDATE articles SET video_url=$1 WHERE id=$2`, nullStr(videoURL), id)
	return err
}

// MarkPostedTG sets posted_tg = true and saves the tg_message_id.
func MarkPostedTG(ctx context.Context, db *sql.DB, id int, tgMessageID int) error {
	_, err := db.ExecContext(ctx, `UPDATE articles SET posted_tg=TRUE, tg_message_id=$2 WHERE id=$1`, id, tgMessageID)
	return err
}

// MarkPostedThreads sets posted_threads = true.
func MarkPostedThreads(ctx context.Context, db *sql.DB, id int) error {
	_, err := db.ExecContext(ctx, `UPDATE articles SET posted_threads=TRUE WHERE id=$1`, id)
	return err
}

// Cleanup deletes articles older than 30 days, preserving highlight masterpieces forever so they are never repeated.
func Cleanup(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `DELETE FROM articles WHERE created_at < NOW() - INTERVAL '30 days' AND COALESCE(source_type, '') <> 'highlight'`)
	return err
}

// RunMigration applies versioned SQL files from the migrations directory.
func RunMigration(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INT PRIMARY KEY)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	paths, err := filepath.Glob(filepath.Join("migrations", "*.sql"))
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no migration files found in %s", filepath.Join("migrations", "*.sql"))
	}
	sort.Strings(paths)

	for _, path := range paths {
		baseName := filepath.Base(path)
		parts := strings.Split(baseName, "_")
		if len(parts) < 2 {
			continue
		}
		
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("parse migration version from %s: %w", path, err)
		}

		var applied bool
		err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration version %d: %w", version, err)
		}
		if applied {
			continue
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}
		sqlText := strings.TrimSpace(string(raw))
		if sqlText == "" {
			continue
		}
		
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin transaction for migration %s: %w", path, err)
		}

		if _, err := tx.ExecContext(ctx, sqlText); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", path, err)
		}
		
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", path, err)
		}
		
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", path, err)
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
	defer func() { _ = rows.Close() }()

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

// WasEventPostedRecently returns true if an article with the given event_tag
// was created (or published) in the last 'hours'.
func WasEventPostedRecently(ctx context.Context, db *sql.DB, eventTag string, hours int) (bool, error) {
	if eventTag == "" {
		return false, nil
	}
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM articles
		WHERE event_tag = $1 AND created_at > NOW() - ($2::int * INTERVAL '1 hour')
		AND status IN ('published', 'pending')
	`, eventTag, hours).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// FetchRecentlyHighlightedGames returns a list of game titles previously posted as highlights.
func FetchRecentlyHighlightedGames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT title_raw 
		FROM articles 
		WHERE source_type = 'highlight' OR source_name = 'Nintendo Masterpiece'
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		result = append(result, title)
	}
	return result, rows.Err()
}

// FetchRecentThreadsTexts returns non-empty Threads bodies from the last 7 days.
func FetchRecentThreadsTexts(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT body_threads 
		FROM articles 
		WHERE created_at > NOW() - INTERVAL '7 days'
		  AND body_threads IS NOT NULL 
		  AND body_threads <> ''
		ORDER BY created_at DESC 
		LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		result = append(result, text)
	}
	return result, rows.Err()
}
