package telegram

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kkdai/youtube/v2"
)

// getYouTubeStream finds and downloads a YouTube video as a memory stream.
func getYouTubeStream(ctx context.Context, videoURL string) (io.ReadCloser, int64, error) {
	client := youtube.Client{}

	vid, err := client.GetVideoContext(ctx, videoURL)
	if err != nil {
		return nil, 0, err
	}

	formats := vid.Formats.WithAudioChannels()
	var selected *youtube.Format

	// We want MP4 formatting, ideally 720p or lower, to stay under Telegram's 50MB limit
	// Wait, kkdai sorts formats from best to worst if we sort it. Let's iterate manually:
	for i := range formats {
		f := formats[i]
		if strings.Contains(f.MimeType, "video/mp4") && f.AudioChannels > 0 {
			// Find a reasonable quality string (e.g. 720p or 360p)
			if strings.Contains(f.QualityLabel, "720p") || strings.Contains(f.QualityLabel, "480p") || strings.Contains(f.QualityLabel, "360p") {
				selected = &f
				break
			}
		}
	}

	// Fallback to highest quality MP4 available with audio
	if selected == nil {
		for i := range formats {
			if strings.Contains(formats[i].MimeType, "video/mp4") && formats[i].AudioChannels > 0 {
				selected = &formats[i]
				break
			}
		}
	}

	if selected == nil {
		return nil, 0, fmt.Errorf("no suitable mp4 format with audio found")
	}

	stream, size, err := client.GetStreamContext(ctx, vid, selected)
	if err != nil {
		return nil, 0, fmt.Errorf("get stream: %w", err)
	}

	return stream, size, nil
}

