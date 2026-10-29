package deals

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// IsDealRecentlyPublished checks if a deal_id was posted within the last N days.
func IsDealRecentlyPublished(ctx context.Context, db *sql.DB, dealID string, days int) (bool, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM deal_history
			WHERE deal_id = $1 AND posted_at > $2
		)`, dealID, cutoff).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// MarkDealPublished inserts a deal into the history table.
func MarkDealPublished(ctx context.Context, db *sql.DB, d Deal) error {
	region := d.Region
	if region == "" {
		region = "EU"
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO deal_history (deal_id, title, old_price, new_price, currency, region, cut, metacritic, url, source, reddit_quote)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (deal_id) DO UPDATE SET
			posted_at = NOW(),
			cut = EXCLUDED.cut,
			new_price = EXCLUDED.new_price,
			region = EXCLUDED.region`,
		d.ID, d.Title, d.OldPrice, d.NewPrice, d.Currency, region, d.Cut, d.Metacritic, d.URL, d.Source, d.RedditQuote)
	return err
}

// EncodeDealsMetadata serializes deals into a compact JSON string for storing in article.EventTag
// while the deals digest is pending moderation.
func EncodeDealsMetadata(list []Deal) string {
	if len(list) == 0 {
		return ""
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return ""
	}
	return "deals_json:" + string(raw)
}

// MarkDealsPublishedFromMetadata decodes pending deals metadata and marks each deal as published in deal_history.
func MarkDealsPublishedFromMetadata(ctx context.Context, db *sql.DB, metadata string) error {
	metadata = strings.TrimSpace(metadata)
	if !strings.HasPrefix(metadata, "deals_json:") {
		return nil
	}
	raw := strings.TrimPrefix(metadata, "deals_json:")
	var list []Deal
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return err
	}
	for _, d := range list {
		if err := MarkDealPublished(ctx, db, d); err != nil {
			return err
		}
	}
	return nil
}

