package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
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
