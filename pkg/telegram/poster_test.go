package telegram

import (
	"strings"
	"testing"
)

func TestSafeTruncateHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string untouched",
			input:    "<b>Hello</b> world",
			maxLen:   50,
			expected: "<b>Hello</b> world",
		},
		{
			name:     "truncation closes open bold tag",
			input:    "<b>Hello world this is a long text</b>",
			maxLen:   20,
			expected: "<b>Hello world th...</b>",
		},
		{
			name:     "truncation closes nested tags",
			input:    "<b><i>Hello world this is a nested tag test</i></b>",
			maxLen:   25,
			expected: "<b><i>Hello world this...</i></b>",
		},
		{
			name:     "truncation in middle of html tag strips incomplete tag",
			input:    "Test message <a href=\"https://example.com\">Click here</a>",
			maxLen:   18,
			expected: "Test message...</a>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := safeTruncateHTML(tc.input, tc.maxLen)
			// Must never have unclosed tag brackets '<' without matching '>'
			if strings.Count(res, "<") != strings.Count(res, ">") {
				t.Fatalf("unbalanced angle brackets in %q", res)
			}
			// Must end with closing tag if cut within open tag
			if tc.name == "truncation closes open bold tag" && !strings.HasSuffix(res, "</b>") {
				t.Errorf("expected suffix </b>, got %q", res)
			}
			if tc.name == "truncation closes nested tags" && !strings.HasSuffix(res, "</i></b>") {
				t.Errorf("expected suffix </i></b>, got %q", res)
			}
		})
	}
}

func TestNewBaseChat(t *testing.T) {
	// Numeric chat ID (private or supergroup)
	bc, err := newBaseChat("-100123456789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bc.ChatID != -100123456789 || bc.ChannelUsername != "" {
		t.Errorf("expected ChatID -100123456789 and empty username, got ChatID=%d Username=%q", bc.ChatID, bc.ChannelUsername)
	}

	// Channel username with @
	bc2, err := newBaseChat("@nintendoflow")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bc2.ChatID != 0 || bc2.ChannelUsername != "@nintendoflow" {
		t.Errorf("expected ChatID 0 and Username @nintendoflow, got ChatID=%d Username=%q", bc2.ChatID, bc2.ChannelUsername)
	}
}
