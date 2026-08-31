package service_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/syncpolicy"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

var _ service.MessageStore = (*store.SQLiteStore)(nil)

// Focused offline composition only; production continues to use Memory.
func TestCoreSQLiteCommittedCompatibility(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	disk, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: filepath.Join(t.TempDir(), "cache.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	base := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	incoming, _ := model.NewMessage(model.MessageInput{ChatID: "synthetic-chat", MessageID: "incoming", SentAt: base, Text: "Café 👋"})
	quote, _ := model.NewTextQuote("incoming", incoming.Text(), false)
	reply, _ := model.NewMessage(model.MessageInput{ChatID: "synthetic-chat", MessageID: "reply", SentAt: base.Add(time.Second), Text: "reply ❤️", FromMe: true, Quote: quote})
	var steps []wa.ScriptStep
	// FakeSource deliberately has a one-slot, nonblocking ingress. Gate each
	// next event on the preceding publication so this tests the committed store
	// path, not scheduler-dependent pre-admission source drops.
	advanced := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	for i, message := range []model.Message{incoming, reply, reply.WithQuote(model.TextQuote{})} {
		event, _ := model.NewEvent(message, message.SentAt())
		var gate <-chan struct{}
		if i > 0 {
			gate = advanced[i-1]
		}
		steps = append(steps, wa.NewRealtimeStep(event, gate))
	}
	source, err := wa.NewFakeSource(steps)
	if err != nil {
		t.Fatal(err)
	}
	values := config.DefaultValues()
	policy, _ := syncpolicy.New(values.Retention)
	core, err := service.New(integrationOptions(values), source, disk, policy, service.NewSystemClock())
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	var runErr error
	go func() { runErr = core.Run(ctx); close(finished) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("Core did not join")
		}
	})
	var events []model.LiveEvent
	for _, gate := range advanced {
		select {
		case event, ok := <-core.LiveEvents():
			if !ok {
				t.Fatal("live stream closed before committed event")
			}
			events = append(events, event)
			close(gate)
		case <-time.After(5 * time.Second):
			t.Fatal("committed event did not arrive")
		}
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Core did not complete")
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	for event := range core.LiveEvents() {
		events = append(events, event)
	}
	if len(events) != 2 {
		t.Fatalf("committed publication count=%d", len(events))
	}
	if source.Status().Degraded() {
		t.Fatal("fixture dropped input before service admission")
	}
	for i, message := range []model.Message{incoming, reply} {
		got := events[i]
		if got.Message().MessageID() != message.MessageID() || got.Message().Text() != message.Text() || got.Message().Quote() != message.Quote() || got.UnreadCount() != 1 || !got.ActivityTime().Equal(message.SentAt()) {
			t.Fatalf("event %d lost committed data", i)
		}
		stored, err := disk.Message(ctx, message.ChatID(), message.MessageID())
		if err != nil || stored.Quote() != message.Quote() {
			t.Fatalf("published without persistence: %v", err)
		}
	}
	usage, err := disk.Usage(ctx)
	if err != nil || usage.Messages() != 2 || usage.Bodies() != 2 {
		t.Fatalf("usage: %+v %v", usage, err)
	}
}
