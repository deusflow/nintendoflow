package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/db"
)

type ExportArticle struct {
	ID          int        `json:"id"`
	TitleRaw    string     `json:"title_raw"`
	TitleUA     string     `json:"title_ua"`
	BodyUA      string     `json:"body_ua"`
	Description string     `json:"description"`
	ImageURL    string     `json:"image_url"`
	SourceName  string     `json:"source_name"`
	SourceType  string     `json:"source_type"`
	SourceURL   string     `json:"source_url"`
	Score       int        `json:"score"`
	AIProvider  string     `json:"ai_provider"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"`
}

type ExportData struct {
	Articles   []ExportArticle `json:"articles"`
	ExportedAt time.Time       `json:"exported_at"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	host, dbName := parseDSNInfo(dsn)
	slog.Info("export: target database", "host", host, "database", dbName)

	slog.Info("export: connecting to database")
	var database *sql.DB
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		database, err = db.Connect(dsn)
		if err == nil {
			slog.Info("export: database connected", "attempt", attempt)
			break
		}
		slog.Warn("export: connection attempt failed", "attempt", attempt, "error", err)
		if attempt < 3 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
	}
	if err != nil {
		slog.Error("export: db connect failed after 3 attempts", "error", err)
		os.Exit(1)
	}
	defer func() { _ = database.Close() }()
	slog.Info("export: database ready")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stats, err := fetchArticleStatusStats(ctx, database)
	if err != nil {
		slog.Error("export: failed to read article status stats", "error", err)
		os.Exit(1)
	}
	slog.Info("export: article status stats",
		"published", stats[db.StatusPublished],
		"pending", stats[db.StatusPending],
		"needs_edit", stats[db.StatusNeedsEdit],
		"rejected", stats[db.StatusRejected],
	)
	if stats[db.StatusPublished] == 0 {
		slog.Warn("export: no published rows found; data.json will be empty")
	}

	// Fetch all published articles
	const pageSize = 1000
	var allArticles []ExportArticle

	for page := 1; ; page++ {
		offset := (page - 1) * pageSize
		articles, err := db.ListPublishedArticles(ctx, database, pageSize, offset)
		if err != nil {
			slog.Error("list articles failed", "error", err, "page", page)
			os.Exit(1)
		}

		if len(articles) == 0 {
			break
		}

		for _, a := range articles {
			allArticles = append(allArticles, ExportArticle{
				ID:          a.ID,
				TitleRaw:    a.TitleRaw,
				TitleUA:     a.TitleUA,
				BodyUA:      a.BodyUA,
				Description: a.BodyUA, // Use body_ua as description for JSON
				ImageURL:    a.ImageURL,
				SourceName:  a.SourceName,
				SourceType:  a.SourceType,
				SourceURL:   a.SourceURL,
				Score:       a.Score,
				AIProvider:  a.AIProvider,
				Status:      a.Status,
				CreatedAt:   a.CreatedAt,
				PublishedAt: a.PublishedAt,
			})
		}

		slog.Info("exported page", "page", page, "count", len(articles))
	}

	data := ExportData{
		Articles:   allArticles,
		ExportedAt: time.Now().UTC(),
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		slog.Error("json marshal failed", "error", err)
		os.Exit(1)
	}

	outPath := "docs/data.json"
	if err := os.WriteFile(outPath, jsonData, 0644); err != nil {
		slog.Error("write file failed", "path", outPath, "error", err)
		os.Exit(1)
	}

	slog.Info("export complete", "path", outPath, "articles", len(allArticles))
	if len(allArticles) > 0 {
		slog.Info("exported articles preview", "total", len(allArticles), "first_id", allArticles[0].ID, "first_title", allArticles[0].TitleUA)
	} else {
		slog.Info("exported articles preview", "total", 0, "warning", "no published articles found")
	}
}

func fetchArticleStatusStats(ctx context.Context, database *sql.DB) (map[string]int, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM articles
		GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("query status stats: %w", err)
	}
	defer rows.Close()

	stats := map[string]int{
		db.StatusPending:   0,
		db.StatusPublished: 0,
		db.StatusNeedsEdit: 0,
		db.StatusRejected:  0,
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan status stats: %w", err)
		}
		stats[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate status stats: %w", err)
	}
	return stats, nil
}

func parseDSNInfo(dsn string) (host, dbName string) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "unknown", "unknown"
	}
	host = u.Hostname()
	dbName = strings.TrimPrefix(u.Path, "/")
	if host == "" {
		host = "unknown"
	}
	if dbName == "" {
		dbName = "unknown"
	}
	return host, dbName
}
