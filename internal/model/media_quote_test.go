package model

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMediaQuoteSupportsEveryMediaKindAndOwnsOnlyPresentation(t *testing.T) {
	for _, kind := range []MediaKind{MediaImage, MediaVideo, MediaDocument, MediaAudio, MediaSticker} {
		downloadable, err := NewDownloadableMedia(kind, "report 日本語 👋.webp", "image/webp", "/mms/media", []byte("key"), []byte("hash"), []byte("encrypted"), 42)
		if err != nil {
			t.Fatal(err)
		}
		quote, err := NewMediaQuote("media-original", "", false, downloadable)
		if err != nil || quote.Media().Kind() != kind || quote.Media().Name() != downloadable.Name() || quote.Media().MIMEType() != downloadable.MIMEType() {
			t.Fatalf("kind=%s quote=%+v err=%v", kind.String(), quote, err)
		}
		if _, downloadable := quote.Media().Download(); downloadable {
			t.Fatalf("kind=%s quote retained download metadata", kind.String())
		}
		message, err := NewMessage(MessageInput{ChatID: "chat", MessageID: "reply", SentAt: time.Unix(1, 0).UTC(), FromMe: true, Text: "reply", Quote: quote})
		if err != nil || message.Quote() != quote {
			t.Fatalf("kind=%s message quote lost: %v", kind.String(), err)
		}
	}
}

func TestMediaQuoteCaptionUsesExistingOneKiBBound(t *testing.T) {
	media, _ := NewMedia(MediaImage, "", "image/jpeg")
	quote, err := NewMediaQuote("image", strings.Repeat("q", MaxQuoteTextBytes-1)+"👋 tail", true, media)
	if err != nil || len(quote.Text()) > MaxQuoteTextBytes || !utf8.ValidString(quote.Text()) || quote.Media() != media {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
	if _, err := NewTextQuote("text", "", false); err == nil {
		t.Fatal("ordinary text quote accepted an empty excerpt")
	}
}
