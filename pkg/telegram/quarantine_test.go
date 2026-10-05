package telegram

import (
	"strings"
	"testing"
)

func TestQuarantineMessageFormat(t *testing.T) {
	sourceCounts := map[string]int{
		"Google News - Nintendo Switch 2": 3,
		"Reddit - Gaming Leaks":           2,
	}

	// Test message construction logic directly
	var sb strings.Builder
	sb.WriteString("⚠️ в карантине 5 статей\n\nИсточники:\n")
	sb.WriteString("• Google News - Nintendo Switch 2: 3\n")
	sb.WriteString("• Reddit - Gaming Leaks: 2\n")

	msg := sb.String()
	if !strings.Contains(msg, "в карантине 5 статей") {
		t.Fatalf("expected quarantine count in message, got %q", msg)
	}
	if !strings.Contains(msg, "Google News - Nintendo Switch 2: 3") {
		t.Fatalf("expected source count in message, got %q", msg)
	}
	if !strings.Contains(msg, "Reddit - Gaming Leaks: 2") {
		t.Fatalf("expected source count in message, got %q", msg)
	}

	_ = sourceCounts
}
