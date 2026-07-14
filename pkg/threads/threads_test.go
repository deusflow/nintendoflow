package threads

import (
	"strings"
	"testing"
)

func TestFormatThread(t *testing.T) {
	title := "Super Mario 64: Безчасна Класика"
	username := "deusflow"
	msgID := 42

	thread := FormatThread(title, username, msgID)

	if !strings.Contains(thread, "Super Mario 64: Безчасна Класика") {
		t.Errorf("Expected thread to contain title, got: %q", thread)
	}

	if !strings.Contains(thread, "https://t.me/deusflow/42") {
		t.Errorf("Expected thread to contain Telegram link, got: %q", thread)
	}

	if !strings.Contains(thread, "#Nintendo") {
		t.Errorf("Expected thread to contain hashtag, got: %q", thread)
	}
}

func TestFormatThreadTruncation(t *testing.T) {
	// A very long title (500 characters)
	longTitle := strings.Repeat("A", 500)
	username := "deusflow"
	msgID := 12345

	thread := FormatThread(longTitle, username, msgID)
	threadRunes := []rune(thread)

	if len(threadRunes) > 500 {
		t.Errorf("Formatted thread is too long: %d characters (max 500)", len(threadRunes))
	}

	if !strings.Contains(thread, "...") {
		t.Errorf("Expected long thread to be truncated with ellipsis")
	}

	if !strings.Contains(thread, "https://t.me/deusflow/12345") {
		t.Errorf("Expected truncated thread to still contain link")
	}
}
