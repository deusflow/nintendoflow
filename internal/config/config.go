package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL         string
	TelegramBotToken    string
	TelegramChannelID   string
	GeminiAPIKey        string
	OpenRouterAPIKey    string // optional
	GeminiModel         string
	MaxPostsPerRun      int
	MinScore            int
	DryRun              bool
	SleepBetweenAICalls time.Duration
}

func Load() (*Config, error) {
	// Load .env if present (local dev); ignore error in CI
	_ = godotenv.Load()

	cfg := &Config{
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		TelegramBotToken:    os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChannelID:   os.Getenv("TELEGRAM_CHANNEL_ID"),
		GeminiAPIKey:        os.Getenv("GEMINI_API_KEY"),
		OpenRouterAPIKey:    os.Getenv("OPENROUTER_API_KEY"), // optional
		GeminiModel:         getEnvOrDefault("GEMINI_MODEL", "gemini-2.5-flash"),
		MaxPostsPerRun:      getEnvInt("MAX_POSTS_PER_RUN", 3),
		MinScore:            getEnvInt("MIN_SCORE", 4),
		DryRun:              os.Getenv("DRY_RUN") == "true",
		SleepBetweenAICalls: 8 * time.Second,
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if cfg.TelegramChannelID == "" {
		return nil, fmt.Errorf("TELEGRAM_CHANNEL_ID is required")
	}
	if cfg.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required")
	}

	return cfg, nil
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
