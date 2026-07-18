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
	username := "deusflow"
	msgID := 42

	thread := FormatThread(article, username, msgID)

	if !strings.Contains(thread, "Це опис короткої новини для тестування.") {
		t.Errorf("Expected thread to contain news body, got: %q", thread)
	}

	if !strings.Contains(thread, "https://t.me/deusflow/42") {
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
	username := "deusflow"
	msgID := 12345

	thread := FormatThread(article, username, msgID)
	threadRunes := []rune(thread)

	if len(threadRunes) > 500 {
		t.Errorf("Formatted thread is too long: %d characters (max 500)", len(threadRunes))
	}

	if !strings.Contains(thread, "Дуже довга новина про Маріо") {
		t.Errorf("Expected truncated teaser to contain title, got: %q", thread)
	}

	if !strings.Contains(thread, "https://t.me/deusflow/12345") {
		t.Errorf("Expected truncated thread to still contain link")
	}
}

func TestFormatThreadWithBodyThreadsOverLimit(t *testing.T) {
	longBodyThreads := strings.Repeat("Текст для Трідс ", 35) // ~525 chars
	article := db.Article{
		TitleRaw:    "Маріо",
		BodyThreads: longBodyThreads,
	}
	username := "deusflow"
	msgID := 999

	thread := FormatThread(article, username, msgID)
	threadRunes := []rune(thread)

	if len(threadRunes) > 500 {
		t.Errorf("Formatted thread with BodyThreads is too long: %d characters (max 500)", len(threadRunes))
	}

	if !strings.Contains(thread, "https://t.me/deusflow/999") {
		t.Errorf("Expected thread to contain Telegram link, got: %q", thread)
	}
}
