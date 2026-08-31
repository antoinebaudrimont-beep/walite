package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

type textSenderFunc func(context.Context, model.ChatID, string) (model.Event, error)

func (function textSenderFunc) SendText(ctx context.Context, chatID model.ChatID, text string, quotes ...model.TextQuote) (model.Event, error) {
	if len(quotes) != 0 {
		return model.Event{}, errors.New("plain-only fixture received a quote")
	}
	return function(ctx, chatID, text)
}

type sendCoreSource struct {
	realtime chan model.Event
	metadata chan model.HistoryJob
	onDemand chan model.HistoryJob
	bulk     chan model.HistoryJob
	status   chan struct{}
}

func newSendCoreSource() *sendCoreSource {
	return &sendCoreSource{
		realtime: make(chan model.Event), metadata: make(chan model.HistoryJob),
		onDemand: make(chan model.HistoryJob), bulk: make(chan model.HistoryJob), status: make(chan struct{}),
	}
}

func (source *sendCoreSource) Run(ctx context.Context) error {
	<-ctx.Done()
	close(source.realtime)
	close(source.metadata)
	close(source.onDemand)
	close(source.bulk)
	close(source.status)
	return ctx.Err()
}
func (source *sendCoreSource) RealtimeEvents() <-chan model.Event           { return source.realtime }
func (source *sendCoreSource) MetadataHistoryJobs() <-chan model.HistoryJob { return source.metadata }
func (source *sendCoreSource) OnDemandHistoryJobs() <-chan model.HistoryJob { return source.onDemand }
func (source *sendCoreSource) BulkHistoryJobs() <-chan model.HistoryJob     { return source.bulk }
func (source *sendCoreSource) StatusWake() <-chan struct{}                  { return source.status }
func (*sendCoreSource) Status() model.SourceStatus                          { return model.SourceStatus{} }
func (*sendCoreSource) NextHistory(context.Context, model.HistoryJob, int, int64) (model.HistoryChunk, bool, error) {
	return model.HistoryChunk{}, true, nil
}
func (*sendCoreSource) ReleaseHistory(model.HistoryJob) {}

