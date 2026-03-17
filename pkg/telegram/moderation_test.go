package telegram

import (
	"strings"
	"testing"

	"github.com/deuswork/nintendoflow/pkg/db"
)

func TestParseModerationCallbackDataSupportsCancel(t *testing.T) {
	action, articleID, err := ParseModerationCallbackData("mod:cancel:42")
	if err != nil {
		t.Fatalf("ParseModerationCallbackData returned error: %v", err)
	}
	if action != moderationActionCancel {
		t.Fatalf("expected action %q, got %q", moderationActionCancel, action)
	}
	if articleID != 42 {
		t.Fatalf("expected articleID=42, got %d", articleID)
	}
}

func TestBuildModerationEditWaitingTextMentionsTitle(t *testing.T) {
	text := BuildModerationEditWaitingText(db.Article{TitleRaw: "Switch 2 leak"})
	if text == "" {
		t.Fatal("expected non-empty waiting text")
	}
	if got := text; got == "Switch 2 leak" {
		t.Fatal("expected formatted waiting text, got raw title only")
	}
	if want := "Switch 2 leak"; !strings.Contains(text, want) {
		t.Fatalf("expected waiting text to contain %q, got %q", want, text)
	}
}

func TestModerationWaitingKeyboardUsesCancelAction(t *testing.T) {
	markup := moderationWaitingKeyboard(7)
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("expected one cancel button, got %#v", markup.InlineKeyboard)
	}
	if got := markup.InlineKeyboard[0][0].CallbackData; got == nil || *got != "mod:cancel:7" {
		t.Fatalf("unexpected callback data: %#v", got)
	}
}
