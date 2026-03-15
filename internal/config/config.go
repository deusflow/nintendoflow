package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
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
	RecentTitlesHours   int
	DryRun              bool
	SleepBetweenAICalls time.Duration
	FeedsPath           string
	KeywordsPath        string
}

type Feed struct {
	URL                  string `yaml:"url"`
	Name                 string `yaml:"name"`
	Lang                 string `yaml:"lang"`
	Priority             int    `yaml:"priority"`
	Active               bool   `yaml:"active"`
	Type                 string `yaml:"type"`
	NeedsRedirectResolve bool   `yaml:"needs_redirect_resolve"`
}

type Keyword struct {
	Word     string `yaml:"word"`
	Category string `yaml:"category"`
	Weight   int    `yaml:"weight"`
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
		RecentTitlesHours:   getEnvInt("RECENT_TITLES_HOURS", 24),
		DryRun:              os.Getenv("DRY_RUN") == "true",
		SleepBetweenAICalls: 8 * time.Second,
		FeedsPath:           getEnvOrDefault("FEEDS_PATH", "feeds.yaml"),
		KeywordsPath:        getEnvOrDefault("KEYWORDS_PATH", "keywords.yaml"),
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

func LoadFeeds(path string) ([]Feed, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("feeds config: cannot read %q: %w", path, err)
	}
	var feeds []Feed
	if err := yaml.Unmarshal(raw, &feeds); err != nil {
		return nil, fmt.Errorf("feeds config: cannot parse %q: %w", path, err)
	}

	active := make([]Feed, 0, len(feeds))
	for _, f := range feeds {
		if !f.Active {
			continue
		}
		if strings.TrimSpace(f.URL) == "" || strings.TrimSpace(f.Name) == "" {
			continue
		}
		active = append(active, f)
	}
	return active, nil
}

func LoadKeywords(path string) ([]Keyword, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("keywords config: cannot read %q: %w", path, err)
	}
	var keywords []Keyword
	if err := yaml.Unmarshal(raw, &keywords); err != nil {
		return nil, fmt.Errorf("keywords config: cannot parse %q: %w", path, err)
	}

	filtered := make([]Keyword, 0, len(keywords))
	for _, kw := range keywords {
		if strings.TrimSpace(kw.Word) == "" {
			continue
		}
		filtered = append(filtered, kw)
	}
	return filtered, nil
}

func LogConfigLoaded(activeFeeds int, keywords int) {
	slog.Info("config loaded", "active_feeds", activeFeeds, "keywords", keywords)
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
