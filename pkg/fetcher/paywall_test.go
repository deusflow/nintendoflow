package fetcher

import (
	"strings"
	"testing"
)

func TestIsPaywalledOrStubDetectsGatesAndShortTeasers(t *testing.T) {
	if blocked, _ := IsPaywalledOrStub("Subscribe to continue reading the full exclusive report on Nintendo.", "https://www.bloomberg.com/news/nintendo"); !blocked {
		t.Fatal("expected paywall phrase to be detected")
	}

	if blocked, _ := IsPaywalledOrStub("Just a moment... Checking your browser before accessing wccftech.com", "https://wccftech.com/test"); !blocked {
		t.Fatal("expected Cloudflare challenge stub to be detected")
	}

	if blocked, _ := IsPaywalledOrStub("Nintendo revealed a single teaser sentence today about Switch 2.", "https://www.bloomberg.com/news/articles/1"); !blocked {
		t.Fatal("expected short stub to be flagged")
	}

	goodArticle := strings.Repeat("Dataminers uncovered hidden DLSS 4 Transformer Preset K and Tiny DLSS models inside Nintendo Switch 2 SDK binaries. ", 4) +
		"\n\n" +
		strings.Repeat("Independent analysis suggests sharper image reconstruction without hurting handheld battery efficiency on Nvidia Tegra T239 hardware. ", 3)
	if blocked, reason := IsPaywalledOrStub(goodArticle, "https://wccftech.com/switch-2-dlss-4"); blocked {
		t.Fatalf("expected valid full article to pass, got blocked=%v reason=%q", blocked, reason)
	}
}
