package main

import (
	"context"
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

	database, err := db.Connect(dsn)
	if err != nil {
		slog.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = database.Close() }()

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
}
