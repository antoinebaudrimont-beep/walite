package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

type namedApplication struct {
	*liveApplicationService
	names  chan model.DisplayMetadata
	memory *store.Memory
}

func TestDisplayGroupSenderSurvivesSnapshotAdapter(t *testing.T) {
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	chat := mustSnapshotChat(t, "group", "Family 家族", true, 2, now)
	message, err := model.NewMessage(model.MessageInput{ChatID: "group", MessageID: "m", SenderID: "opaque-person", IsGroup: true, SentAt: now, Text: "body é 👋"})
	if err != nil {
		t.Fatal(err)
	}
	source := &snapshotStub{chats: []model.Chat{chat}, messages: map[string][]model.Message{"group": {message}}}
	state, err := buildInitialTUIState(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	got := state.Chats[0].Messages[0]
	if got.SenderID != message.SenderID().String() || !got.IsGroup || got.Text != message.Text() || state.Chats[0].Title != chat.DisplayName() {
		t.Fatal("snapshot lost group/sender display data")
	}
}

func (application *namedApplication) DisplayUpdates() <-chan model.DisplayMetadata {
	return application.names
}
func (application *namedApplication) ApplyDisplayMetadata(ctx context.Context, value model.DisplayMetadata) (model.DisplayMetadata, error) {
	return application.memory.ApplyDisplayMetadata(ctx, value)
}

func TestApplicationGroupMetadataReachesExistingRenderedMessage(t *testing.T) {
	isolateApplicationFiles(t)
	memory, _ := store.NewMemory(2)
	application := &namedApplication{liveApplicationService: newLiveApplicationService(nil, nil), names: make(chan model.DisplayMetadata), memory: memory}
	screen := &committedReplyScreen{startupObservedScreen: newStartupObservedScreen(), frames: make(chan string, 4)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runStartedApplication(ctx, screen, tui.DefaultOptions(), application, tui.Run) }()
	<-screen.frames
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	message, err := model.NewMessage(model.MessageInput{ChatID: "group", MessageID: "m", SenderID: "person", IsGroup: true, SentAt: now, Text: "group message"})
	if err != nil {
		t.Fatal(err)
	}
	batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{message})
	events, err := memory.WriteRealtime(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	event, _ := events.At(0)
	application.emit <- event
	if frame := <-screen.frames; !strings.Contains(frame, "person") || !strings.Contains(frame, "group message") {
		t.Fatal("initial group sender missing")
	}
	name, _ := model.NewDisplayMetadata("group", "Family", model.DisplayGroup, true)
	application.names <- name
	if frame := <-screen.frames; !strings.Contains(frame, "Family") {
		t.Fatal("group title missing")
	}
	name, _ = model.NewDisplayMetadata("person", "Elena", model.DisplaySaved, false)
	application.names <- name
	if frame := <-screen.frames; !strings.Contains(frame, "Elena") || strings.Count(frame, "group message") != 1 {
		t.Fatal("sender update missing or duplicated message")
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	page, _, err := memory.Page(context.Background(), message.ChatID(), model.NoCursor(), 10)
	if err != nil || len(page) != 1 || page[0] != message {
		t.Fatal("display update mutated committed message")
	}
}
