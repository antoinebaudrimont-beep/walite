package wa

import (
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
)

func TestAdaptMessageRecognizesBoundedMediaMetadata(t *testing.T) {
	caption := "Holiday Café 日本語 👋"
	filename := "résumé 👨‍👩‍👧‍👦.pdf"
	imageMIME, gifMIME, videoMIME, documentMIME, audioMIME, stickerMIME := "image/jpeg", "image/gif", "video/mp4", "application/pdf", "audio/ogg", "image/webp"
	gifPlayback := true
	tests := []struct {
		name     string
		payload  *waE2E.Message
		kind     model.MediaKind
		filename string
		mime     string
		caption  string
	}{
		{name: "image", payload: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: &imageMIME, Caption: &caption}}, kind: model.MediaImage, mime: imageMIME, caption: caption},
		{name: "ordinary_gif", payload: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: &gifMIME}}, kind: model.MediaImage, mime: gifMIME},
		{name: "video", payload: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: &videoMIME, Caption: &caption}}, kind: model.MediaVideo, mime: videoMIME, caption: caption},
		{name: "gif_playback_mp4", payload: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: &videoMIME, GifPlayback: &gifPlayback}}, kind: model.MediaVideo, mime: videoMIME},
		{name: "document", payload: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: &documentMIME, FileName: &filename, Caption: &caption}}, kind: model.MediaDocument, filename: filename, mime: documentMIME, caption: caption},
		{name: "audio", payload: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: &audioMIME}}, kind: model.MediaAudio, mime: audioMIME},
		{name: "sticker", payload: &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: &stickerMIME}}, kind: model.MediaSticker, mime: stickerMIME},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sentAt := time.Date(2026, 9, 8, 18, 42, 0, 0, time.UTC)
			incoming := upstreamTextMessage("15551234567", "media-"+test.name, sentAt, false, test.payload)
			event, ok := adaptMessage(incoming, sentAt.Add(time.Second))
			if !ok {
				t.Fatal("media message was not recognized")
			}
			message := event.Message()
			if message.Media().Kind() != test.kind || message.Media().Name() != test.filename ||
				message.Media().MIMEType() != test.mime || message.Text() != test.caption ||
				message.MessageID().String() != "media-"+test.name || !message.SentAt().Equal(sentAt) || message.FromMe() {
				t.Fatalf("message=%+v media=%+v", message, message.Media())
			}
		})
	}
}

func TestAdaptMediaIsMetadataOnlyAndUnsupportedIsControlled(t *testing.T) {
	sentAt := time.Unix(10, 0).UTC()
	url, mime, caption := "https://media.invalid/private", "image/jpeg", "bounded caption"
	wantMIME := mime
	upstream := &waE2E.ImageMessage{
		URL: &url, Mimetype: &mime, Caption: &caption,
		MediaKey: []byte("secret-key"), FileSHA256: []byte("hash"), JPEGThumbnail: []byte("thumbnail bytes"),
	}
	event, ok := adaptMessage(upstreamTextMessage("12345", "image", sentAt, false, &waE2E.Message{ImageMessage: upstream}), sentAt)
	if !ok {
		t.Fatal("image was not recognized")
	}
	message := event.Message()
	if message.Media().Kind() != model.MediaImage || message.Media().MIMEType() != wantMIME || message.Text() != caption || message.Media().Name() != "" {
		t.Fatalf("metadata=%+v text=%q", message.Media(), message.Text())
	}
	// The model owns copied bounded fields. This pure adapter has no client on
	// which it could perform a download or network lookup.
	*upstream.Mimetype = "changed"
	upstream.MediaKey[0] = 'X'
	if message.Media().MIMEType() != wantMIME {
		t.Fatal("transport metadata aliased upstream protobuf state")
	}
	if _, ok := adaptMessage(upstreamTextMessage("12345", "contact", sentAt, false, &waE2E.Message{ContactMessage: &waE2E.ContactMessage{}}), sentAt); ok {
		t.Fatal("unsupported media-like message escaped the controlled ignore path")
	}
}

func TestHistorySyncMediaUsesExistingNewestMessageBound(t *testing.T) {
	now := time.Date(2026, 9, 8, 18, 42, 0, 0, time.UTC)
	client := bootstrapTestClient(now)
	conversation := &waHistorySync.Conversation{ID: stringPointer("media-history@g.us")}
	for index := 0; index < model.MaxBootstrapMessagesPerChat+10; index++ {
		mime := "image/jpeg"
		direct := fmt.Sprintf("/mms/image/%d", index)
		length := uint64(index + 1)
		conversation.Messages = append(conversation.Messages, &waHistorySync.HistorySyncMsg{Message: bootstrapWebMessage(
			"media-history@g.us", fmt.Sprintf("media-%02d", index), now.Add(time.Duration(index)*time.Second), false,
			"22222@s.whatsapp.net", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: &mime, DirectPath: &direct, MediaKey: []byte("key"), FileSHA256: []byte("hash"), FileEncSHA256: []byte("encrypted"), FileLength: &length}},
		)})
	}
	record, ok := client.adaptBootstrapConversation(model.BootstrapFull, conversation, now)
	if !ok || record.Len() != model.MaxBootstrapMessagesPerChat {
		t.Fatalf("accepted=%t messages=%d", ok, record.Len())
	}
	first, _ := record.At(0)
	last, _ := record.At(record.Len() - 1)
	if first.MessageID().String() != "media-10" || last.MessageID().String() != "media-59" || first.Media().Kind() != model.MediaImage {
		t.Fatalf("bounded media first=%q last=%q", first.MessageID(), last.MessageID())
	}
	download, ok := first.Media().Download()
	if !ok || download.DirectPath() != "/mms/image/10" || download.DeclaredBytes() != 11 {
		t.Fatalf("history download=%+v ok=%t", download, ok)
	}
}

func TestAdaptMediaPreservesDownloadInputsWithoutProtocolObject(t *testing.T) {
	sentAt := time.Unix(11, 0).UTC()
	direct, mime := "/mms/image", "image/jpeg"
	key, hash, encrypted := []byte("key"), []byte("hash"), []byte("encrypted")
	length := uint64(42)
	upstream := &waE2E.ImageMessage{DirectPath: &direct, Mimetype: &mime, MediaKey: key, FileSHA256: hash, FileEncSHA256: encrypted, FileLength: &length}
	event, ok := adaptMessage(upstreamTextMessage("12345", "downloadable", sentAt, false, &waE2E.Message{ImageMessage: upstream}), sentAt)
	if !ok {
		t.Fatal("downloadable image ignored")
	}
	download, ok := event.Message().Media().Download()
	if !ok || download.DirectPath() != direct || string(download.MediaKey()) != "key" || download.DeclaredBytes() != length {
		t.Fatalf("download=%+v ok=%t", download, ok)
	}
	upstream.MediaKey[0] = 'X'
	if string(download.MediaKey()) != "key" {
		t.Fatal("adapter retained protobuf bytes")
	}
}
