package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type localReadSnapshotStub struct {
	*snapshotStub
	reads   []string
	through []time.Time
}

func (stub *localReadSnapshotStub) MarkChatLocallyRead(_ context.Context, id model.ChatID, through time.Time) error {
	stub.reads = append(stub.reads, id.String())
	stub.through = append(stub.through, through)
	return nil
}

func TestCacheLoaderCoalescesToNewestPendingSelection(t *testing.T) {
	base := time.Date(2100, 9, 3, 12, 0, 0, 0, time.UTC)
	stub := &snapshotStub{
		chats: []model.Chat{
			mustSnapshotChat(t, "a", "A", false, 0, base),
			mustSnapshotChat(t, "b", "B", false, 0, base.Add(time.Minute)),
		},
		messages: map[string][]model.Message{
			"a": {mustSnapshotMessage(t, "a", "a-message", base, false, "A")},
			"b": {mustSnapshotMessage(t, "b", "b-message", base, false, "B")},
		},
	}
	loader := newCacheLoader(stub)
	if !loader.requestChat(tui.ChatLoadRequest{ChatID: "a", Revision: 1}) ||
		!loader.requestChat(tui.ChatLoadRequest{ChatID: "b", Revision: 2}) {
		t.Fatal("selection request rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loader.run(ctx); close(done) }()
	select {
	case result := <-loader.chats:
		if result.ChatID != "b" || result.Revision != 2 || len(result.Messages) != 1 || result.Messages[0].ID != "b-message" {
			t.Fatalf("result=%+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cache loader did not return")
	}
	cancel()
	<-done
	if len(stub.messageLimits) != 1 {
		t.Fatalf("page queries=%d want one", len(stub.messageLimits))
	}
}

func TestCacheLoaderDeduplicatesAndDrainsAcceptedLocalReadsOnShutdown(t *testing.T) {
	stub := &localReadSnapshotStub{snapshotStub: &snapshotStub{}}
	loader := newCacheLoader(stub)
	base := time.Date(2100, 9, 7, 10, 0, 0, 0, time.UTC)
	if !loader.requestLocalRead(tui.LocalReadRequest{ChatID: "a@lid", ActivityTime: base}) ||
		!loader.requestLocalRead(tui.LocalReadRequest{ChatID: "a@lid", ActivityTime: base.Add(time.Minute)}) ||
		!loader.requestLocalRead(tui.LocalReadRequest{ChatID: "b@lid"}) {
		t.Fatal("bounded local-read request rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { loader.run(ctx); close(done) }()
	<-done
	if got := fmt.Sprint(stub.reads); got != "[a@lid b@lid]" || len(stub.through) != 2 || !stub.through[0].Equal(base.Add(time.Minute)) {
		t.Fatalf("local-read order/dedup=%s", got)
	}
}
