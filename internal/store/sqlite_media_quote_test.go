package store

import (
	"context"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLiteCommittedMediaQuoteSurvivesRestartAndQuoteLessDuplicate(t *testing.T) {
	ctx := context.Background()
	path := testSQLitePath(t)
	disk, err := OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	media, _ := model.NewMedia(model.MediaDocument, "report Café 日本語.pdf", "application/pdf")
	quote, _ := model.NewMediaQuote("media-original", "", false, media)
	message, err := model.NewMessage(model.MessageInput{ChatID: "chat", MessageID: "reply", SentAt: time.Unix(2, 0).UTC(), FromMe: true, Text: "reply", Quote: quote})
	if err != nil {
		t.Fatal(err)
	}
	committed, err := disk.WriteRealtime(ctx, sqliteLiveBatch(t, message, message.WithQuote(model.TextQuote{})))
	if err != nil || committed.Len() != 1 {
		t.Fatalf("committed=%d err=%v", committed.Len(), err)
	}
	event, _ := committed.At(0)
	if event.Message().Quote() != quote {
		t.Fatal("same-transaction duplicate downgraded media quote")
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
	if err != nil || got.Quote() != quote || got.Quote().Media() != media {
		t.Fatalf("quote=%+v err=%v", got.Quote(), err)
	}
	page, _, err := disk.Page(ctx, message.ChatID(), model.NoCursor(), 1)
	if err != nil || len(page) != 1 || page[0].Quote() != quote {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
