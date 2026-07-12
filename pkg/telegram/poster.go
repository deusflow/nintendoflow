package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/db"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var telegramHTTPClient = &http.Client{Timeout: 15 * time.Second}

// PostArticle sends an article to the Telegram channel.
// If the article has an image, sendPhoto is used; otherwise sendMessage.
func PostArticle(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	videoURL := strings.TrimSpace(article.VideoURL)
	imageURL := strings.TrimSpace(article.ImageURL)
	sourceURL := strings.TrimSpace(article.SourceURL)

	// Ensure an image exists if all video uploads fail
	if imageURL == "" {
		imageURL = getFallbackImageURL(article.ArticleType)
	}

	markup := buildInlineKeyboard(sourceURL, article.SourceName, videoURL)

	// --- SCENARIO A: WE HAVE A VIDEO ---
	if videoURL != "" {
		// 1. Try to send as actual Native Video
		if strings.Contains(videoURL, "youtube.com") || strings.Contains(videoURL, "youtu.be") {
			// Try to download and stream to Telegram
			stream, size, err := getYouTubeStream(context.Background(), videoURL)
			if err == nil {
				// Telegram bot API limits local files to 50MB
				if size < 49*1024*1024 {
					video := tgbotapi.VideoConfig{
						BaseFile: tgbotapi.BaseFile{
							BaseChat: tgbotapi.BaseChat{ChannelUsername: channelID},
							File:     tgbotapi.FileReader{Name: "video.mp4", Reader: stream},
						},
						Caption:   buildCaption(&article, 1024),
						ParseMode: "HTML",
					}
					if markup != nil {
						video.ReplyMarkup = markup
					}
					_, errSend := bot.Send(video)
					_ = stream.Close()
					if errSend == nil {
						return nil
					} else {
						slog.Warn("youtube video upload failed, fallback to native link preview", "article_id", article.ID, "error", errSend)
						textErr := sendTextWithLinkPreview(bot, channelID, buildCaption(&article, 4096), videoURL, markup)
						if textErr == nil {
							return nil
						}
						slog.Warn("youtube native link preview failed, fallback to photo", "article_id", article.ID, "error", textErr)
					}
				} else {
					_ = stream.Close()
					slog.Info("youtube video too large, fallback to native link preview", "article_id", article.ID, "size", size)
					textErr := sendTextWithLinkPreview(bot, channelID, buildCaption(&article, 4096), videoURL, markup)
					if textErr == nil {
						return nil
					}
					slog.Warn("youtube native link preview failed, fallback to photo", "article_id", article.ID, "error", textErr)
				}
			} else {
				slog.Warn("youtube download failed, fallback to native link preview", "error", err)
				textErr := sendTextWithLinkPreview(bot, channelID, buildCaption(&article, 4096), videoURL, markup)
				if textErr == nil {
					return nil
				}
				slog.Warn("youtube native link preview failed, fallback to photo", "article_id", article.ID, "error", textErr)
			}
		} else {
			// Non-youtube video (direct remote URL mp4)
			video := tgbotapi.VideoConfig{
				BaseFile: tgbotapi.BaseFile{
					BaseChat: tgbotapi.BaseChat{ChannelUsername: channelID},
					File:     tgbotapi.FileURL(videoURL),
				},
				Caption:   buildCaption(&article, 1024),
				ParseMode: "HTML",
			}
			if markup != nil {
				video.ReplyMarkup = markup
			}
			if _, err := bot.Send(video); err == nil {
				return nil
			}
		}
	}

	// --- SCENARIO B: NO VIDEO, OR NATIVE VIDEO FAILED. Send as Photo ---
	photo := tgbotapi.PhotoConfig{
		BaseFile: tgbotapi.BaseFile{
			BaseChat: tgbotapi.BaseChat{ChannelUsername: channelID},
			File:     tgbotapi.FileURL(imageURL),
		},
		Caption:   buildCaption(&article, 1024),
		ParseMode: "HTML",
	}
	if markup != nil {
		photo.ReplyMarkup = markup
	}
	if _, err := bot.Send(photo); err == nil {
		return nil
	} else {
		slog.Warn("telegram publish send photo failed", "article_id", article.ID, "image", imageURL, "error", err)
	}

	// --- SCENARIO C: ABSOLUTE LAST RESORT ---
	msg := tgbotapi.NewMessageToChannel(channelID, buildCaption(&article, 4096))
	msg.ParseMode = "HTML"
	if markup != nil {
		msg.ReplyMarkup = markup
	}
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("telegram sendMessage: %w", err)
	}
	return nil
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



