package threads

import (
	"strings"
	"testing"

	"github.com/deuswork/nintendoflow/pkg/db"
)

func TestFormatThread(t *testing.T) {
	article := db.Article{
		TitleRaw: "Super Mario 64: Безчасна Класика",
		BodyUA:   "Це опис короткої новини для тестування.",
	}
	username := "Nintendoflow"
	msgID := 42

	thread := FormatThread(article, username, msgID)

	if !strings.Contains(thread, "Це опис короткої новини для тестування.") {
		t.Errorf("Expected thread to contain news body, got: %q", thread)
	}

	if !strings.Contains(thread, "https://t.me/Nintendoflow/42") {
		t.Errorf("Expected thread to contain Telegram link, got: %q", thread)
	}

	if !strings.Contains(thread, "#Nintendo") {
		t.Errorf("Expected thread to contain hashtag, got: %q", thread)
	}
}

func TestFormatThreadTruncation(t *testing.T) {
	// A very long body (600 characters) to trigger Scenario 4 (teaser fallback)
	longBody := strings.Repeat("A", 600)
	article := db.Article{
		TitleRaw: "Дуже довга новина про Маріо",
		BodyUA:   longBody,
	}
	username := "Nintendoflow"
	msgID := 12345

	thread := FormatThread(article, username, msgID)
	threadRunes := []rune(thread)

	if len(threadRunes) > 500 {
		t.Errorf("Formatted thread is too long: %d characters (max 500)", len(threadRunes))
	}

	if !strings.Contains(thread, "Дуже довга новина про Маріо") {
		t.Errorf("Expected truncated teaser to contain title, got: %q", thread)
	}

	if !strings.Contains(thread, "https://t.me/Nintendoflow/12345") {
		t.Errorf("Expected truncated thread to still contain link")
	}
}

func TestFormatThreadWithBodyThreadsOverLimit(t *testing.T) {
	longBodyThreads := strings.Repeat("Текст для Трідс ", 35) // ~525 chars
	article := db.Article{
		TitleRaw:    "Маріо",
		BodyThreads: longBodyThreads,
	}
	username := "Nintendoflow"
	msgID := 999

	thread := FormatThread(article, username, msgID)
	threadRunes := []rune(thread)

	if len(threadRunes) > 500 {
		t.Errorf("Formatted thread with BodyThreads is too long: %d characters (max 500)", len(threadRunes))
	}

	if !strings.Contains(thread, "https://t.me/Nintendoflow/999") {
		t.Errorf("Expected thread to contain Telegram link, got: %q", thread)
	}
}

func TestParseMetaError(t *testing.T) {
	// OAuth 190 (token expired)
	body190 := []byte(`{"error":{"message":"Error validating access token: Session has expired","type":"OAuthException","code":190,"error_subcode":463}}`)
	err190 := parseMetaError(body190, 400)
	if !strings.Contains(err190.Error(), "OAuth 190") || !strings.Contains(err190.Error(), "expired") {
		t.Errorf("expected expired token guidance, got: %v", err190)
	}

	// Code 100 (permission/parameter error)
	body100 := []byte(`{"error":{"message":"Unsupported post request. Object with ID 'me' does not exist","type":"GraphMethodException","code":100}}`)
	err100 := parseMetaError(body100, 400)
	if !strings.Contains(err100.Error(), "Code 100") {
		t.Errorf("expected code 100 guidance, got: %v", err100)
	}
}

