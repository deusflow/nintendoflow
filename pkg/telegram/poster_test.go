package telegram

import (
	"strings"
	"testing"

	"github.com/deuswork/nintendoflow/pkg/db"
)

func TestEncodeAndSplitCachedVideoURL(t *testing.T) {
	rawURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	fileID := "BAACAgIAAxkBAAIB22c"

	encoded := EncodeCachedVideoURL(fileID, rawURL)
	gotFileID, gotURL := SplitCachedVideoURL(encoded)
	if gotFileID != fileID {
		t.Fatalf("expected fileID %q, got %q", fileID, gotFileID)
	}
	if gotURL != rawURL {
		t.Fatalf("expected url %q, got %q", rawURL, gotURL)
	}

	plainID, plainURL := SplitCachedVideoURL(rawURL)
	if plainID != "" || plainURL != rawURL {
		t.Fatalf("expected plain URL passthrough, got id=%q url=%q", plainID, plainURL)
	}
}

func TestSafeTruncateHTMLSupportsExpandableBlockquoteAndSpoiler(t *testing.T) {
	input := `<b>Заголовок</b>` + "\n\n" +
		`<blockquote expandable>Деталі патчу та <tg-spoiler>секретний бос у фіналі дуже довгий спойлер</tg-spoiler> дуже довгий текст для перевірки обрізання</blockquote>`

	truncated := safeTruncateHTML(input, 95)
	if !strings.HasSuffix(truncated, "...</tg-spoiler></blockquote>") {
		t.Fatalf("expected unclosed <tg-spoiler> and <blockquote> to be closed in reverse order, got: %s", truncated)
	}
}

func TestBuildCaptionAddsCommunitySignature(t *testing.T) {
	body := "<b>🎉 Відбувся реліз нової гри для Nintendo Switch 2</b>\n\nВидання містить базову гру та всі доповнення."
	caption := buildCaption(&db.Article{BodyUA: body}, 1024)
	if !strings.Contains(caption, `<a href="https://t.me/deusflow">DEUSFLOW</a> 🎮 <a href="https://t.me/deusflow">Твоя Nintendo Спільнота</a>`) {
		t.Fatalf("expected community footer signature in caption, got: %s", caption)
	}
}
