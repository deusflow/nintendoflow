package ai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildProvidersFromConfigOrderAndFlags(t *testing.T) {
	t.Setenv("KEY_GH", "gh-key")
	t.Setenv("KEY_GROQ", "groq-key")

	cfgPath := filepath.Join(t.TempDir(), "ai_config.json")
	jsonConfig := `{
  "models": [
    {"name":"gemini","model":"gemini-2.5-flash","enabled":true,"api_key_env":"KEY_GEMINI"},
    {"name":"github_models","model":"gpt-5","enabled":true,"api_key_env":"KEY_GH"},
    {"name":"groq","model":"llama-3.3-70b-versatile","enabled":true,"api_key_env":"KEY_GROQ"},
    {"name":"openrouter","model":"deepseek/deepseek-chat:free","enabled":false,"api_key_env":"KEY_OR"}
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(jsonConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	providers, err := BuildProvidersFromConfig(context.Background(), cfgPath)
	if err != nil {
		t.Fatalf("BuildProvidersFromConfig returned error: %v", err)
	}

	if len(providers) != 2 {
		t.Fatalf("expected 2 providers (gemini skipped due missing key, openrouter disabled), got %d", len(providers))
	}
	if providers[0].Name() != "github-models-gpt-5" {
		t.Fatalf("unexpected provider[0]: %s", providers[0].Name())
	}
	if providers[1].Name() != "groq-llama-3.3-70b-versatile" {
		t.Fatalf("unexpected provider[1]: %s", providers[1].Name())
	}
}
