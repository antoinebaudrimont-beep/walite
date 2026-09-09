package model

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMediaMetadataIsClosedBoundedAndOwned(t *testing.T) {
	for kind, label := range map[MediaKind]string{
		MediaImage: "image", MediaVideo: "video", MediaDocument: "document",
		MediaAudio: "audio", MediaSticker: "sticker",
	} {
		media, err := NewMedia(kind, "Café 👨‍👩‍👧‍👦\n"+strings.Repeat("x", MaxMediaNameBytes), "image/jpeg\tprivate")
		if err != nil {
			t.Fatal(err)
		}
		if media.Kind() != kind || kind.String() != label || len(media.Name()) > MaxMediaNameBytes ||
			len(media.MIMEType()) > MaxMediaMIMEBytes || !utf8.ValidString(media.Name()) || strings.ContainsAny(media.Name(), "\n\t") {
			t.Fatalf("media=%+v label=%q", media, kind.String())
		}
	}
	if _, err := NewMedia(MediaKind(99), "", ""); err == nil {
		t.Fatal("unknown media kind accepted")
	}
}

func TestMessageMediaSurvivesCopiesAndBodyRemoval(t *testing.T) {
	media, _ := NewMedia(MediaDocument, "résumé 👋.pdf", "application/pdf")
	message, err := NewMessage(MessageInput{
		ChatID: "chat", MessageID: "media", SentAt: time.Unix(1, 0).UTC(),
		Text: "caption 日本語", Media: media,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Media() != media || message.ByteSize() != len("chat")+len("media")+len("caption 日本語")+len(media.Name())+len(media.MIMEType()) {
		t.Fatalf("message media=%+v bytes=%d", message.Media(), message.ByteSize())
	}
	bodyless := message.WithoutBody()
	if bodyless.BodyRetained() || bodyless.Text() != "" || bodyless.Media() != media {
		t.Fatalf("bodyless media message=%+v", bodyless)
	}
}

func TestDownloadableMediaOwnsBoundedNeutralInputs(t *testing.T) {
	key, hash, encrypted := []byte("key"), []byte("hash"), []byte("encrypted")
	media, err := NewDownloadableMedia(MediaImage, "Café 👋.jpg", "image/jpeg", "/mms/image", key, hash, encrypted, 123)
	if err != nil {
		t.Fatal(err)
	}
	key[0], hash[0], encrypted[0] = 'X', 'X', 'X'
	download, ok := media.Download()
	if !ok || download.DirectPath() != "/mms/image" || string(download.MediaKey()) != "key" || string(download.FileSHA256()) != "hash" || string(download.FileEncSHA256()) != "encrypted" || download.DeclaredBytes() != 123 {
		t.Fatalf("download=%+v ok=%t", download, ok)
	}
	returned := download.MediaKey()
	returned[0] = 'Y'
	again, _ := media.Download()
	if string(again.MediaKey()) != "key" {
		t.Fatal("download bytes were mutable")
	}
	if _, err := NewDownloadableMedia(MediaImage, "", "", "relative", []byte("k"), []byte("h"), []byte("e"), 0); err == nil {
		t.Fatal("relative path accepted")
	}
}
