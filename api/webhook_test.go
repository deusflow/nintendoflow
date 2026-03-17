package handler

import (
	"testing"
	"time"

	"github.com/deuswork/nintendoflow/pkg/db"
)

func TestEditSessionExpired(t *testing.T) {
	now := time.Now()

	if !editSessionExpired(db.ModerationEditSession{UpdatedAt: now.Add(-editSessionTTL - time.Second)}, now) {
		t.Fatal("expected expired session to return true")
	}

	if editSessionExpired(db.ModerationEditSession{UpdatedAt: now.Add(-editSessionTTL + time.Second)}, now) {
		t.Fatal("expected fresh session to return false")
	}

	if editSessionExpired(db.ModerationEditSession{}, now) {
		t.Fatal("expected zero UpdatedAt to return false")
	}
}

func TestFinalModerationStateText(t *testing.T) {
	if got := finalModerationStateText(db.StatusPublished); got != "Published ✅" {
		t.Fatalf("unexpected published text: %q", got)
	}
	if got := finalModerationStateText(db.StatusRejected); got != "Rejected ❌" {
		t.Fatalf("unexpected rejected text: %q", got)
	}
}
