package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type metaAPIError struct {
	Error struct {
		Message      string `json:"message"`
		Type         string `json:"type"`
		Code         int    `json:"code"`
		ErrorSubcode int    `json:"error_subcode"`
		FBTraceID    string `json:"fbtrace_id"`
	} `json:"error"`
}

func parseMetaError(body []byte, statusCode int) error {
	var metaErr metaAPIError
	if err := json.Unmarshal(body, &metaErr); err == nil && metaErr.Error.Message != "" {
		switch metaErr.Error.Code {
		case 190:
			return fmt.Errorf("Meta token expired or invalid (OAuth 190, subcode %d): %s. Please generate a fresh THREADS_ACCESS_TOKEN and update it in Vercel / GitHub secrets",
				metaErr.Error.ErrorSubcode, metaErr.Error.Message)
		case 100:
			return fmt.Errorf("Meta parameter/permission error (Code 100, subcode %d): %s. Ensure token has threads_content_publish scope",
				metaErr.Error.ErrorSubcode, metaErr.Error.Message)
		default:
			return fmt.Errorf("Meta API error (HTTP %d, Code %d, Type %s): %s",
				statusCode, metaErr.Error.Code, metaErr.Error.Type, metaErr.Error.Message)
		}
	}
	return fmt.Errorf("Meta API returned HTTP %d: %s", statusCode, string(body))
}

func getThreadsUserID(ctx context.Context, accessToken string) string {
	if envID := strings.TrimSpace(os.Getenv("THREADS_USER_ID")); envID != "" {
		return envID
	}
	// Fetch actual user ID via /me to avoid Code 100 "Object with ID 'me' does not exist"
	req, err := http.NewRequestWithContext(ctx, "GET", "https://graph.threads.net/v1.0/me?fields=id", nil)
	if err == nil {
		req.Header.Set("Authorization", "Bearer "+accessToken)
		resp, err := httpClient.Do(req)
		if err == nil {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode == http.StatusOK {
				var data struct {
					ID string `json:"id"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&data); err == nil && data.ID != "" {
					return data.ID
				}
			}
		}
	}
	return "me"
}

type containerStatusResp struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
}

func waitForContainer(ctx context.Context, containerID, accessToken string) error {
	statusURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s?fields=status,error_message&access_token=%s",
		url.PathEscape(containerID), url.QueryEscape(accessToken))

	for attempt := 1; attempt <= 5; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt*400) * time.Millisecond):
		}

		req, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}
		var cs containerStatusResp
		decodeErr := json.NewDecoder(resp.Body).Decode(&cs)
		_ = resp.Body.Close()
		if decodeErr != nil {
			continue
		}

		switch cs.Status {
		case "FINISHED", "PUBLISHED":
			return nil
		case "ERROR":
			if cs.ErrorMessage != "" {
				return fmt.Errorf("threads container failed: %s", cs.ErrorMessage)
			}
			return fmt.Errorf("threads media container processing failed")
		case "EXPIRED":
			return fmt.Errorf("threads container expired")
		case "IN_PROGRESS":
			// wait for next attempt
		}
	}
	return nil
}

// PostThread publishes a text thread to the Meta Threads API.
func PostThread(ctx context.Context, text string) (string, error) {
	accessToken := strings.TrimSpace(os.Getenv("THREADS_ACCESS_TOKEN"))
	if accessToken == "" {
		return "", fmt.Errorf("missing THREADS_ACCESS_TOKEN in environment. Check Vercel/GitHub secrets")
	}

	userID := getThreadsUserID(ctx, accessToken)

	// Step 1: Create a container for the text post
	apiURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", userID)

	q := url.Values{}
	q.Set("media_type", "TEXT")
	q.Set("text", text)
	q.Set("access_token", accessToken)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(q.Encode()))
	if err != nil {
		return "", fmt.Errorf("create container request build: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create container execute: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", parseMetaError(bodyBytes, resp.StatusCode)
	}

	var cr ContainerResponse
	if err := json.Unmarshal(bodyBytes, &cr); err != nil {
		return "", fmt.Errorf("decode container response: %w", err)
	}

	containerID := cr.ID
	if containerID == "" {
		return "", fmt.Errorf("empty container ID returned by Threads API")
	}

	// Step 1.5: Poll container status until ready
	if err := waitForContainer(ctx, containerID, accessToken); err != nil {
		return "", err
	}

	// Step 2: Publish the container
	publishURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish", userID)
	pq := url.Values{}
	pq.Set("creation_id", containerID)
	pq.Set("access_token", accessToken)

	publishReq, err := http.NewRequestWithContext(ctx, "POST", publishURL, strings.NewReader(pq.Encode()))
	if err != nil {
		return "", fmt.Errorf("publish request build: %w", err)
	}
	publishReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	publishReq.Header.Set("Authorization", "Bearer "+accessToken)

	publishResp, err := httpClient.Do(publishReq)
	if err != nil {
		return "", fmt.Errorf("publish execute: %w", err)
	}
	defer func() { _ = publishResp.Body.Close() }()

	publishBody, _ := io.ReadAll(publishResp.Body)
	if publishResp.StatusCode != http.StatusOK {
		return "", parseMetaError(publishBody, publishResp.StatusCode)
	}

	var pr PublishResponse
	if err := json.Unmarshal(publishBody, &pr); err != nil {
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
		ch := strings.TrimSpace(os.Getenv("TELEGRAM_CHANNEL_ID"))
		if ch == "" {
			ch = strings.TrimSpace(os.Getenv("TEST_CHANNEL_ID"))
		}
		if strings.HasPrefix(ch, "@") {
			tgUsername = strings.TrimPrefix(ch, "@")
		}
	}
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
