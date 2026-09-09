package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

func TestSelectingUnreadChatPersistsThroughSQLiteRestart(t *testing.T) {
	testSelectingUnreadChatPersistsThroughSQLiteRestart(t, true)
}

func TestReadReceiptsOffStillPersistsLocalClearThroughSQLiteRestart(t *testing.T) {
	testSelectingUnreadChatPersistsThroughSQLiteRestart(t, false)
}

func TestActiveInteractionInSelectedChatPersistsReadThroughSQLiteRestart(t *testing.T) {
	isolateApplicationFiles(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.db")
	cache, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2100, 9, 5, 11, 0, 0, 0, time.UTC)
	chat, _ := model.NewChat(model.ChatInput{ID: "selected@lid", DisplayName: "Selected", UnreadCount: 1, LastMessageAt: base, UpdatedAt: base})
	if err := cache.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}

	loader := newCacheLoader(&connectedApplicationService{store: cache})
	loaderCtx, cancelLoader := context.WithCancel(context.Background())
	loaderDone := make(chan struct{})
	go func() {
		loader.run(loaderCtx)
		close(loaderDone)
	}()
	screen := newStartupObservedScreen()
	live := make(chan tui.LiveMessage, 1)
	remoteReads := make(chan tui.ReadReceiptRequest, 1)
	tuiDone := make(chan error, 1)
	go func() {
		tuiDone <- tui.Run(context.Background(), screen, tui.Input{
			Options: tui.DefaultOptions(), InitialState: tui.InitialState{Chats: []tui.InitialChat{{
				ID: chat.ID().String(), Title: "Selected", ActivityTime: base,
			}}}, LiveEvents: live, PersistLocalRead: loader.requestLocalRead,
			SendReadReceipt: func(request tui.ReadReceiptRequest) bool { remoteReads <- request; return true },
		})
	}()
	<-screen.shown
	live <- tui.LiveMessage{
		ChatID: chat.ID().String(), MessageID: "new-incoming", SentAt: base,
		Text: "body is irrelevant", BodyRetained: true, UnreadCount: 1, ActivityTime: base,
	}
	<-screen.shown
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	if request := <-remoteReads; request.ChatID != chat.ID().String() || len(request.Messages) != 1 || request.Messages[0].MessageID != "new-incoming" {
		t.Fatalf("remote request=%+v", request)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-tuiDone; err != nil {
		t.Fatal(err)
	}
	cancelLoader()
	<-loaderDone
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	chats, err := reopened.ListChats(ctx, model.MaxChatSummaries)
	if err != nil || len(chats) != 1 || chats[0].UnreadCount() != 0 {
		t.Fatalf("restart chats=%+v err=%v", chats, err)
	}
}

func testSelectingUnreadChatPersistsThroughSQLiteRestart(t *testing.T, receipts bool) {
	isolateApplicationFiles(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.db")
	cache, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2100, 9, 5, 10, 0, 0, 0, time.UTC)
	first, _ := model.NewChat(model.ChatInput{ID: "first@lid", DisplayName: "First", UnreadCount: 2, LastMessageAt: base, UpdatedAt: base})
	second, _ := model.NewChat(model.ChatInput{ID: "second@lid", DisplayName: "Second", UnreadCount: 3, LastMessageAt: base.Add(-time.Minute), UpdatedAt: base})
	if err := cache.EnsureChat(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := cache.EnsureChat(ctx, second); err != nil {
		t.Fatal(err)
	}

	application := &connectedApplicationService{store: cache}
	loader := newCacheLoader(application)
	loaderCtx, cancelLoader := context.WithCancel(context.Background())
	loaderDone := make(chan struct{})
	go func() {
		loader.run(loaderCtx)
		close(loaderDone)
	}()
	screen := newStartupObservedScreen()
	tuiDone := make(chan error, 1)
	remoteReads := make(chan tui.ReadReceiptRequest, 1)
	options := tui.DefaultOptions()
	options.SendReadReceipts = receipts
	go func() {
		tuiDone <- tui.Run(context.Background(), screen, tui.Input{
			Options: options, InitialState: tui.InitialState{Chats: []tui.InitialChat{
				{ID: first.ID().String(), Title: "First", UnreadCount: 0, ActivityTime: first.LastMessageAt()},
				{ID: second.ID().String(), Title: "Second", UnreadCount: 3, ActivityTime: second.LastMessageAt(), Messages: []tui.InitialMessage{
					{ID: "stable-incoming", SentAt: second.LastMessageAt(), Text: "unicode 🐧", BodyRetained: true},
				}},
			}}, PersistLocalRead: loader.requestLocalRead,
			SendReadReceipt: func(request tui.ReadReceiptRequest) bool { remoteReads <- request; return false },
		})
	}()
	<-screen.shown
	screen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	<-screen.shown
	if rendered := startupScreenText(screen); !strings.Contains(rendered, "> Second") || strings.Contains(rendered, "> Second (3)") {
		t.Fatalf("selected unread chat did not clear locally:\n%s", rendered)
	}
	if receipts {
		remote := <-remoteReads
		if remote.ChatID != second.ID().String() || len(remote.Messages) != 1 || remote.Messages[0].MessageID != "stable-incoming" {
			t.Fatalf("remote read=%+v", remote)
		}
	} else {
		select {
		case remote := <-remoteReads:
			t.Fatalf("Off admitted remote read: %+v", remote)
		default:
		}
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-tuiDone; err != nil {
		t.Fatal(err)
	}
	cancelLoader()
	<-loaderDone
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	chats, err := reopened.ListChats(ctx, model.MaxChatSummaries)
	if err != nil || len(chats) != 2 {
		t.Fatalf("restart chats=%+v err=%v", chats, err)
	}
	byID := map[string]uint32{}
	for _, chat := range chats {
		byID[chat.ID().String()] = chat.UnreadCount()
	}
	if byID[first.ID().String()] != 2 || byID[second.ID().String()] != 0 {
		t.Fatalf("restart unread first=%d second=%d", byID[first.ID().String()], byID[second.ID().String()])
	}
}
