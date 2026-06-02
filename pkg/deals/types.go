package deals

import "time"

// Deal represents a single game discount from any source.
type Deal struct {
	ID          string  // Unique identifier (e.g. "ITAD_xxx" or "CS_xxx")
	Title       string  // Game title
	OldPrice    float64 // Original price
	NewPrice    float64 // Discounted price
	Currency    string  // Currency symbol (€, $, kr)
	Cut         int     // Discount percentage (e.g. 50 for 50%)
	Metacritic  int     // Metacritic score (0 if unknown)
	URL         string  // Link to the deal
	Source      string  // Data source: "ITAD" or "CheapShark"
	RedditQuote string  // One-line description/recommendation from Reddit
	PublishDate time.Time
}
