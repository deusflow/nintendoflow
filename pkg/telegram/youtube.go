package telegram

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kkdai/youtube/v2"
)

const (
	maxVideoBytes     = 50 << 20 // 50 MB — Telegram upload limit
	cachedVideoPrefix = "tgfile:"
)

// EncodeCachedVideoURL packs a Telegram file_id together with the original video URL
// so the webhook can publish the pre-uploaded video instantaneously while keeping the URL for buttons.
func EncodeCachedVideoURL(fileID, rawURL string) string {
	fileID = strings.TrimSpace(fileID)
	rawURL = strings.TrimSpace(rawURL)
	if fileID == "" {
		return rawURL
	}
	return cachedVideoPrefix + fileID + "|" + rawURL
}

// SplitCachedVideoURL unpacks an encoded video_url field into (telegramFileID, originalVideoURL).
func SplitCachedVideoURL(videoField string) (fileID string, rawURL string) {
	videoField = strings.TrimSpace(videoField)
	if strings.HasPrefix(videoField, cachedVideoPrefix) {
		rest := strings.TrimPrefix(videoField, cachedVideoPrefix)
		if idx := strings.Index(rest, "|"); idx != -1 {
			return strings.TrimSpace(rest[:idx]), strings.TrimSpace(rest[idx+1:])
		}
		return strings.TrimSpace(rest), ""
	}
	return "", videoField
}

// getYouTubeStream finds and downloads a YouTube video as a memory stream.
// It tries yt-dlp first (when available on GitHub Actions) for maximum reliability with 2026 YouTube streams,
// and falls back to the native Go youtube client.
func getYouTubeStream(ctx context.Context, videoURL string) (io.ReadCloser, int64, error) {
	if stream, size, err := getYouTubeStreamViaYtDlp(ctx, videoURL); err == nil && size > 0 {
		slog.Info("youtube video downloaded via yt-dlp", "url", videoURL, "size_bytes", size)
		return stream, size, nil
	}

	client := youtube.Client{}

	vid, err := client.GetVideoContext(ctx, videoURL)
	if err != nil {
		return nil, 0, err
	}

	formats := vid.Formats.WithAudioChannels()
	var selected *youtube.Format

	// Prefer 720p, 480p, or 360p MP4 with audio to stay well under Telegram's 50MB limit
	for _, pref := range []string{"720p", "480p", "360p"} {
		for i := range formats {
			f := formats[i]
			if strings.Contains(f.MimeType, "video/mp4") && f.AudioChannels > 0 && strings.Contains(f.QualityLabel, pref) {
				if f.ContentLength == 0 || f.ContentLength <= maxVideoBytes {
					selected = &f
					break
				}
			}
		}
		if selected != nil {
			break
		}
	}

	// Fallback to any MP4 available with audio under 50MB
	if selected == nil {
		for i := range formats {
			f := formats[i]
			if strings.Contains(f.MimeType, "video/mp4") && f.AudioChannels > 0 {
				if f.ContentLength == 0 || f.ContentLength <= maxVideoBytes {
					selected = &f
					break
				}
			}
		}
	}

	if selected == nil {
		return nil, 0, fmt.Errorf("no suitable mp4 format with audio found")
	}

	if selected.ContentLength > 0 && selected.ContentLength > maxVideoBytes {
		return nil, 0, fmt.Errorf("video too large: %d bytes (limit %d)", selected.ContentLength, maxVideoBytes)
	}

	stream, _, err := client.GetStreamContext(ctx, vid, selected)
	if err != nil {
		return nil, 0, fmt.Errorf("get stream: %w", err)
	}

	limitReader := io.LimitReader(stream, maxVideoBytes)
	buf := new(bytes.Buffer)

	readCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(buf, limitReader)
		_ = stream.Close()
		errCh <- err
	}()

	select {
	case <-readCtx.Done():
		_ = stream.Close()
		return nil, 0, fmt.Errorf("youtube download timed out: %w", readCtx.Err())
	case err := <-errCh:
		if err != nil && err != io.EOF {
			return nil, 0, fmt.Errorf("youtube download error: %w", err)
		}
	}

	bReader := bytes.NewReader(buf.Bytes())
	return io.NopCloser(bReader), int64(bReader.Len()), nil
}

func getYouTubeStreamViaYtDlp(ctx context.Context, videoURL string) (io.ReadCloser, int64, error) {
	ytDlpBin, err := exec.LookPath("yt-dlp")
	if err != nil {
		return nil, 0, err
	}

	tmpFile, err := os.CreateTemp("", "nf-yt-*.mp4")
	if err != nil {
		return nil, 0, err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpPath)
	defer func() { _ = os.Remove(tmpPath) }()

	dlCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	// Select best single-file or merged mp4 up to 720p under 48MB
	cmd := exec.CommandContext(dlCtx, ytDlpBin,
		"--no-playlist",
		"--no-warnings",
		"--max-filesize", "48M",
		"-f", "best[ext=mp4][height<=720][filesize<48M]/best[ext=mp4][height<=720]/18/best[ext=mp4]",
		"--merge-output-format", "mp4",
		"-o", tmpPath,
		videoURL,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Debug("yt-dlp download attempt failed", "error", err, "output", string(out))
		return nil, 0, err
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil || len(data) == 0 {
		return nil, 0, fmt.Errorf("yt-dlp produced empty file: %w", err)
	}
	if int64(len(data)) > maxVideoBytes {
		return nil, 0, fmt.Errorf("yt-dlp file exceeds 50MB (%d bytes)", len(data))
	}

	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

