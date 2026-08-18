package telegram

import (
	"strings"
	"testing"

	"github.com/deuswork/nintendoflow/pkg/deals"
)

func TestFormatDealsDigestHTMLWithRegions(t *testing.T) {
	testDeals := []deals.Deal{
		{
			Title:      "Super Mario Odyssey",
			OldPrice:   59.99,
			NewPrice:   39.99,
			Currency:   "€",
			Region:     "EU",
			Cut:        33,
			Metacritic: 97,
			RedditQuote: "Incredible platformer, must buy.",
		},
		{
			Title:      "Hollow Knight",
			OldPrice:   15.0,
			NewPrice:   7.5,
			Currency:   "€",
			Region:     "EU",
			RegionName: "🇪🇺 eShop Європа",
			Cut:        50,
			Metacritic: 90,
		},
	}

	html := FormatDealsDigestHTML(testDeals)

	if !strings.Contains(html, "Super Mario Odyssey") {
		t.Errorf("expected HTML to contain game title, got:\n%s", html)
	}

	if !strings.Contains(html, "🇪🇺 eShop Європа") {
		t.Errorf("expected HTML to contain European region tag, got:\n%s", html)
	}

	if !strings.Contains(html, "<s>€59.99</s> → <b>€39.99</b>") {
		t.Errorf("expected HTML to contain formatted price, got:\n%s", html)
	}

	if !strings.Contains(html, "Ціни вказані для європейського eShop (EUR / €)") {
		t.Errorf("expected HTML to contain European eShop info note, got:\n%s", html)
	}
}
