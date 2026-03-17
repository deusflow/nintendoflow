package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// RouterConfig controls AI provider priority and enablement order.
type RouterConfig struct {
	Models []RouterModel `json:"models"`
}

// RouterModel describes one provider entry in ai_config.json.
type RouterModel struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	Enabled   bool   `json:"enabled"`
	APIKeyEnv string `json:"api_key_env"`
	BaseURL   string `json:"base_url,omitempty"`
}

func LoadRouterConfig(path string) (*RouterConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read AI router config %q: %w", path, err)
	}

	var cfg RouterConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse AI router config %q: %w", path, err)
	}
	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("AI router config %q has no models", path)
	}

	return &cfg, nil
}

// BuildProvidersFromConfig builds providers in config order and reads keys strictly from os.Getenv.
func BuildProvidersFromConfig(ctx context.Context, path string) ([]AIProvider, error) {
	cfg, err := LoadRouterConfig(path)
	if err != nil {
		return nil, err
	}

	providers := make([]AIProvider, 0, len(cfg.Models))
	for i, modelCfg := range cfg.Models {
		if !modelCfg.Enabled {
			slog.Info("AI model disabled, skipping", "name", modelCfg.Name, "index", i)
			continue
		}

		name := strings.ToLower(strings.TrimSpace(modelCfg.Name))
		if name == "" {
			return nil, fmt.Errorf("AI router config model at index %d has empty name", i)
		}

		apiKeyEnv := strings.TrimSpace(modelCfg.APIKeyEnv)
		if apiKeyEnv == "" {
			return nil, fmt.Errorf("AI router config model %q has empty api_key_env", modelCfg.Name)
		}
		apiKey := strings.TrimSpace(os.Getenv(apiKeyEnv))
		if apiKey == "" {
			slog.Warn("AI model enabled but API key env is empty, skipping",
				"name", modelCfg.Name,
				"api_key_env", apiKeyEnv,
			)
			continue
		}

		provider, err := buildProvider(ctx, name, modelCfg, apiKey)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no enabled AI providers with available API keys")
	}

	return providers, nil
}

func buildProvider(ctx context.Context, name string, modelCfg RouterModel, apiKey string) (AIProvider, error) {
	model := strings.TrimSpace(modelCfg.Model)
	switch name {
	case "gemini":
		if model == "" {
			model = "gemini-2.5-flash"
		}
		return NewGeminiProvider(ctx, apiKey, model)
	case "github_models", "github-models", "github":
		if model == "" {
			model = "gpt-5"
		}
		return NewGitHubModelsProvider(apiKey, model, modelCfg.BaseURL), nil
	case "groq":
		if model == "" {
			model = "llama-3.3-70b-versatile"
		}
		return NewGroqProvider(apiKey, model, modelCfg.BaseURL), nil
	case "openrouter":
		return NewOpenRouterProviderWithModel(apiKey, model), nil
	default:
		return nil, fmt.Errorf("unsupported AI provider name %q", modelCfg.Name)
	}
}
