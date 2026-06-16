package deals

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// CheapShark response shape
type cheapsharkDeal struct {
	Title           string `json:"title"`
	SalePrice       string `json:"salePrice"`
	NormalPrice     string `json:"normalPrice"`
	Savings         string `json:"savings"`
	MetacriticScore string `json:"metacriticScore"`
	GameID          string `json:"gameID"`
	DealID          string `json:"dealID"`
}

// FetchCheapSharkDeals returns filtered deals from CheapShark (fallback source).
// storeID=23 corresponds to Nintendo eShop in CheapShark's database.
func FetchCheapSharkDeals() ([]Deal, error) {
	apiURL := "https://www.cheapshark.com/api/1.0/deals?storeID=23&sortBy=Savings&pageSize=50"

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("cheapshark request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cheapshark returned %d", resp.StatusCode)
	}

	var raw []cheapsharkDeal
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("cheapshark decode: %w", err)
	}

	var results []Deal
	for _, cd := range raw {
		cutFloat, _ := strconv.ParseFloat(cd.Savings, 64)
		cut := int(cutFloat)
		meta, _ := strconv.Atoi(cd.MetacriticScore)
		oldPrice, _ := strconv.ParseFloat(cd.NormalPrice, 64)
		newPrice, _ := strconv.ParseFloat(cd.SalePrice, 64)

		results = append(results, Deal{
			ID:         "CS_" + cd.GameID,
			Title:      cd.Title,
			OldPrice:   oldPrice,
			NewPrice:   newPrice,
			Currency:   "$",
			Cut:        cut,
			Metacritic: meta,
			URL:        "https://www.cheapshark.com/redirect?dealID=" + cd.DealID,
			Source:     "CheapShark",
		})
	}

	return results, nil
}
