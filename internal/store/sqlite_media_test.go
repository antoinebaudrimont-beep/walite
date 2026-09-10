package store

import (
	"context"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLiteMediaMetadataSurvivesRestartWithoutPayload(t *testing.T) {
	ctx := context.Background()
	path := testSQLitePath(t)
	disk, err := OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	media, _ := model.NewMedia(model.MediaDocument, "report 日本語 👋.pdf", "application/pdf")
	want, err := model.NewMessage(model.MessageInput{
		ChatID: "documents@g.us", MessageID: "document-1", SentAt: time.UnixMilli(1234).UTC(),
		Text: "Quarterly results Café", Media: media, SenderID: "person@lid", IsGroup: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := disk.PutMessage(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}

	disk, err = OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	got, err := disk.Message(ctx, want.ChatID(), want.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	assertMessagesEqual(t, got, want)
	page, _, err := disk.Page(ctx, want.ChatID(), model.NoCursor(), 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("page len=%d err=%v", len(page), err)
	}
	assertMessagesEqual(t, page[0], want)

	var attachmentKind int
	var downloadRef, localPath any
	var declaredBytes, localBytes int64
	if err := disk.db.QueryRowContext(ctx, `SELECT kind, download_ref, local_path, declared_bytes, local_bytes
		FROM attachments WHERE chat_id = ? AND message_id = ? AND attachment_id = ?`,
		want.ChatID().String(), want.MessageID().String(), primaryMediaAttachmentID,
	).Scan(&attachmentKind, &downloadRef, &localPath, &declaredBytes, &localBytes); err != nil {
		t.Fatal(err)
	}
	if attachmentKind != int(model.MediaDocument) || downloadRef != nil || localPath != nil || declaredBytes != 0 || localBytes != 0 {
		t.Fatalf("attachment kind=%d download=%v path=%v bytes=(%d,%d)", attachmentKind, downloadRef, localPath, declaredBytes, localBytes)
	}
}

func TestSQLiteRealtimeMediaCommitRetainsMetadata(t *testing.T) {
	disk, _ := openTestSQLite(t)
	media, _ := model.NewMedia(model.MediaImage, "", "image/jpeg")
	message, err := model.NewMessage(model.MessageInput{
		ChatID: "image-chat", MessageID: "image-live", SentAt: time.Unix(2, 0).UTC(),
		Text: "caption 👋", Media: media,
	})
	if err != nil {
		t.Fatal(err)
	}
	committed, err := disk.WriteRealtime(context.Background(), sqliteLiveBatch(t, message))
	if err != nil || committed.Len() != 1 {
		t.Fatalf("committed=%d err=%v", committed.Len(), err)
	}
	event, _ := committed.At(0)
	if event.Message().Media() != media || event.Message().Text() != "caption 👋" {
		t.Fatalf("live media=%+v text=%q", event.Message().Media(), event.Message().Text())
	}
	conflict, _ := model.NewMedia(model.MediaVideo, "different.mp4", "video/mp4")
	if duplicate, err := disk.WriteRealtime(context.Background(), sqliteLiveBatch(t, message.WithMedia(conflict))); err != nil || duplicate.Len() != 0 {
		t.Fatalf("duplicate committed=%d err=%v", duplicate.Len(), err)
	}
	stored, err := disk.Message(context.Background(), message.ChatID(), message.MessageID())
	if err != nil || stored.Media() != media {
		t.Fatalf("duplicate changed media=%+v err=%v", stored.Media(), err)
	}
}

func TestSQLiteDownloadDescriptorSurvivesRestartAndDuplicateCannotDowngradeIt(t *testing.T) {
	ctx := context.Background()
	path := testSQLitePath(t)
	disk, err := OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	media, _ := model.NewDownloadableMedia(model.MediaImage, "holiday.jpg", "image/jpeg", "/mms/image", []byte("key"), []byte("hash"), []byte("encrypted"), 7)
	message, _ := model.NewMessage(model.MessageInput{ChatID: "chat", MessageID: "image", SentAt: time.Unix(1, 0).UTC(), Media: media})
	if err := disk.PutMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	presentationOnly, _ := model.NewMedia(model.MediaImage, "better.jpg", "image/jpeg")
	if err := disk.PutMessage(ctx, message.WithMedia(presentationOnly)); err != nil {
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	disk, err = OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	got, err := disk.Message(ctx, message.ChatID(), message.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	download, ok := got.Media().Download()
	if !ok || download.DirectPath() != "/mms/image" || download.DeclaredBytes() != 7 || string(download.MediaKey()) != "key" {
		t.Fatalf("media=%+v download=%+v", got.Media(), download)
	}
}

func TestSQLiteOutgoingMediaDirectionAndPlaceholderMetadataSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := testSQLitePath(t)
	disk, err := OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	media, _ := model.NewDownloadableMedia(model.MediaDocument, "sent report.pdf", "application/pdf", "/outgoing/document",
		[]byte("key"), []byte("hash"), []byte("encrypted"), 123)
	want, _ := model.NewMessage(model.MessageInput{ChatID: "chat", MessageID: "sent-media", SentAt: time.Unix(3, 0).UTC(), FromMe: true, Media: media})
	if err := disk.PutMessage(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	disk, err = OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	got, err := disk.Message(ctx, want.ChatID(), want.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	assertMessagesEqual(t, got, want)
	if !got.FromMe() || got.Media().Kind() != model.MediaDocument || got.Media().Name() != "sent report.pdf" {
		t.Fatalf("outgoing media=%+v from_me=%t", got.Media(), got.FromMe())
	}
}

func TestSQLiteOutgoingStickerSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := testSQLitePath(t)
	disk, err := OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	media, _ := model.NewDownloadableMedia(model.MediaSticker, "sent sticker.webp", "image/webp", "/outgoing/sticker",
		[]byte("key"), []byte("hash"), []byte("encrypted"), 321)
	want, _ := model.NewMessage(model.MessageInput{ChatID: "chat", MessageID: "sent-sticker", SentAt: time.Unix(4, 0).UTC(), FromMe: true, Media: media})
	if err := disk.PutMessage(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	disk, err = OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	got, err := disk.Message(ctx, want.ChatID(), want.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	assertMessagesEqual(t, got, want)
	if !got.FromMe() || got.Media().Kind() != model.MediaSticker || got.Media().MIMEType() != "image/webp" {
		t.Fatalf("outgoing sticker=%+v from_me=%t", got.Media(), got.FromMe())
	}
}
