package deals

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SolrResponse represents the search response from Nintendo of Europe Solr API.
type SolrResponse struct {
	Response struct {
		Docs []struct {
			FSID            string   `json:"fs_id"`
			Title           string   `json:"title"`
			PriceDiscounted float64  `json:"price_discounted_f"`
			PriceRegular    float64  `json:"price_regular_f"`
			PriceCut        float64  `json:"price_discount_percentage_f"`
			URL             string   `json:"url"`
			NSUIDTxt        []string `json:"nsuid_txt"`
		} `json:"docs"`
	} `json:"response"`
}

// FetchNintendoOfficialDeals retrieves discounted Switch games directly from Nintendo Europe.
func FetchNintendoOfficialDeals() ([]Deal, error) {
	// Querying discounted Nintendo Switch games, format JSON, limited to 150 rows.
	apiURL := "https://search.nintendo-europe.com/en/select?q=*&fq=type:GAME%20AND%20price_has_discount_b:true%20AND%20system_type:nintendoswitch*&wt=json&rows=150"

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("nintendo official request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nintendo official returned status %d", resp.StatusCode)
	}

	var parsed SolrResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("nintendo official decode: %w", err)
	}

	var results []Deal
	for _, doc := range parsed.Response.Docs {
		if doc.Title == "" || doc.PriceRegular <= 0 {
			continue
		}

		dealURL := doc.URL
		if len(dealURL) > 0 && dealURL[0] == '/' {
			dealURL = "https://www.nintendo.com" + dealURL
		}

		id := doc.FSID
		if id == "" {
			id = doc.URL
		}

		results = append(results, Deal{
			ID:         "NOE_" + id,
			Title:      doc.Title,
			OldPrice:   doc.PriceRegular,
			NewPrice:   doc.PriceDiscounted,
			Currency:   "€",
			Cut:        int(doc.PriceCut),
			Metacritic: 0, // Nintendo API doesn't provide Metacritic scores
			URL:        dealURL,
			Source:     "NintendoOfficial",
		})
	}

	return results, nil
}