func TestSendTextUsesRealtimeCommitAndLiveEventPipeline(t *testing.T) {
	store := &writerStore{}
	sentAt := time.Date(2200, 4, 5, 6, 7, 8, 0, time.UTC)
	core := runningSendCore(t, store, textSenderFunc(func(_ context.Context, chatID model.ChatID, text string) (model.Event, error) {
		return outgoingTestEvent(t, chatID, "transport-commit-id", sentAt, text), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx) }()
	if ready := <-core.Updates(); ready.Kind() != model.UpdateReady {
		t.Fatalf("first update=%v", ready.Kind())
	}
	updatesDone := make(chan struct{})
	go func() {
		for range core.Updates() {
		}
		close(updatesDone)
	}()
	request, _ := NewSendTextRequest("pipeline-chat", "committed outgoing")
	if err := core.SendText(ctx, request); err != nil {
		t.Fatal(err)
	}
	committed, ok := <-core.LiveEvents()
	if !ok || committed.Message().MessageID().String() != "transport-commit-id" ||
		committed.Message().Text() != request.Text() || !committed.Message().FromMe() ||
		committed.UnreadCount() != 0 || !committed.ActivityTime().Equal(sentAt) {
		t.Fatalf("committed=%+v open=%t", committed, ok)
	}
	observations := store.observations()
	if len(observations) != 1 || observations[0].origin != model.WriteRealtime || observations[0].ids[0] != "transport-commit-id" {
		t.Fatalf("writes=%+v", observations)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	<-updatesDone
}

func TestReadyMakesSendAdmissionImmediatelyAvailable(t *testing.T) {
	store := &writerStore{}
	core := runningSendCore(t, store, textSenderFunc(func(_ context.Context, chatID model.ChatID, text string) (model.Event, error) {
		return outgoingTestEvent(t, chatID, "ready-send-id", writerTestTime, text), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx) }()
	if ready := <-core.Updates(); ready.Kind() != model.UpdateReady {
		t.Fatalf("first update=%v", ready.Kind())
	}
	request, _ := NewSendTextRequest("ready-chat", "send immediately after Ready")
	if err := core.SendText(ctx, request); err != nil {
		t.Fatalf("send after Ready=%v", err)
	}
	if event, ok := <-core.LiveEvents(); !ok || event.Message().MessageID().String() != "ready-send-id" {
		t.Fatalf("event=%+v open=%t", event, ok)
	}
	updatesDone := make(chan struct{})
	go func() {
		for range core.Updates() {
		}
		close(updatesDone)
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	<-updatesDone
}

func TestSendTextStoreFailurePublishesNoLiveEvent(t *testing.T) {
	failure := errors.New("synthetic realtime store failure")
	store := &writerStore{fail: failure}
	core := runningSendCore(t, store, textSenderFunc(func(_ context.Context, chatID model.ChatID, text string) (model.Event, error) {
		return outgoingTestEvent(t, chatID, "uncommitted-id", writerTestTime, text), nil
	}))
	done := make(chan error, 1)
	go func() { done <- core.Run(context.Background()) }()
	if ready := <-core.Updates(); ready.Kind() != model.UpdateReady {
		t.Fatalf("first update=%v", ready.Kind())
	}
	updatesDone := make(chan struct{})
	go func() {
		for range core.Updates() {
		}
		close(updatesDone)
	}()
	request, _ := NewSendTextRequest("pipeline-chat", "accepted before store failure")
	if err := core.SendText(context.Background(), request); err != nil {
		t.Fatalf("bounded admission=%v", err)
	}
	if err := <-done; !errors.Is(err, failure) {
		t.Fatalf("Run=%v", err)
	}
	if event, ok := <-core.LiveEvents(); ok {
		t.Fatalf("uncommitted event published=%+v", event)
	}
	<-updatesDone
}

func TestSendTextAdmitsOneTransportOwnedEvent(t *testing.T) {
	sentAt := time.Date(2200, 2, 3, 4, 5, 6, 0, time.UTC)
	var calls atomic.Int32
	sender := textSenderFunc(func(ctx context.Context, chatID model.ChatID, text string) (model.Event, error) {
		calls.Add(1)
		return outgoingTestEvent(t, chatID, "transport-owned", sentAt, text), nil
	})
	core := acceptingSendCore(t, sender)
	request, err := NewSendTextRequest("stable-chat", "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦")
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SendText(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	lease, ok := core.realtimeQ.TryTake()
	if !ok {
		t.Fatal("accepted send was not admitted")
	}
	defer lease.Release()
	message := lease.value.Message()
	if calls.Load() != 1 || message.ChatID().String() != "stable-chat" || message.MessageID().String() != "transport-owned" ||
		!message.SentAt().Equal(sentAt) || !message.FromMe() || message.Text() != request.Text() {
		t.Fatalf("calls=%d message=%+v", calls.Load(), message)
	}
	if stats := core.realtimeQ.Stats(); stats.Reservations != 0 {
		t.Fatalf("successful send retained reservation: %+v", stats)
	}
}

func TestSendTextReservationPrecedesTransportAndSurvivesFullQueueCancellation(t *testing.T) {
	var core *Core
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	options := validCoreOptions()
	options.Realtime.Bytes = int64(options.Realtime.Entries) * model.MaxNormalizedEventBytes
	sender := textSenderFunc(func(_ context.Context, chatID model.ChatID, text string) (model.Event, error) {
		stats := core.realtimeQ.Stats()
		if stats.Reservations != 1 || stats.UsedBytes != model.MaxNormalizedEventBytes {
			t.Fatalf("transport started before reservation: %+v", stats)
		}
		for index := 0; index < options.Realtime.Entries-1; index++ {
			event := outgoingTestEvent(t, chatID, fmt.Sprintf("occupied-%d", index), writerTestTime, "occupied")
			if err := core.realtimeQ.TryPut(event); err != nil {
				t.Fatal(err)
			}
		}
		if err := core.realtimeQ.TryPut(outgoingTestEvent(t, chatID, "overflow", writerTestTime, "occupied")); !errors.Is(err, errQueueFull) {
			t.Fatalf("reserved slot stolen: %v", err)
		}
		cancel()
		core.realtimeQ.Close() // shutdown admission closes before handoff
		return outgoingTestEvent(t, chatID, "successful", writerTestTime, text), nil
	})
	var err error
	core, err = NewWithTextSender(options, newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime), sender)
	if err != nil {
		t.Fatal(err)
	}
	core.acceptingSends.Store(true)
	request, _ := NewSendTextRequest("chat", "sent")
	if err := core.SendText(ctx, request); err != nil {
		t.Fatalf("remote success changed to rejection: %v", err)
	}
	found := 0
	for {
		owned, ok := core.realtimeQ.TryTake()
		if !ok {
			break
		}
		if owned.Value().Message().MessageID().String() == "successful" {
			found++
		}
		_ = owned.Release()
	}
	if stats := core.realtimeQ.Stats(); found != 1 || stats.Reservations != 0 || stats.UsedBytes != 0 {
		t.Fatalf("found=%d stats=%+v", found, stats)
	}
}

func TestSendTextSaturatedRealtimePathDoesNotCallTransport(t *testing.T) {
	var calls atomic.Int32
	sender := textSenderFunc(func(context.Context, model.ChatID, string) (model.Event, error) {
		calls.Add(1)
		return model.Event{}, errors.New("transport must not be called")
	})
	options := validCoreOptions()
	options.Realtime.Bytes = int64(options.Realtime.Entries) * model.MaxNormalizedEventBytes
	core, err := NewWithTextSender(options, newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime), sender)
	if err != nil {
		t.Fatal(err)
	}
	core.acceptingSends.Store(true)
	chatID, _ := model.NewChatID("saturated-chat")
	for index := 0; index < options.Realtime.Entries; index++ {
		event := outgoingTestEvent(t, chatID, fmt.Sprintf("saturated-%03d", index), writerTestTime.Add(time.Duration(index)*time.Second), "occupied")
		if err := core.realtimeQ.TryPut(event); err != nil {
			t.Fatalf("fill %d=%v", index, err)
		}
	}
	request, _ := NewSendTextRequest(chatID.String(), "must reject before transport")
	if err := core.SendText(context.Background(), request); !errors.Is(err, &CoreError{Kind: CoreBusy}) {
		t.Fatalf("saturated send=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("transport calls=%d", calls.Load())
	}
	if stats := core.realtimeQ.Stats(); stats.Entries != options.Realtime.Entries || stats.Reservations != 0 {
		t.Fatalf("queue stats=%+v", stats)
	}
	core.realtimeQ.drainAndRelease()
}

func TestSendTextCancellationAndSingleInflightBackpressure(t *testing.T) {
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	sender := textSenderFunc(func(ctx context.Context, chatID model.ChatID, text string) (model.Event, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return model.Event{}, ctx.Err()
	})
	core := acceptingSendCore(t, sender)
	request, _ := NewSendTextRequest("stable-chat", "blocked")
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- core.SendText(ctx, request) }()
	<-entered
	if err := core.SendText(context.Background(), request); !errors.Is(err, &CoreError{Kind: CoreBusy}) {
		t.Fatalf("second send=%v", err)
	}
	cancel()
	if err := <-first; !errors.Is(err, &CoreError{Kind: CoreCancelled}) {
		t.Fatalf("cancelled send=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("transport calls=%d", calls.Load())
	}
	if stats := core.realtimeQ.Stats(); stats.Entries != 0 || stats.Reservations != 0 || stats.UsedBytes != 0 {
		t.Fatalf("cancelled send queue=%+v", stats)
	}
}

func TestSendTextCancellationBeforeTransportDoesNotCallOrAdmit(t *testing.T) {
	request, _ := NewSendTextRequest("stable-chat", "cancelled")
	var calls atomic.Int32
	core := acceptingSendCore(t, textSenderFunc(func(context.Context, model.ChatID, string) (model.Event, error) {
		calls.Add(1)
		return model.Event{}, errors.New("unexpected")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := core.SendText(ctx, request); !errors.Is(err, &CoreError{Kind: CoreCancelled}) || calls.Load() != 0 {
		t.Fatalf("pre-cancel err=%v calls=%d", err, calls.Load())
	}
}

func TestSendTextSuccessfulTransportResultRemainsAdmittedAfterCancellation(t *testing.T) {
	request, _ := NewSendTextRequest("stable-chat", "successful before cancellation observation")
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	core := acceptingSendCore(t, textSenderFunc(func(_ context.Context, chatID model.ChatID, text string) (model.Event, error) {
		calls.Add(1)
		cancel()
		return outgoingTestEvent(t, chatID, "success-after-cancel", writerTestTime, text), nil
	}))
	if err := core.SendText(ctx, request); err != nil {
		t.Fatalf("successful transport result=%v", err)
	}
	owned, ok := core.realtimeQ.TryTake()
	if !ok || owned.Value().Message().MessageID().String() != "success-after-cancel" || calls.Load() != 1 {
		t.Fatalf("admitted=%t calls=%d event=%+v", ok, calls.Load(), owned.Value())
	}
	if err := owned.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSendTextRejectsMalformedRequestAndTransportMismatch(t *testing.T) {
	if _, err := NewSendTextRequest("", "text"); !errors.Is(err, &CoreError{Kind: CoreMalformed}) {
		t.Fatalf("empty chat=%v", err)
	}
	if _, err := NewSendTextRequest("chat", ""); !errors.Is(err, &CoreError{Kind: CoreMalformed}) {
		t.Fatalf("empty text=%v", err)
	}
	if _, err := NewSendTextRequest("chat", strings.Repeat("x", model.MaxRetainedTextBytes+1)); !errors.Is(err, &CoreError{Kind: CoreMalformed}) {
		t.Fatalf("oversize=%v", err)
	}
	if _, err := NewSendTextRequest("chat", string([]byte{0xff})); !errors.Is(err, &CoreError{Kind: CoreMalformed}) {
		t.Fatalf("invalid UTF-8=%v", err)
	}
	request, _ := NewSendTextRequest("chat", "exact")
	core := acceptingSendCore(t, textSenderFunc(func(_ context.Context, _ model.ChatID, _ string) (model.Event, error) {
		other, _ := model.NewChatID("other")
		return outgoingTestEvent(t, other, "mismatch", writerTestTime, "exact"), nil
	}))
	if err := core.SendText(context.Background(), request); !errors.Is(err, &CoreError{Kind: CoreMalformed}) {
		t.Fatalf("mismatch=%v", err)
	}
	if core.realtimeQ.Stats().Entries != 0 {
		t.Fatal("malformed transport event admitted")
	}

	transportFailure := errors.New("synthetic transport failure")
	core = acceptingSendCore(t, textSenderFunc(func(context.Context, model.ChatID, string) (model.Event, error) {
		return model.Event{}, transportFailure
	}))
	if err := core.SendText(context.Background(), request); !errors.Is(err, &CoreError{Kind: CoreDegraded}) || !errors.Is(err, transportFailure) {
		t.Fatalf("transport failure=%v", err)
	}
	if core.realtimeQ.Stats().Entries != 0 {
		t.Fatal("transport failure admitted event")
	}
	if stats := core.realtimeQ.Stats(); stats.Reservations != 0 || stats.UsedBytes != 0 {
		t.Fatalf("transport failure leaked reservation: %+v", stats)
	}
}

func acceptingSendCore(t *testing.T, sender TextSender) *Core {
	t.Helper()
	core, err := NewWithTextSender(validCoreOptions(), newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime), sender)
	if err != nil {
		t.Fatal(err)
	}
	core.acceptingSends.Store(true)
	return core
}

func runningSendCore(t *testing.T, store MessageStore, sender TextSender) *Core {
	t.Helper()
	options := validCoreOptions()
	options.BatchMaxOperations = 1
	core, err := NewWithTextSender(options, newSendCoreSource(), store, coordinatorKeepPolicy{}, newManualClock(writerTestTime), sender)
	if err != nil {
		t.Fatal(err)
	}
	return core
}

func outgoingTestEvent(t *testing.T, chatID model.ChatID, messageID string, sentAt time.Time, text string) model.Event {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{
		ChatID: chatID.String(), MessageID: messageID, SentAt: sentAt, FromMe: true, Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := model.NewEvent(message, sentAt)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
