package wa

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/syncpolicy"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"
)

func TestAdaptTextMessageConversationPreservesIdentityTimeDirectionAndUnicode(t *testing.T) {
	sentAt := time.Date(2026, 8, 30, 10, 11, 12, 0, time.UTC)
	receivedAt := sentAt.Add(time.Second)
	text := "walite real message test äöü é € 日本語 🐧"
	incoming := upstreamTextMessage("15551234567", "stable-message-id", sentAt, false, &waE2E.Message{Conversation: &text})

	event, ok := adaptTextMessage(incoming, receivedAt)
	if !ok {
		t.Fatal("conversation text was not recognized")
	}
	message := event.Message()
	if message.ChatID().String() != "15551234567@s.whatsapp.net" ||
		message.MessageID().String() != "stable-message-id" ||
		!message.SentAt().Equal(sentAt) || message.FromMe() || message.Text() != text ||
		!event.ReceivedAt().Equal(receivedAt) {
		t.Fatalf("event chat=%q id=%q sent=%v fromMe=%t text=%q received=%v",
			message.ChatID(), message.MessageID(), message.SentAt(), message.FromMe(), message.Text(), event.ReceivedAt())
	}
}

func TestAdaptTextMessageExtendedTextAndFromMe(t *testing.T) {
	sentAt := time.Date(2026, 8, 30, 10, 11, 12, 0, time.UTC)
	text := "extended Café 東京 ❤️"
	incoming := upstreamTextMessage("12345", "extended-id", sentAt, true, &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &text},
	})

	event, ok := adaptTextMessage(incoming, sentAt.Add(time.Second))
	if !ok || event.Message().Text() != text || !event.Message().FromMe() {
		t.Fatalf("event=%+v recognized=%t", event, ok)
	}
}

func TestAdaptTextMessageIgnoresUnsupportedAndSystemMessages(t *testing.T) {
	sentAt := time.Date(2026, 8, 30, 10, 11, 12, 0, time.UTC)
	text := "must not escape protocol wrapper"
	tests := map[string]*waEvents.Message{
		"contact": upstreamTextMessage("12345", "contact-id", sentAt, false, &waE2E.Message{ContactMessage: &waE2E.ContactMessage{}}),
		"protocol": upstreamTextMessage("12345", "protocol-id", sentAt, false, &waE2E.Message{
			Conversation: &text, ProtocolMessage: &waE2E.ProtocolMessage{},
		}),
		"edit": func() *waEvents.Message {
			message := upstreamTextMessage("12345", "edit-id", sentAt, false, &waE2E.Message{Conversation: &text})
			message.IsEdit = true
			return message
		}(),
	}
	for name, incoming := range tests {
		t.Run(name, func(t *testing.T) {
			if _, ok := adaptTextMessage(incoming, sentAt.Add(time.Second)); ok {
				t.Fatal("unsupported event was recognized")
			}
		})
	}
}

func TestRealtimeSourceIsBoundedOrderedAndBackpressured(t *testing.T) {
	source := newRealtimeSource()
	base := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	for index := 0; index < RealtimeSourceCapacity; index++ {
		if !source.admit(sourceTestEvent(t, index, base)) {
			t.Fatalf("admit %d rejected", index)
		}
	}

	overflow := sourceTestEvent(t, RealtimeSourceCapacity, base)
	result := make(chan bool, 1)
	go func() { result <- source.admit(overflow) }()
	waitForRealtimeWaiters(t, source, 1)
	source.mu.Lock()
	if source.count != RealtimeSourceCapacity {
		t.Fatalf("entries=%d capacity=%d", source.count, RealtimeSourceCapacity)
	}
	source.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.Run(ctx) }()
	first := <-source.RealtimeEvents()
	if first.Message().MessageID().String() != "bounded-000" {
		t.Fatalf("first message=%q", first.Message().MessageID())
	}
	if accepted := <-result; !accepted {
		t.Fatal("backpressured event was rejected after capacity became available")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	if _, ok := <-source.RealtimeEvents(); ok {
		t.Fatal("realtime output remained open")
	}
}

func TestRealtimeSourceCancellationReleasesBlockedCallback(t *testing.T) {
	source := newRealtimeSource()
	base := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	for index := 0; index < RealtimeSourceCapacity; index++ {
		if !source.admit(sourceTestEvent(t, index, base)) {
			t.Fatal("initial admission rejected")
		}
	}
	overflow := sourceTestEvent(t, RealtimeSourceCapacity, base)
	result := make(chan bool, 1)
	go func() { result <- source.admit(overflow) }()
	waitForRealtimeWaiters(t, source, 1)
	source.closeAdmission()
	if accepted := <-result; accepted {
		t.Fatal("blocked callback admitted after shutdown")
	}
	source.closeAdmission()
}

