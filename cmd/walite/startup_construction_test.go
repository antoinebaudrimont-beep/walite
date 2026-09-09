package main

import (
	"context"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

// TestMilestone4EProductionShapedConstruction exercises the exact capability
// extraction and service-constructor gate used after a real connection object
// is opened. It deliberately does not connect or run any network operation.
func TestMilestone4EProductionShapedConstruction(t *testing.T) {
	ctx := context.Background()
	sessionPath := t.TempDir() + "/session.db"
	cachePath := t.TempDir() + "/cache.db"
	connection, err := wa.NewConnection(ctx, sessionPath)
	if err != nil {
		t.Fatalf("construct WhatsApp connection: %v", err)
	}
	defer connection.Close()
	source := connection.RealtimeSource()
	textSender := connection.TextSender()
	readReceiptSender := connection.ReadReceiptSender()
	mediaDownloader := connection.MediaDownloader()
	if source == nil || textSender == nil || readReceiptSender == nil || mediaDownloader == nil {
		t.Fatalf("capabilities source=%t text=%t receipt=%t media=%t linked=%t", source != nil, textSender != nil, readReceiptSender != nil, mediaDownloader != nil, connection.Linked())
	}
	cache, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: cachePath})
	if err != nil {
		t.Fatalf("open application cache: %v", err)
	}
	defer cache.Close()
	base := time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)
	for index, id := range []string{"12345@s.whatsapp.net", "987654@lid"} {
		chat, chatErr := model.NewChat(model.ChatInput{ID: id, LastMessageAt: base.Add(-time.Duration(index) * time.Minute), UpdatedAt: base})
		if chatErr != nil {
			t.Fatal(chatErr)
		}
		if err := cache.EnsureChat(ctx, chat); err != nil {
			t.Fatalf("seed duplicate-form cache chat: %v", err)
		}
	}
	cachedChats, err := cache.ListChats(ctx, model.MaxChatSummaries)
	if err != nil {
		t.Fatalf("load cached chat identities: %v", err)
	}
	ids := make([]model.ChatID, len(cachedChats))
	for index, chat := range cachedChats {
		ids[index] = chat.ID()
	}
	if err := source.SeedChatIDs(ids); err != nil {
		t.Fatalf("seed cached chat identities: %v", err)
	}
	application, err := newConnectedApplicationServiceWithMedia(source, textSender, readReceiptSender, mediaDownloader, cache)
	if err != nil || application == nil {
		t.Fatalf("construct service: application=%t err=%v", application != nil, err)
	}
	worker := newReadReceiptWorker(ctx, application)
	worker.stop()
}
