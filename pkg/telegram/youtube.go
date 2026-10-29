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

	// Attempt 1: Full trailer in H.264 (avc1) + AAC (mp4a) up to 720p under 48MB,
	// with -movflags +faststart so Telegram mobile/desktop streams inline with duration badge.
	fullArgs := []string{
		"--no-playlist",
		"--no-warnings",
		"--geo-bypass",
		"--extractor-args", "youtube:player_client=android,web",
		"--max-filesize", "48M",
		"-S", "res:720,ext:mp4:m4a,vcodec:h264,acodec:aac",
		"-f", "bestvideo[ext=mp4][vcodec^=avc1][height<=720][filesize<40M]+bestaudio[ext=m4a][filesize<8M]/bestvideo[ext=mp4][vcodec^=avc1][height<=720][filesize_approx<40M]+bestaudio[ext=m4a]/best[ext=mp4][height<=720]/18/best[ext=mp4]",
		"--merge-output-format", "mp4",
		"--postprocessor-args", "ffmpeg:-movflags +faststart",
		"-o", tmpPath,
		videoURL,
	}

	dlCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
	out, runErr := exec.CommandContext(dlCtx, ytDlpBin, fullArgs...).CombinedOutput()
	cancel()

	data, readErr := os.ReadFile(tmpPath)
	if runErr != nil || readErr != nil || len(data) == 0 || int64(len(data)) > maxVideoBytes {
		_ = os.Remove(tmpPath)
		slog.Warn("yt-dlp full video attempt exceeded limit or failed, trying 90s highlight clip",
			"url", videoURL,
			"error", runErr,
			"output", strings.TrimSpace(string(out)),
		)

		// Attempt 2: If the video is a long showcase/Direct (>48MB), grab the first 90 seconds in H.264 MP4
		clipArgs := []string{
			"--no-playlist",
			"--no-warnings",
			"--geo-bypass",
			"--extractor-args", "youtube:player_client=android,web",
			"--download-sections", "*0:00-1:30",
			"-S", "res:720,ext:mp4:m4a,vcodec:h264,acodec:aac",
			"-f", "bestvideo[ext=mp4][vcodec^=avc1][height<=720]+bestaudio[ext=m4a]/18/best[ext=mp4]",
			"--merge-output-format", "mp4",
			"--postprocessor-args", "ffmpeg:-movflags +faststart",
			"-o", tmpPath,
			videoURL,
		}
		clipCtx, clipCancel := context.WithTimeout(ctx, 50*time.Second)
		clipOut, clipErr := exec.CommandContext(clipCtx, ytDlpBin, clipArgs...).CombinedOutput()
		clipCancel()
		if clipErr != nil {
			slog.Warn("yt-dlp clip download attempt failed", "url", videoURL, "error", clipErr, "output", strings.TrimSpace(string(clipOut)))
			return nil, 0, clipErr
		}
		data, readErr = os.ReadFile(tmpPath)
		if readErr != nil || len(data) == 0 {
			return nil, 0, fmt.Errorf("yt-dlp produced empty file: %w", readErr)
		}
	}

	if int64(len(data)) > maxVideoBytes {
		return nil, 0, fmt.Errorf("yt-dlp file exceeds 50MB (%d bytes)", len(data))
	}

	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

