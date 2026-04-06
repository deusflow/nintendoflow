package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/deuswork/nintendoflow/pkg/db"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// PostArticle sends an article to the Telegram channel.
// If the article has an image, sendPhoto is used; otherwise sendMessage.
func PostArticle(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	videoURL := strings.TrimSpace(article.VideoURL)
	imageURL := strings.TrimSpace(article.ImageURL)

	// If there is no image and no video, use a fallback image based on article type.
	if imageURL == "" && videoURL == "" {
		imageURL = getFallbackImageURL(article.ArticleType)
	}

	// If we have an image (or fallback image), but no video, send as Photo.
	// If we have BOTH an image and a video, we prioritize the video link preview for YouTube.
	if videoURL == "" && imageURL != "" {
		photo := tgbotapi.PhotoConfig{
			BaseFile: tgbotapi.BaseFile{
				BaseChat: tgbotapi.BaseChat{ChannelUsername: channelID},
				File:     tgbotapi.FileURL(imageURL),
			},
			Caption:   buildCaption(&article, 1024),
			ParseMode: "HTML",
		}
		if _, err := bot.Send(photo); err == nil {
			return nil
		} else {
			slog.Warn("telegram publish send photo failed",
				"mode", "publish",
				"article_id", article.ID,
				"image_url", imageURL,
				"error", err,
			)
			// fallback to text below
		}
	}

	if videoURL != "" {
		// First try sending as native video (if it's a direct mp4)
		video := tgbotapi.VideoConfig{
			BaseFile: tgbotapi.BaseFile{
				BaseChat: tgbotapi.BaseChat{ChannelUsername: channelID},
				File:     tgbotapi.FileURL(videoURL),
			},
			Caption:   buildCaption(&article, 1024),
			ParseMode: "HTML",
		}
		if _, err := bot.Send(video); err == nil {
			return nil
		}

		// If native video failed (e.g. YouTube), send as a text message with LinkPreview.
		if err := sendTextWithVideoLink(bot, channelID, article); err == nil {
			return nil
		} else {
			slog.Warn("telegram publish send video link fallback failed",
				"mode", "publish",
				"article_id", article.ID,
				"video_url", videoURL,
				"error", err,
			)
		}
	}

	// Last resort: just send text
	return sendText(bot, channelID, article)
}

func getFallbackImageURL(articleType string) string {
	baseURL := "https://deusflow.github.io/nintendoflow/assets/placeholders"
	switch articleType {
	case "insight", "инсайт":
		return baseURL + "/news-fallback-16x9.webp"
	case "news", "новость", "факт":
		return baseURL + "/newstwo-fallback-16x9.webp"
	case "rumor", "слух", "слухи", "чутка":
		return baseURL + "/card-fallback-16x9.webp"
	case "offtop":
		return baseURL + "/offtop-fallback-16x9.webp"
	default:
		return baseURL + "/newstwo-fallback-16x9.webp"
	}
}

func sendTextWithVideoLink(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	caption := buildCaption(&article, 3800)
	videoURL := strings.TrimSpace(article.VideoURL)

	// Create an invisible link to the video if we don't want it to clutter the text,
	// BUT since zero-width spaces fail in some Telegram clients, we just append it
	// clearly at the bottom.
	text := caption + "\n\n<a href=\"" + escapeHTML(videoURL) + "\">🎥 Відео до новини</a>"

	type linkPreviewOptions struct {
		IsDisabled       bool   `json:"is_disabled"`
		URL              string `json:"url"`
		PreferLargeMedia bool   `json:"prefer_large_media"`
		ShowAboveText    bool   `json:"show_above_text"`
	}
	type payload struct {
		ChatID             string             `json:"chat_id"`
		Text               string             `json:"text"`
		ParseMode          string             `json:"parse_mode"`
		LinkPreviewOptions linkPreviewOptions `json:"link_preview_options"`
	}

	body, err := json.Marshal(payload{
		ChatID:    channelID,
		Text:      text,
		ParseMode: "HTML",
		LinkPreviewOptions: linkPreviewOptions{
			IsDisabled:       false,
			URL:              videoURL,
			PreferLargeMedia: true,
			ShowAboveText:    true,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", bot.Token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram sendMessage with video link: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram api error %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func sendText(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	msg := tgbotapi.NewMessageToChannel(channelID, buildCaption(&article, 4096))
	msg.ParseMode = "HTML"
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("telegram sendMessage: %w", err)
	}
	return nil
}

// buildCaption forms the final post text based on source type.
func buildCaption(article *db.Article, maxLen int) string {
	var prefix string
	switch article.SourceType {
	case "official":
		prefix = "📢 <b>ОФІЦІЙНО</b>\n\n"
	case "insider":
		prefix = "🕵️ <i>Інсайд</i>\n\n"
	case "aggregator":
		prefix = "📡 <i>Чутки</i>\n\n"
	default:
		prefix = ""
	}

	body := stripSourceFooter(article.BodyUA)
	full := prefix + body
	runes := []rune(full)
	if len(runes) <= maxLen {
		return full
	}

	bodyRunes := []rune(body)
	allowed := maxLen - len([]rune(prefix)) - 3
	if allowed > 0 && allowed < len(bodyRunes) {
		return prefix + string(bodyRunes[:allowed]) + "..."
	}
	return string(runes[:maxLen])
}

// stripSourceFooter removes stray inline-link lines (🔗) left from older
// bot versions. "Джерело:" lines are now generated by the AI and are kept.
func stripSourceFooter(body string) string {
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "🔗") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