func TestRealtimeSourceThroughCoreCommitsOnceAndPreservesSecondMessage(t *testing.T) {
	source := newRealtimeSource()
	values := config.DefaultValues()
	memory, err := store.NewMemory(values.Retention.MaxChats)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := syncpolicy.New(values.Retention)
	if err != nil {
		t.Fatal(err)
	}
	core, err := service.New(realtimeCoreOptions(values), source, memory, policy, service.NewSystemClock())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx) }()
	waitForCoreReady(t, core.Updates())

	base := time.Now().UTC().Truncate(time.Second)
	first := sourceTestEvent(t, 1, base)
	second := sourceTestEvent(t, 2, base)
	if !source.admit(first) || !source.admit(first) || !source.admit(second) {
		t.Fatal("realtime source rejected test input")
	}
	firstLive := <-core.LiveEvents()
	secondLive := <-core.LiveEvents()
	if firstLive.Message().MessageID().String() != "bounded-001" ||
		secondLive.Message().MessageID().String() != "bounded-002" ||
		firstLive.Message().ChatID().String() != "real-chat@s.whatsapp.net" ||
		firstLive.Message().Text() != "message 1 日本語 🐧" ||
		secondLive.UnreadCount() != 2 {
		t.Fatalf("live events first=%+v second=%+v", firstLive, secondLive)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Core.Run=%v", err)
	}
}

func upstreamTextMessage(user, id string, sentAt time.Time, fromMe bool, message *waE2E.Message) *waEvents.Message {
	return &waEvents.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{
				Chat:     waTypes.NewJID(user, waTypes.DefaultUserServer),
				IsFromMe: fromMe,
			},
			ID:        waTypes.MessageID(id),
			Timestamp: sentAt,
		},
		Message: message,
	}
}

func sourceTestEvent(t *testing.T, index int, base time.Time) model.Event {
	t.Helper()
	sentAt := base.Add(time.Duration(index) * time.Second)
	message, err := model.NewMessage(model.MessageInput{
		ChatID: "real-chat@s.whatsapp.net", MessageID: fmt.Sprintf("bounded-%03d", index),
		SentAt: sentAt, Text: fmt.Sprintf("message %d 日本語 🐧", index),
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := model.NewEvent(message, sentAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func waitForRealtimeWaiters(t *testing.T, source *RealtimeSource, want int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		source.mu.Lock()
		got := source.waiters
		source.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-source.state:
		case <-deadline.C:
			t.Fatalf("waiters=%d want=%d", got, want)
		}
	}
}

func waitForCoreReady(t *testing.T, updates <-chan model.Update) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				t.Fatal("updates closed before ready")
			}
			if update.Kind() == model.UpdateReady {
				return
			}
		case <-deadline.C:
			t.Fatal("ready timeout")
		}
	}
}

func realtimeCoreOptions(values config.Values) service.Options {
	return service.Options{
		Realtime:            service.QueueOptions{Entries: values.Queues.Realtime.Entries, Bytes: values.Queues.Realtime.Bytes},
		History:             service.QueueOptions{Entries: values.Queues.History.Entries, Bytes: values.Queues.History.Bytes},
		LiveWrites:          service.QueueOptions{Entries: values.Queues.LiveWrites.Entries, Bytes: values.Queues.LiveWrites.Bytes},
		HistoryWrites:       service.QueueOptions{Entries: values.Queues.HistoryWrites.Entries, Bytes: values.Queues.HistoryWrites.Bytes},
		ViewUpdates:         service.QueueOptions{Entries: values.Queues.ViewUpdates.Entries, Bytes: values.Queues.ViewUpdates.Bytes},
		HistoryChunkRecords: values.History.ChunkRecords, HistoryChunkBytes: values.History.ChunkBytes,
		BatchMaxOperations:     values.Batch.MaxOperations,
		RetentionSnapshotLimit: values.Retention.MessagesPerChat + values.Batch.MaxOperations + 1,
		BatchWait:              values.Batch.MaxWait, LiveWriteBusy: values.Queues.LiveWriteBusy,
		ShutdownGrace: 2 * time.Second,
	}
}

func TestBootstrapRecordBridgeIsOneSlotBoundedAndShutdownUnblocksProducer(t *testing.T) {
	source := newRealtimeSource()
	if cap(source.BootstrapRecords()) != BootstrapRecordCapacity || BootstrapRecordCapacity != 1 {
		t.Fatalf("bootstrap capacity=%d", cap(source.BootstrapRecords()))
	}
	record, err := model.NewBootstrapComplete(model.BootstrapInitial)
	if err != nil || !source.admitBootstrap(record) {
		t.Fatal("first bounded bootstrap record rejected", err)
	}
	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		done <- source.admitBootstrap(record)
	}()
	<-started
	source.closeAdmission()
	select {
	case admitted := <-done:
		if admitted {
			t.Fatal("blocked bootstrap producer admitted after shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not release bounded bootstrap producer")
	}
}

func TestRealtimeSourceCachedIdentitySeedWinsWhenAlternateArrivesFirst(t *testing.T) {
	source := newRealtimeSource()
	cached, _ := model.NewChatID("12345@s.whatsapp.net")
	lid, _ := model.NewChatID("98765@lid")
	if err := source.SeedChatIDs([]model.ChatID{cached}); err != nil {
		t.Fatal(err)
	}
	resolved, err := source.aliases.resolve(lid, cached)
	if err != nil || resolved != cached || source.aliases.alternateFor(cached) != lid {
		t.Fatalf("resolved=%q alternate=%q err=%v", resolved.String(), source.aliases.alternateFor(cached).String(), err)
	}
	localUnreadRows := map[string]uint32{cached.String(): 0}
	localUnreadRows[resolved.String()]++
	if len(localUnreadRows) != 1 || localUnreadRows[cached.String()] != 1 {
		t.Fatalf("alternate created second local unread identity: %+v", localUnreadRows)
	}
}
