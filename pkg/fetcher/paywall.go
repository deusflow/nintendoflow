package fetcher

import (
	"fmt"
	"strings"
)

var paywallIndicators = []string{
	"subscribe to continue reading",
	"to continue reading this article",
	"this article is for subscribers",
	"exclusive for subscribers",
	"subscriber-only",
	"subscribers only",
	"create a free account to continue",
	"sign in to read",
	"log in to continue reading",
	"unlock this article",
	"read the full story with a subscription",
	"become a member to read",
	"you have reached your article limit",
	"enable javascript and cookies to continue",
	"please verify you are a human",
	"checking your browser before accessing",
	"attention required! | cloudflare",
	"just a moment...",
	"access to this page has been denied",
	"why did this happen? please make sure your browser supports cookies",
	"有料会員限定",
	"この記事は会員限定",
	"会員登録して続きを読む",
}

var knownPaywallDomains = []string{
	"bloomberg.com",
	"asia.nikkei.com",
	"nikkei.com",
	"ft.com",
	"wsj.com",
	"nytimes.com",
	"barrons.com",
}

// IsPaywalledOrStub checks if extracted article text is a paywall block, anti-bot gate, or tiny stub.
func IsPaywalledOrStub(content, articleURL string) (bool, string) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true, "empty content"
	}

	lowerContent := strings.ToLower(trimmed)
	lowerURL := strings.ToLower(articleURL)

	for _, phrase := range paywallIndicators {
		if strings.Contains(lowerContent, phrase) {
			return true, fmt.Sprintf("contains paywall/gate phrase: %q", phrase)
		}
	}

	runes := []rune(trimmed)
	runeCount := len(runes)
	if runeCount < 220 {
		return true, fmt.Sprintf("stub content too short (%d chars, min 220)", runeCount)
	}

	paragraphs := strings.Split(trimmed, "\n\n")
	realParagraphCount := 0
	for _, p := range paragraphs {
		if len([]rune(strings.TrimSpace(p))) > 40 {
			realParagraphCount++
		}
	}

	for _, domain := range knownPaywallDomains {
		if strings.Contains(lowerURL, domain) {
			if runeCount < 380 || realParagraphCount < 2 {
				return true, fmt.Sprintf("suspected paywall teaser from %s (%d chars, %d paragraphs)", domain, runeCount, realParagraphCount)
			}
		}
	}

	return false, ""
}
