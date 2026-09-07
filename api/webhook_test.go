package handler

import (
	"net/http"
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

func TestIsTelegramIP(t *testing.T) {
	cases := []struct {
		ip    string
		valid bool
	}{
		{"149.154.160.1", true},
		{"149.154.175.254", true},
		{"91.108.4.1", true},
		{"91.108.7.254", true},
		{"2001:67c:4e8::1", true},
		{"2001:67c:4e8:ffff:ffff:ffff:ffff:ffff", true},
		{"8.8.8.8", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"invalid-ip", false},
	}

	for _, tc := range cases {
		if got := isTelegramIP(tc.ip); got != tc.valid {
			t.Errorf("isTelegramIP(%q) = %v, want %v", tc.ip, got, tc.valid)
		}
	}
}

func TestGetClientIP(t *testing.T) {
	r1, _ := http.NewRequest("POST", "/", nil)
	r1.Header.Set("X-Forwarded-For", "149.154.160.5:443, 10.0.0.1")
	if got := getClientIP(r1); got != "149.154.160.5" {
		t.Errorf("getClientIP(r1) = %q, want %q", got, "149.154.160.5")
	}

	r2, _ := http.NewRequest("POST", "/", nil)
	r2.Header.Set("X-Forwarded-For", "[2001:67c:4e8::1]:8443")
	if got := getClientIP(r2); got != "2001:67c:4e8::1" {
		t.Errorf("getClientIP(r2) = %q, want %q", got, "2001:67c:4e8::1")
	}

	r3, _ := http.NewRequest("POST", "/", nil)
	r3.Header.Set("X-Real-IP", "91.108.4.10")
	if got := getClientIP(r3); got != "91.108.4.10" {
		t.Errorf("getClientIP(r3) = %q, want %q", got, "91.108.4.10")
	}

	r4, _ := http.NewRequest("POST", "/", nil)
	r4.RemoteAddr = "127.0.0.1:12345"
	if got := getClientIP(r4); got != "127.0.0.1" {
		t.Errorf("getClientIP(r4) = %q, want %q", got, "127.0.0.1")
	}
}
