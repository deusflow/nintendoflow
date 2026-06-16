package deals

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ITAD API v2 response shape (simplified to the fields we use).
type itadResponse struct {
	List []itadDeal `json:"list"`
}

type itadDeal struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Deal  struct {
		Price struct {
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"price"`
		Regular struct {
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"regular"`
		Cut int `json:"cut"`
		URL string `json:"url"`
	} `json:"deal"`
	Reviews *struct {
		Score int `json:"score"`
	} `json:"reviews"`
}

// FetchITADDeals returns raw deals from IsThereAnyDeal API.
// shop 61 = Nintendo eShop, country = DK (Denmark for EUR pricing).
func FetchITADDeals(apiKey string) ([]Deal, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("ITAD API key is empty")
	}

	apiURL := fmt.Sprintf(
		"https://api.isthereanydeal.com/deals/v2?key=%s&country=DK&shops=61&limit=50",
		apiKey,
	)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("ITAD request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ITAD returned %d", resp.StatusCode)
	}

	var parsed itadResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("ITAD decode: %w", err)
	}

	var results []Deal
	for _, item := range parsed.List {
		meta := 0
		if item.Reviews != nil {
			meta = item.Reviews.Score
		}

		currency := item.Deal.Price.Currency
		switch currency {
		case "EUR":
			currency = "€"
		case "USD":
			currency = "$"
		case "DKK":
			currency = "kr"
		}

		dealURL := item.Deal.URL
		if dealURL == "" {
			dealURL = fmt.Sprintf("https://isthereanydeal.com/game/%s/info/", item.Slug)
		}

		results = append(results, Deal{
			ID:         "ITAD_" + item.ID,
			Title:      item.Title,
			OldPrice:   item.Deal.Regular.Amount,
			NewPrice:   item.Deal.Price.Amount,
			Currency:   currency,
			Cut:        item.Deal.Cut,
			Metacritic: meta,
			URL:        dealURL,
			Source:     "ITAD",
		})
	}

	return results, nil
}
