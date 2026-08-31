package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestMemoryDisplayMetadataDoesNotInsertOrChangeActivity(t *testing.T) {
	ctx := context.Background()
	memory, _ := NewMemory(2)
	name, _ := model.NewDisplayMetadata("group", "Family 👋", model.DisplayGroup, true)
	if _, err := memory.ApplyDisplayMetadata(ctx, name); err != nil {
		t.Fatal(err)
	}
	if len(memory.chats) != 0 {
		t.Fatal("metadata inserted a chat")
	}
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	message, _ := model.NewMessage(model.MessageInput{ChatID: "group", MessageID: "m", SenderID: "person", IsGroup: true, SentAt: now, Text: "exact é 👋"})
	batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{message})
	committed, err := memory.WriteRealtime(ctx, batch)
	if err != nil || committed.Len() != 1 {
		t.Fatalf("commit: %v", err)
	}
	live, _ := committed.At(0)
	if live.Message() != message || live.Message().SenderID().String() != "person" {
		t.Fatal("store/live lost sender")
	}
	chat := memory.chats["group"].chat
	if chat.DisplayName() != name.Name() || !chat.IsGroup() || chat.UnreadCount() != 1 {
		t.Fatal("known group metadata missing")
	}
	rename, _ := model.NewDisplayMetadata("group", "Renamed 家族", model.DisplayGroup, true)
	if _, err := memory.ApplyDisplayMetadata(ctx, rename); err != nil {
		t.Fatal(err)
	}
	after := memory.chats["group"].chat
	if after.ID() != chat.ID() || after.UnreadCount() != chat.UnreadCount() || after.LastMessageAt() != chat.LastMessageAt() {
		t.Fatal("name changed identity/unread/activity")
	}
	page, _, err := memory.Page(ctx, chat.ID(), model.NoCursor(), 10)
	if err != nil || len(page) != 1 || page[0] != message {
		t.Fatal("metadata changed messages")
	}
	duplicate, err := memory.WriteRealtime(ctx, batch)
	if err != nil || duplicate.Len() != 0 {
		t.Fatal("metadata defeated dedup")
	}
	// An identity-equivalent duplicate without sender data must not erase the
	// already committed attribution or create another live insertion.
	stripped := message.WithSender(model.ContactID{}, false)
	batch, _ = model.NewWriteBatch(model.WriteRealtime, []model.Message{stripped})
	duplicate, err = memory.WriteRealtime(ctx, batch)
	if err != nil || duplicate.Len() != 0 {
		t.Fatal("duplicate produced another live event")
	}
	page, _, err = memory.Page(ctx, chat.ID(), model.NoCursor(), 10)
	if err != nil || len(page) != 1 || page[0] != message {
		t.Fatal("duplicate erased sender identity")
	}
}

func TestMemoryDisplayQualitySurvivesCacheReplacement(t *testing.T) {
	ctx := context.Background()
	memory, _ := NewMemory(1)
	chat, _ := model.NewChat(model.ChatInput{ID: "selected", UnreadCount: 7})
	if err := memory.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	saved, _ := model.NewDisplayMetadata("selected", "Saved name", model.DisplaySaved, false)
	if _, err := memory.ApplyDisplayMetadata(ctx, saved); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2*model.DisplayMetadataCapacity; i++ {
		name, _ := model.NewDisplayMetadata(fmt.Sprintf("unrelated-%d", i), "Same name", model.DisplayPush, false)
		if _, err := memory.ApplyDisplayMetadata(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"Phone push name", ""} {
		low, _ := model.NewDisplayMetadata("selected", text, model.DisplayPush, false)
		got, err := memory.ApplyDisplayMetadata(ctx, low)
		if err != nil || got.Name() != saved.Name() || memory.chats["selected"].chat.DisplayName() != saved.Name() {
			t.Fatal("cache eviction allowed quality downgrade")
		}
	}
	if len(memory.chats) != 1 || len(memory.display) != model.DisplayMetadataCapacity || memory.chats["selected"].chat.UnreadCount() != 7 {
		t.Fatal("metadata grew chats or changed unread")
	}
}
