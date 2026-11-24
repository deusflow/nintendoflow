package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeJSONExtractsPayloadFromThinkAndFences(t *testing.T) {
	raw := "<think>Thinking through the Nintendo Switch 2 DLSS details...</think>\nHere is your JSON:\n```json\n{\"skip\": false, \"type\": \"insight\", \"category\": \"tech\", \"story_cluster_key\": \"switch2-dlss4-datamining\", \"telegram_html\": \"<b>Switch 2 отримав підтримку DLSS 4</b>\", \"threads_text\": \"У прошивці Switch 2 знайшли сліди DLSS 4\"}\n```\nHope this helps!"
	got := SanitizeJSON(raw)
	post, err := ParseJSONPost(got)
	if err != nil {
		t.Fatalf("ParseJSONPost failed: %v", err)
	}
	if post.Category != "tech" {
		t.Fatalf("expected category 'tech', got %q", post.Category)
	}
	if post.StoryClusterKey != "switch2-dlss4-datamining" {
		t.Fatalf("expected story_cluster_key 'switch2-dlss4-datamining', got %q", post.StoryClusterKey)
	}
	if post.Type != ArticleTypeInsight {
		t.Fatalf("expected type insight, got %q", post.Type)
	}
}

func TestVerifyUkrainianRejectsRussianAndStubs(t *testing.T) {
	bad := []string{
		"<b>Nintendo</b> выпустила новое обновление системы, что очень круто.",
		"<b>Nintendo</b> объявила о новой консоли, это важно.",
		"На жаль, я не маю доступу до повного тексту цієї статті.",
		"Коротко.",
	}
	for _, s := range bad {
		if err := VerifyUkrainian(s); err == nil {
			t.Errorf("expected VerifyUkrainian to reject %q", s)
		}
	}

	good := `<b>🔍 У файлах Nintendo Switch 2 знайшли підтримку DLSS 4 і моделей Transformer</b>

Датамайнери виявили згадки про Preset K та Tiny DLSS у свіжому SDK для Switch 2. Для гравців це означає чіткішу картинку без втрати частоти кадрів.`
	if err := VerifyUkrainian(good); err != nil {
		t.Fatalf("expected valid Ukrainian post to pass, got: %v", err)
	}
}

func TestVerifyNumbersDetectsHallucinationsAndAcceptsSourceMatches(t *testing.T) {
	src := "Nintendo sold 15,000,000 Switch 2 consoles worldwide and shares jumped 12.5% after twenty announcements in 2026."
	okOut := "Nintendo продала вже 15 000 000 консолей Switch 2, а акції зросли на 12.5% після 20 анонсів у 2026 році та 3 крутих презентацій."
	if err := VerifyNumbers(src, okOut); err != nil {
		t.Fatalf("expected valid numbers to pass, got: %v", err)
	}

	hallucinated := "Nintendo продала 99 мільйонів консолей Switch 2 та підняла ціну на 40%."
	err := VerifyNumbers(src, hallucinated)
	if err == nil {
		t.Fatal("expected hallucinated numbers 99 / 40% to be caught")
	}
	if !strings.Contains(err.Error(), "99") && !strings.Contains(err.Error(), "40%") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

type stubProvider struct {
	name  string
	calls *int
	fn    func(call int) (string, error)
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Complete(_ context.Context, _ string) (string, error) {
	*s.calls++
	return s.fn(*s.calls)
}

func TestManagerCooldownSkipsPermanent403ProviderOnSubsequentCalls(t *testing.T) {
	c1, c2 := 0, 0
	p1 := &stubProvider{
		name:  "gemini-3.5-flash",
		calls: &c1,
		fn: func(_ int) (string, error) {
			return "", errors.New("gemini generate: Error 403, PERMISSION_DENIED: Free tier is not available for this project")
		},
	}
	p2 := &stubProvider{
		name:  "groq-llama",
		calls: &c2,
		fn: func(_ int) (string, error) {
			return "Успішна відповідь українською мовою про новини Nintendo Switch 2.", nil
		},
	}

	m := NewManager([]AIProvider{p1, p2}, 4, 0)
	for range 3 {
		out, err := m.Generate(context.Background(), "test prompt")
		if err != nil {
			t.Fatalf("unexpected Generate error: %v", err)
		}
		if out == "" {
			t.Fatal("expected non-empty output")
		}
	}

	if c1 != 1 {
		t.Fatalf("expected 403 provider to be called only once due to run cooldown, got %d", c1)
	}
	if c2 != 3 {
		t.Fatalf("expected fallback provider to be called 3 times, got %d", c2)
	}
}

func TestManagerGenerateValidatedFallsBackOnQualityError(t *testing.T) {
	c1, c2 := 0, 0
	p1 := &stubProvider{
		name:  "bad-russian",
		calls: &c1,
		fn: func(_ int) (string, error) {
			return "Эта новость полностью на русском языке про консоли.", nil
		},
	}
	p2 := &stubProvider{
		name:  "good-ukrainian",
		calls: &c2,
		fn: func(_ int) (string, error) {
			return "<b>Новини світу Nintendo</b>\n\nКомпанія Nintendo оновила апаратну архітектуру Switch 2 та розширила партнерство з Nvidia.", nil
		},
	}

	m := NewManager([]AIProvider{p1, p2}, 4, 5*time.Millisecond)
	out, err := m.GenerateValidated(context.Background(), "prompt", VerifyUkrainian)
	if err != nil {
		t.Fatalf("expected fallback to valid provider, got error: %v", err)
	}
	if !strings.Contains(out, "Компанія Nintendo") {
		t.Fatalf("unexpected validated output: %q", out)
	}
	if m.LastProvider() != "good-ukrainian" {
		t.Fatalf("expected last provider 'good-ukrainian', got %q", m.LastProvider())
	}
}

func TestGroqJSONValidateFailedRetriesWithoutResponseFormat(t *testing.T) {
	reqCount := 0
	var sawStrictSecondCall bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, hasRespFormat := body["response_format"]
		if reqCount == 1 {
			if !hasRespFormat {
				t.Fatalf("expected first request to send response_format")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Failed to generate JSON","type":"invalid_request_error","code":"json_validate_failed"}}`))
			return
		}
		sawStrictSecondCall = hasRespFormat
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]string{
						"content": "<think>thinking</think>\n```json\n{\"game_name\":\"Metroid Prime\",\"telegram_html\":\"<b>Metroid Prime</b>\"}\n```",
					},
				},
			},
		})
	}))
	defer srv.Close()

	g := NewGroqProvider("test-key", "openai/gpt-oss-120b", srv.URL)
	out, err := g.Complete(context.Background(), "System prompt\n=== КІНЕЦЬ ІНСТРУКЦІЙ ===\nRespond with JSON.")
	if err != nil {
		t.Fatalf("expected retry without response_format to succeed, got: %v", err)
	}
	if reqCount != 2 {
		t.Fatalf("expected exactly 2 HTTP requests, got %d", reqCount)
	}
	if sawStrictSecondCall {
		t.Fatalf("expected response_format to be omitted on retry")
	}
	if !strings.HasPrefix(out, "{") || !strings.Contains(out, "Metroid Prime") {
		t.Fatalf("expected sanitized JSON output, got %q", out)
	}
}