func sendTextWithLinkPreview(bot *tgbotapi.BotAPI, chatID string, text string, previewURL string, markup *tgbotapi.InlineKeyboardMarkup) error {
	type rawLinkPreview struct {
		IsDisabled       bool   `json:"is_disabled"`
		URL              string `json:"url,omitempty"`
		PreferLargeMedia bool   `json:"prefer_large_media,omitempty"`
		ShowAboveText    bool   `json:"show_above_text,omitempty"`
	}
	type rawSendMessageReq struct {
		ChatID             string                         `json:"chat_id"`
		Text               string                         `json:"text"`
		ParseMode          string                         `json:"parse_mode,omitempty"`
		LinkPreviewOptions *rawLinkPreview                `json:"link_preview_options,omitempty"`
		ReplyMarkup        *tgbotapi.InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}

	req := rawSendMessageReq{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
		LinkPreviewOptions: &rawLinkPreview{
			IsDisabled:       false,
			URL:              previewURL,
			PreferLargeMedia: true,
			ShowAboveText:    true,
		},
		ReplyMarkup: markup,
	}

	reqBody, _ := json.Marshal(req)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", bot.Token)
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := telegramHTTPClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram raw send failed %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func buildInlineKeyboard(sourceURL, sourceName, videoURL string) *tgbotapi.InlineKeyboardMarkup {
	var buttons []tgbotapi.InlineKeyboardButton

	if sourceURL != "" {
		name := sourceName
		if name == "" {
			name = "Джерело"
		} else {
			name = "🔗 " + name
		}
		buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonURL(name, sourceURL))
	}

	if videoURL != "" {
		buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonURL("🎥 Дивитися відео", videoURL))
	}

	if len(buttons) == 0 {
		return nil
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	if len(buttons) > 0 {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{buttons[0]})
	}
	if len(buttons) > 1 {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{buttons[1]})
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return &markup
}

// buildCaption forms the final post text based on source and article type.
func buildCaption(article *db.Article, maxLen int) string {
	var prefix string
	switch article.SourceType {
	case "official":
		prefix = "📢 <b>ОФІЦІЙНО</b>\n\n"
	case "insider":
		prefix = "🕵️ <i>Інсайд</i>\n\n"
	case "highlight":
		prefix = "⭐️ <b>ШЕДЕВР ДНЯ</b>\n\n"
	default:
		// For aggregators and other sources, use the AI-determined article type.
		switch article.ArticleType {
		case "rumor":
			prefix = "🤫 <i>Чутки</i>\n\n"
		case "insight":
			prefix = "🔍 <i>Інсайт</i>\n\n"
		}
	}

	body := stripSourceFooter(article.BodyUA)

	full := prefix + body

	// Leave space for ellipsis just in case
	reserve := 3

	runes := []rune(full)
	var finalBody string
	if len(runes) <= maxLen-reserve {
		finalBody = full
	} else {
		bodyRunes := []rune(body)
		allowed := maxLen - reserve - len([]rune(prefix))
		if allowed > 0 && allowed < len(bodyRunes) {
			finalBody = prefix + string(bodyRunes[:allowed]) + "..."
		} else {
			finalBody = string(runes[:maxLen-reserve]) + "..."
		}
	}

	return finalBody
}

// stripSourceFooter removes stray inline-link lines (🔗) left from older
// bot versions. "Джерело:" lines are now generated by the AI and are kept.
func stripSourceFooter(body string) string {
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "🔗") {
			continue
		}
		if lower == "preview" || strings.HasPrefix(lower, "title:") || strings.HasPrefix(lower, "source:") || strings.HasPrefix(lower, "score:") {
			continue
		}
		if strings.HasPrefix(lower, "<b>title:</b>") || strings.HasPrefix(lower, "<b>source:</b>") || strings.HasPrefix(lower, "<b>score:</b>") {
			continue
		}
		if strings.Contains(lower, "original link") {
			continue
		}
		kept = append(kept, line)
	}
	clean := strings.TrimSpace(strings.Join(kept, "\n"))
	clean = strings.TrimPrefix(clean, "<b>Preview</b>")
	return strings.TrimSpace(clean)
}
