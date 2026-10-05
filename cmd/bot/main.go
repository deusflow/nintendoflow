package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/ai"
	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/highlight"
	"github.com/deuswork/nintendoflow/pkg/pipeline"
	"github.com/joho/godotenv"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	envFile := flag.String("env-file", "", "Path to .env file")
	dryRun := flag.Bool("dry-run", false, "Run pipeline in dry-run mode without publishing")
	modeFlag := flag.String("mode", "", "Execution mode: news (default) or highlight")
	flag.Parse()

	if *envFile != "" {
		if err := godotenv.Load(*envFile); err != nil {
			slog.Warn("failed to load custom env file", "path", *envFile, "error", err)
		} else {
			slog.Info("loaded env file", "path", *envFile)
		}
	}

	if *dryRun {
		_ = os.Setenv("DRY_RUN", "true")
	}

	// -- 1. Config ---------------------------------------------------------
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	if cfg.DryRun {
		slog.Info("running in dry-run mode (--dry-run flag or DRY_RUN=true)")
	}

	feeds, err := config.LoadFeeds(cfg.FeedsPath)
	if err != nil {
		slog.Error("feeds load failed", "path", cfg.FeedsPath, "error", err)
		os.Exit(1)
	}
	topics, err := config.LoadKeywords(cfg.KeywordsPath)
	if err != nil {
		slog.Error("keywords load failed", "path", cfg.KeywordsPath, "error", err)
		os.Exit(1)
	}
	totalKeywords := 0
	for _, t := range topics {
		totalKeywords += len(t.Keywords)
	}
	config.LogConfigLoaded(len(feeds), totalKeywords)

	ctx := context.Background()

	// -- 2. DB connect (retry 3x) -----------------------------------------
	database, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = database.Close() }()

	if cfg.DryRun {
		slog.Info("DRY_RUN - skipping database writes", "skip_migration", true, "skip_cleanup", true)
	} else {
		if err := db.RunMigration(ctx, database); err != nil {
			slog.Error("migration failed", "error", err)
			os.Exit(1)
		}

		// -- 3. DB cleanup -----------------------------------------------------
		if err := db.Cleanup(ctx, database); err != nil {
			slog.Warn("cleanup failed", "error", err)
		} else {
			slog.Info("db cleanup: removed articles older than 30 days")
		}
	}

	// -- 4. Init AI manager ------------------------------------------------
	aiConfigPath := os.Getenv("AI_CONFIG_PATH")
	if aiConfigPath == "" {
		aiConfigPath = "ai_config.json"
	}

	providers, err := ai.BuildProvidersFromConfig(ctx, aiConfigPath)
	if err != nil {
		slog.Error("AI router init failed", "config_path", aiConfigPath, "error", err)
		os.Exit(1)
	}
	providerNames := make([]string, 0, len(providers))
	for _, provider := range providers {
		providerNames = append(providerNames, provider.Name())
	}
	slog.Info("AI router ready", "config_path", aiConfigPath, "providers", strings.Join(providerNames, ","))

	// maxAICallsPerRun=4, delay=20s between calls to respect API limits
	manager := ai.NewManager(providers, 4, 20*time.Second)

	// -- 4.5. Check command-line mode ------------------------------------
	mode := "news"
	if *modeFlag != "" {
		mode = *modeFlag
	} else {
		for _, arg := range flag.Args() {
			if arg == "highlight" {
				mode = "highlight"
			}
		}
	}

	if mode == "highlight" {
		highlight.Run(ctx, database, manager, cfg)
		return
	}

	pipeline.Run(ctx, cfg, database, manager, feeds, topics)
}
