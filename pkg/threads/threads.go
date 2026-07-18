package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/db"
)

type ContainerResponse struct {
	ID string `json:"id"`
}

type PublishResponse struct {
	ID string `json:"id"`
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// PostThread publishes a text thread to the Meta Threads API.
func PostThread(ctx context.Context, text string) (string, error) {
	accessToken := strings.TrimSpace(os.Getenv("THREADS_ACCESS_TOKEN"))
	if accessToken == "" {
		return "", fmt.Errorf("missing THREADS_ACCESS_TOKEN in environment")
	}

	// Step 1: Create a container for the text post
	apiURL := "https://graph.threads.net/v1.0/me/threads"
	
	q := url.Values{}
	q.Set("media_type", "TEXT")
	q.Set("text", text)
	q.Set("access_token", accessToken)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("create container request build: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create container execute: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errData map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errData)
		return "", fmt.Errorf("create container returned status %d: %v", resp.StatusCode, errData)
	}

	var cr ContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("decode container response: %w", err)
	}

	containerID := cr.ID
	if containerID == "" {
		return "", fmt.Errorf("empty container ID returned by Threads API")
	}

	// Step 2: Publish the container
	publishURL := "https://graph.threads.net/v1.0/me/threads_publish"
	pq := url.Values{}
	pq.Set("creation_id", containerID)
	pq.Set("access_token", accessToken)

	publishReq, err := http.NewRequestWithContext(ctx, "POST", publishURL+"?"+pq.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("publish request build: %w", err)
	}

	publishResp, err := httpClient.Do(publishReq)
	if err != nil {
		return "", fmt.Errorf("publish execute: %w", err)
	}
	defer func() { _ = publishResp.Body.Close() }()

	if publishResp.StatusCode != http.StatusOK {
		var errData map[string]interface{}
		_ = json.NewDecoder(publishResp.Body).Decode(&errData)
		return "", fmt.Errorf("publish returned status %d: %v", publishResp.StatusCode, errData)
	}

	var pr PublishResponse
	if err := json.NewDecoder(publishResp.Body).Decode(&pr); err != nil {
		return "", fmt.Errorf("decode publish response: %w", err)
	}

	return pr.ID, nil
}

// htmlTagRe matches any HTML tag including self-closing and tags with attributes.
var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// stripHTML removes all HTML tags and normalizes whitespace for Threads (plain text only).
func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	// Normalize excessive newlines that may result from removed block tags.
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// FormatThread formats the article content into a Threads post.
// Respects the 500-character limit of Threads and implements smart content length strategy.
func FormatThread(article db.Article, tgChannelUsername string, tgMessageID int) string {
	var tgLink string
	if tgChannelUsername != "" && tgMessageID > 0 {
		username := strings.TrimPrefix(tgChannelUsername, "@")
		tgLink = fmt.Sprintf("https://t.me/%s/%d", username, tgMessageID)
	}

	if article.BodyThreads != "" {
		bodyClean := stripHTML(article.BodyThreads)
		var suffix string
		if tgLink != "" {
			suffix = fmt.Sprintf("\n\n👉 %s", tgLink)
		}

		bodyRunes := []rune(bodyClean)
		suffixRunes := []rune(suffix)
		maxBodyRunes := 500 - len(suffixRunes)

		if len(bodyRunes) > maxBodyRunes {
			if maxBodyRunes > 3 {
				bodyClean = string(bodyRunes[:maxBodyRunes-3]) + "..."
			} else {
				bodyClean = string(bodyRunes[:maxBodyRunes])
			}
		}

		return bodyClean + suffix
	}

	hashtag := "#Nintendo"

	// Scenario 1: Highlight post (Masterpiece) -> Too long for a single post. Post a premium teaser.
	if article.SourceType == "highlight" {
		titleClean := stripHTML(article.TitleRaw)
		return fmt.Sprintf("⭐️ %s — легендарний шедевр від Nintendo!\n\nУ нашому Telegram-каналі вийшла детальна історія створення цієї гри, її секрети та шлях до оцінки 95+ на Metacritic. Читайте повну історію за посиланням:\n\n👉 %s\n\n%s", 
			titleClean, tgLink, hashtag)
	}

	// Scenario 2: Deals Digest -> Post a custom teaser for discounts
	if article.ArticleType == "deals" || article.SourceType == "deals" {
		return fmt.Sprintf("🛒 Свіжі знижки в Nintendo eShop!\n\nЗібрали найкращі пропозиції на ігри для Switch з Metacritic 80+ та реферальними картками поповнення. Переглядайте весь список та купуйте вигідно за посиланням:\n\n👉 %s\n\n%s", 
			tgLink, hashtag)
	}

	// Scenario 3: Regular News -> Try to post the full text if it fits in 500 characters
	bodyClean := stripHTML(article.BodyUA)
	prefix := "🎮 "
	
	var suffix string
	if tgLink != "" {
		suffix = fmt.Sprintf("\n\nЧитати далі: %s\n\n%s", tgLink, hashtag)
	} else {
		suffix = "\n\n" + hashtag
	}

	totalRunes := len([]rune(prefix)) + len([]rune(bodyClean)) + len([]rune(suffix))
	if totalRunes <= 500 {
		return prefix + bodyClean + suffix
	}

	// Scenario 4: News is too long -> Post a teaser with Title and direct link
	titleClean := stripHTML(article.TitleRaw)
	titleRunes := []rune(titleClean)
	// Safe budget for title: 500 - 30 - 30 = 440 chars
	if len(titleRunes) > 400 {
		titleClean = string(titleRunes[:397]) + "..."
	}

	if tgLink != "" {
		return fmt.Sprintf("🎮 %s\n\nЧитати далі у нашому Telegram: %s\n\n%s", titleClean, tgLink, hashtag)
	}
	
	return fmt.Sprintf("🎮 %s\n\n%s", titleClean, hashtag)
}

// MaybeCrossPost posts a thread for the given article if Threads credentials are set.
func MaybeCrossPost(ctx context.Context, article db.Article, messageID int) error {
	accessToken := strings.TrimSpace(os.Getenv("THREADS_ACCESS_TOKEN"))
	if accessToken == "" {
		slog.Debug("threads: access token is empty, skipping cross-post")
		return fmt.Errorf("THREADS_ACCESS_TOKEN is missing or empty")
	}

	tgUsername := os.Getenv("TELEGRAM_CHANNEL_USERNAME")
	if tgUsername == "" {
		tgUsername = "deusflow"
	}

	threadText := FormatThread(article, tgUsername, messageID)
	slog.Info("threads: preparing to cross-post to Threads", "text", threadText)

	postID, err := PostThread(ctx, threadText)
	if err != nil {
		slog.Error("threads: cross-post failed", "error", err)
		return fmt.Errorf("threads API error: %w", err)
	}

	slog.Info("threads: successfully cross-posted to Threads", "post_id", postID)
	return nil
}
