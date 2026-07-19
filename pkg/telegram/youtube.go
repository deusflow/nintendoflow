package telegram

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kkdai/youtube/v2"
)

const maxVideoBytes = 50 << 20 // 50 MB — Telegram upload limit

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

	// Reject oversized videos before downloading to avoid OOM.
	// ContentLength may be 0 when the library couldn't determine size — skip check in that case.
	if selected.ContentLength > 0 && selected.ContentLength > maxVideoBytes {
		return nil, 0, fmt.Errorf("video too large: %d bytes (limit %d)", selected.ContentLength, maxVideoBytes)
	}

	stream, _, err := client.GetStreamContext(ctx, vid, selected)
	if err != nil {
		return nil, 0, fmt.Errorf("get stream: %w", err)
	}

	// Wrap with a size cap so reads beyond the limit return EOF instead of OOM.
	limitReader := io.LimitReader(stream, maxVideoBytes)

	buf := new(bytes.Buffer)
	
	// Set a hard timeout for the download phase
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	
	errCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(buf, limitReader)
		stream.Close()
		errCh <- err
	}()
	
	select {
	case <-readCtx.Done():
		stream.Close()
		return nil, 0, fmt.Errorf("youtube download timed out: %w", readCtx.Err())
	case err := <-errCh:
		if err != nil && err != io.EOF {
			return nil, 0, fmt.Errorf("youtube download error: %w", err)
		}
	}
	
	bReader := bytes.NewReader(buf.Bytes())
	return io.NopCloser(bReader), int64(bReader.Len()), nil
}
