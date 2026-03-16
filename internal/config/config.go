package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DatabaseURL        string
	TelegramBotToken   string
	TelegramChannelID  string
	TestTelegramToken  string
	TestChannelID      string
	TestAdminChatID    string
	GeminiAPIKey       string
	OpenRouterAPIKey   string // optional
	GeminiModel        string
	MinScore           int
	RecentTitlesHours  int
	DryRun             bool
	TestModerationMode bool
	FeedsPath          string
	KeywordsPath       string
}

type Feed struct {
	URL                  string `yaml:"url"`
	Name                 string `yaml:"name"`
	Lang                 string `yaml:"lang"`
	Priority             int    `yaml:"priority"`
	Active               bool   `yaml:"active"`
	Type                 string `yaml:"type"`
	RequireAnchor        bool   `yaml:"require_anchor"`
	NeedsRedirectResolve bool   `yaml:"needs_redirect_resolve"`
	TimeoutSeconds       int    `yaml:"timeout_seconds"`
	// FetchMode controls the fetcher backend.
	// Values: "" / "rss" (default RSS parser) | "reddit_json" (Reddit JSON API).
	FetchMode string `yaml:"fetch_mode"`
}

type Keyword struct {
	Word     string `yaml:"word"`
	Category string `yaml:"category"`
	Role     string `yaml:"role"`
	Weight   int    `yaml:"weight"`
}

// Topic groups keywords under a named category with an enabled flag and a
// priority multiplier. effective_weight = keyword.Weight * Priority / 100.
// Priority defaults to 100 when the field is omitted in YAML (zero value).
type Topic struct {
	Enabled  bool      `yaml:"enabled"`
	Priority int       `yaml:"priority"`
	Keywords []Keyword `yaml:"keywords"`
}

// topicsFile is the top-level YAML wrapper for the new topics format.
type topicsFile struct {
	Topics map[string]Topic `yaml:"topics"`
}

func Load() (*Config, error) {
	// Load .env if present (local dev); ignore error in CI
	_ = godotenv.Load()

	cfg := &Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		TelegramBotToken:   os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChannelID:  os.Getenv("TELEGRAM_CHANNEL_ID"),
		TestTelegramToken:  os.Getenv("TEST_TELEGRAM_TOKEN"),
		TestChannelID:      os.Getenv("TEST_CHANNEL_ID"),
		TestAdminChatID:    os.Getenv("TEST_ADMIN_CHAT_ID"),
		GeminiAPIKey:       os.Getenv("GEMINI_API_KEY"),
		OpenRouterAPIKey:   os.Getenv("OPENROUTER_API_KEY"), // optional
		GeminiModel:        getEnvOrDefault("GEMINI_MODEL", "gemini-2.5-flash"),
		MinScore:           getEnvInt("MIN_SCORE", 4),
		RecentTitlesHours:  getEnvInt("RECENT_TITLES_HOURS", 24),
		DryRun:             os.Getenv("DRY_RUN") == "true",
		TestModerationMode: os.Getenv("TEST_MODERATION_MODE") == "true",
		FeedsPath:          getEnvOrDefault("FEEDS_PATH", "feeds.yaml"),
		KeywordsPath:       getEnvOrDefault("KEYWORDS_PATH", "keywords.yaml"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.TestModerationMode {
		if cfg.TestTelegramToken == "" {
			return nil, fmt.Errorf("TEST_TELEGRAM_TOKEN is required when TEST_MODERATION_MODE=true")
		}
		if cfg.TestChannelID == "" {
			return nil, fmt.Errorf("TEST_CHANNEL_ID is required when TEST_MODERATION_MODE=true")
		}
	} else {
		if cfg.TelegramBotToken == "" {
			return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
		}
		if cfg.TelegramChannelID == "" {
			return nil, fmt.Errorf("TELEGRAM_CHANNEL_ID is required")
		}
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
		if f.Priority == 0 {
			f.Priority = 100
		}
		active = append(active, f)
	}
	return active, nil
}

func LoadKeywords(path string) (map[string]Topic, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("keywords config: cannot read %q: %w", path, err)
	}
	var tf topicsFile
	if err := yaml.Unmarshal(raw, &tf); err != nil {
		return nil, fmt.Errorf("keywords config: cannot parse %q: %w", path, err)
	}
	if tf.Topics == nil {
		tf.Topics = make(map[string]Topic)
	}
	// Normalise: default priority=100 when the field is omitted (zero value).
	for name, topic := range tf.Topics {
		if topic.Priority == 0 {
			topic.Priority = 100
		}
		// Drop keywords with an empty word.
		valid := topic.Keywords[:0]
		for _, kw := range topic.Keywords {
			if strings.TrimSpace(kw.Word) != "" {
				valid = append(valid, kw)
			}
		}
		topic.Keywords = valid
		tf.Topics[name] = topic
	}
	return tf.Topics, nil
}

func LogConfigLoaded(activeFeeds int, totalKeywords int) {
	slog.Info("config loaded", "active_feeds", activeFeeds, "total_keywords", totalKeywords)
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
