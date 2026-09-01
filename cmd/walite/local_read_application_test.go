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
	go func() {
		tuiDone <- tui.Run(context.Background(), screen, tui.Input{
			Options: tui.DefaultOptions(), InitialState: tui.InitialState{Chats: []tui.InitialChat{
				{ID: first.ID().String(), Title: "First", UnreadCount: 0, ActivityTime: first.LastMessageAt()},
				{ID: second.ID().String(), Title: "Second", UnreadCount: 3, ActivityTime: second.LastMessageAt()},
			}}, PersistLocalRead: loader.requestLocalRead,
		})
	}()
	<-screen.shown
	screen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	<-screen.shown
	if rendered := startupScreenText(screen); !strings.Contains(rendered, "> Second") || strings.Contains(rendered, "> Second (3)") {
		t.Fatalf("selected unread chat did not clear locally:\n%s", rendered)
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
