package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

type coordinatorKeepPolicy struct{}

func (coordinatorKeepPolicy) Decide(model.Message, model.RetentionState) (model.RetentionDecision, error) {
	return model.NewRetentionDecision(model.KeepBody, model.RetentionEligible)
}
func (coordinatorKeepPolicy) PlanPrune(model.RetentionSnapshot, model.RetentionState) (model.PrunePlan, error) {
	return model.PrunePlan{}, nil
}

func validCoreOptions() Options {
	return Options{Realtime: QueueOptions{128, model.MaxNormalizedEventBytes}, History: QueueOptions{4, model.MaxHistoryChunkBytes}, LiveWrites: QueueOptions{128, 1 << 20}, HistoryWrites: QueueOptions{256, 2 << 20}, ViewUpdates: QueueOptions{64, model.MaxNormalizedUpdateBytes}, HistoryChunkRecords: model.MaxHistoryChunkRecords, HistoryChunkBytes: model.MaxHistoryChunkBytes, BatchMaxOperations: 50, RetentionSnapshotLimit: model.MaxRetentionSnapshotSummaries, BatchWait: time.Second, LiveWriteBusy: time.Second, ShutdownGrace: time.Second}
}

func TestCoreOptionValidation(t *testing.T) {
	source := newCoreTestSource()
	store := defaultHistoryStore(t)
	policy := &historyPolicy{}
	options := validCoreOptions()
	if _, err := New(options, source, store, policy, newManualClock(writerTestTime)); err != nil {
		t.Fatal(err)
	}
	options.Realtime.Entries = 129
	if _, err := New(options, source, store, policy, newManualClock(writerTestTime)); err == nil {
		t.Fatal("invalid options succeeded")
	}
}
func TestCoreWeightsRejectMalformed(t *testing.T) {
	if _, err := weighEvent(model.Event{}); err == nil {
		t.Fatal("malformed event weighed")
	}
	if _, err := weighMessage(model.Message{}); err == nil {
		t.Fatal("malformed message weighed")
	}
	if _, err := weighUpdate(model.Update{}); err == nil {
		t.Fatal("malformed update weighed")
	}
}

func TestRealtimeIngressBackpressuresWithoutDroppingRecognizedEvent(t *testing.T) {
	source := newCoreTestSource()
	budget := mustBudget(t, 2*model.MaxNormalizedEventBytes)
	realtime, err := newBoundedQueue(1, budget, weighEvent)
	if err != nil {
		t.Fatal(err)
	}
	firstMessage := historyMessage(t, "ingress-first", 0)
	first, _ := model.NewEvent(firstMessage, writerTestTime)
	secondMessage := historyMessage(t, "ingress-second", 1)
	second, _ := model.NewEvent(secondMessage, writerTestTime)
	if err := realtime.TryPut(first); err != nil {
		t.Fatal(err)
	}
	source.realtime <- second
	close(source.realtime)
	drops := &saturatingCounter{}
	core := &Core{source: source, realtimeQ: realtime, localDrops: drops, localDropWake: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() { done <- core.runRealtimeIngress(context.Background()) }()
	for realtime.Stats().EntryWaiters != 1 {
		<-realtime.stateChanges()
	}
	if drops.load() != 0 {
		t.Fatalf("drops=%d", drops.load())
	}
	owned, ok := realtime.TryTake()
	if !ok || owned.Value().Message().MessageID().String() != "ingress-first" {
		t.Fatal("prefilled event missing")
	}
	_ = owned.Release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	owned, ok = realtime.TryTake()
	if !ok || owned.Value().Message().MessageID().String() != "ingress-second" {
		t.Fatal("backpressured event missing")
	}
	_ = owned.Release()
	if realtime.Stats().UsedBytes != 0 {
		t.Fatalf("used bytes=%d", realtime.Stats().UsedBytes)
	}
}
func TestSystemClockStructural(t *testing.T) {
	clock := NewSystemClock()
	if clock.Now().IsZero() {
		t.Fatal("zero time")
	}
	timer := clock.NewTimer(time.Hour)
	if timer == nil || !timer.Stop() {
		t.Fatal("system timer invalid")
	}
}

func TestPublisherRetainsPendingAndSynchronouslyRescans(t *testing.T) {
	core, err := New(validCoreOptions(), newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	pending := make(chan struct{}, 1)
	core.publisherPendingHook = func() { pending <- struct{}{} }
	filler, _ := model.NewUpdate(model.UpdateInput{Kind: model.UpdateReady})
	core.updates <- filler
	chat, _ := model.NewChatID("publisher-chat")
	message, _ := model.NewMessageID("publisher-message")
	first, _ := model.NewUpdate(model.UpdateInput{Kind: model.UpdateLive, ChatID: chat, MessageID: message, Accepted: 1})
	second, _ := model.NewUpdate(model.UpdateInput{Kind: model.UpdateLive, ChatID: chat, MessageID: message, Accepted: 2})
	slot, _ := updateSlot(model.UpdateLive)
	if err := core.updateMailbox.Replace(slot, first); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { core.runPublisher(ctx, nil, nil); close(done) }()
	<-pending
	if err := core.updateMailbox.Replace(slot, second); err != nil {
		t.Fatal(err)
	}
	<-core.updates
	if got := <-core.updates; got.Accepted() != 1 {
		t.Fatalf("pending accepted=%d", got.Accepted())
	}
	if got := <-core.updates; got.Accepted() != 2 {
		t.Fatalf("rescanned accepted=%d", got.Accepted())
	}
	core.updateMailbox.Close()
	cancel()
	<-done
	if _, ok := <-core.updates; ok {
		t.Fatal("updates remained open")
	}
	if used := core.updateMailbox.budget.usedBytes(); used != 0 {
		t.Fatalf("mailbox bytes=%d", used)
	}
}

func TestCoordinatorRealtimeKeepBody(t *testing.T) {
	source := newCoreTestSource()
	store := defaultHistoryStore(t)
	policy := &historyPolicy{}
	clock := newManualClock(writerTestTime)
	realtimeBudget := mustBudget(t, model.MaxNormalizedEventBytes)
	realtime, _ := newBoundedQueue(2, realtimeBudget, weighEvent)
	live := newMessageQueue(t, 2)
	resultBudget := mustBudget(t, writeResultWeight)
	results, _ := newBoundedQueue(1, resultBudget, weighWriteResult)
	updateBudget := mustBudget(t, model.MaxNormalizedUpdateBytes)
	mailbox, _ := newFixedMailbox([]viewSlot{0, 1, 2, 3, 4, 5, 6, 7}, updateBudget, weighUpdate)
	message := historyMessage(t, "live", 0)
	event, _ := model.NewEvent(message, writerTestTime)
	if err := realtime.TryPut(event); err != nil {
		t.Fatal(err)
	}
	realtime.Close()
	results.Close()
	drops := &saturatingCounter{}
	coordinator := &realtimeCoordinator{source: source, store: store, policy: policy, clock: clock, realtimeQ: realtime, liveWriteQ: live, resultQ: results, updates: mailbox, liveWriteBusy: time.Second, localDrops: drops}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	owned, ok := live.TryTake()
	if !ok || owned.Value().MessageID().String() != "live" {
		t.Fatal("live write missing")
	}
	_ = owned.Release()
	_, update, _, ok := mailbox.TakeDirty()
	if !ok || update.Value().Kind() != model.UpdateLive {
		t.Fatal("live update missing")
	}
	_ = update.Release()
}

func TestCoordinatorLiveRetryWakesOnByteBudgetRelease(t *testing.T) {
	message := historyMessage(t, "budget-live", 0)
	messageWeight := int64(message.ByteSize())
	liveBudget := mustBudget(t, messageWeight)
	live, err := newBoundedQueue(2, liveBudget, weighMessage)
	if err != nil {
		t.Fatal(err)
	}
	blockingCharge, err := liveBudget.tryAcquire(messageWeight)
	if err != nil {
		t.Fatal(err)
	}
	realtimeBudget := mustBudget(t, model.MaxNormalizedEventBytes)
	realtime, _ := newBoundedQueue(1, realtimeBudget, weighEvent)
	event, _ := model.NewEvent(message, writerTestTime)
	if err := realtime.TryPut(event); err != nil {
		t.Fatal(err)
	}
	realtime.Close()
	resultBudget := mustBudget(t, writeResultWeight)
	results, _ := newBoundedQueue(1, resultBudget, weighWriteResult)
	results.Close()
	updateBudget := mustBudget(t, model.MaxNormalizedUpdateBytes)
	mailbox, _ := newFixedMailbox([]viewSlot{0, 1, 2, 3, 4, 5, 6, 7}, updateBudget, weighUpdate)
	clock := newWriterClock()
	coordinator := &realtimeCoordinator{source: newCoreTestSource(), store: defaultHistoryStore(t), policy: &historyPolicy{}, clock: clock, realtimeQ: realtime, liveWriteQ: live, resultQ: results, updates: mailbox, liveWriteBusy: time.Second, localDrops: &saturatingCounter{}, localDropWake: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(context.Background()) }()
	<-clock.registered
	if entries := live.Stats().Entries; entries != 0 {
		t.Fatalf("live entries=%d", entries)
	}
	if err := blockingCharge.Release(); err != nil {
		t.Fatal(err)
	}
	for live.Stats().Entries == 0 {
		<-live.stateChanges()
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	owned, ok := live.TryTake()
	if !ok || owned.Value().MessageID().String() != message.MessageID().String() {
		t.Fatal("pending live message was not admitted")
	}
	_ = owned.Release()
	if realtime.Stats().UsedBytes != 0 || live.Stats().UsedBytes != 0 {
		t.Fatalf("realtime=%+v live=%+v", realtime.Stats(), live.Stats())
	}
	clock.mu.Lock()
	timer := clock.timers[0]
	clock.mu.Unlock()
	select {
	case <-timer.C():
		t.Fatal("busy timer fired")
	default:
	}
}

func TestRealtimeProgressWhileRealHistoryIngesterIsBlocked(t *testing.T) {
	historyQ := newChunkQueue(t, 1)
	historyWrites := newMessageQueue(t, 1)
	putMessage(t, historyWrites, "prefill", 0)
	chunk := mustChunk(t, historyMessage(t, "blocked-history", 1))
	putRawChunk(t, historyQ, chunk)
	historyQ.Close()
	ingester, err := newHistoryIngester(defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime), historyQ, historyWrites)
	if err != nil {
		t.Fatal(err)
	}
	ingestDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { ingestDone <- ingester.Run(ctx) }()
	for historyWrites.Stats().EntryWaiters != 1 {
		<-historyWrites.stateChanges()
	}
	if historyQ.Stats().UsedBytes == 0 {
		t.Fatal("blocked history chunk lease is not charged")
	}

	realtimeBudget := mustBudget(t, model.MaxNormalizedEventBytes)
	realtime, _ := newBoundedQueue(1, realtimeBudget, weighEvent)
	live := newMessageQueue(t, 1)
	resultBudget := mustBudget(t, writeResultWeight)
	results, _ := newBoundedQueue(1, resultBudget, weighWriteResult)
	results.Close()
	updateBudget := mustBudget(t, model.MaxNormalizedUpdateBytes)
	mailbox, _ := newFixedMailbox([]viewSlot{0, 1, 2, 3, 4, 5, 6, 7}, updateBudget, weighUpdate)
	liveMessage := historyMessage(t, "isolated-live", 2)
	liveEvent, _ := model.NewEvent(liveMessage, writerTestTime)
	if err := realtime.TryPut(liveEvent); err != nil {
		t.Fatal(err)
	}
	realtime.Close()
	coordinator := &realtimeCoordinator{source: newCoreTestSource(), store: defaultHistoryStore(t), policy: &historyPolicy{}, clock: newManualClock(writerTestTime), realtimeQ: realtime, liveWriteQ: live, resultQ: results, updates: mailbox, liveWriteBusy: time.Second, localDrops: &saturatingCounter{}, localDropWake: make(chan struct{})}
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	liveOwned, ok := live.TryTake()
	if !ok || liveOwned.Value().MessageID().String() != "isolated-live" {
		t.Fatal("realtime live admission did not progress")
	}
	_ = liveOwned.Release()
	_, update, _, ok := mailbox.TakeDirty()
	if !ok || update.Value().Kind() != model.UpdateLive {
		t.Fatal("Live update missing")
	}
	_ = update.Release()

	prefilled, ok := historyWrites.TryTake()
	if !ok {
		t.Fatal("history prefill missing")
	}
	_ = prefilled.Release()
	if err := <-ingestDone; err != nil {
		t.Fatal(err)
	}
	historyWrites.drainAndRelease()
	if historyQ.Stats().UsedBytes != 0 || historyWrites.Stats().UsedBytes != 0 || realtime.Stats().UsedBytes != 0 || live.Stats().UsedBytes != 0 {
		t.Fatalf("historyQ=%+v historyWrites=%+v realtime=%+v live=%+v", historyQ.Stats(), historyWrites.Stats(), realtime.Stats(), live.Stats())
	}
}

func TestCoordinatorFinalRealtimeDrainLimit(t *testing.T) {
	for _, limit := range []int{7, 50} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			realtimeBudget := mustBudget(t, int64(limit+3)*model.MaxNormalizedEventBytes)
			realtime, _ := newBoundedQueue(limit+3, realtimeBudget, weighEvent)
			live := newMessageQueue(t, limit+3)
			for index := 0; index < limit+3; index++ {
				message := historyMessage(t, "final-"+strconv.Itoa(index), index)
				event, _ := model.NewEvent(message, writerTestTime)
				if err := realtime.TryPut(event); err != nil {
					t.Fatal(err)
				}
			}
			realtime.Close()
			resultBudget := mustBudget(t, writeResultWeight)
			results, _ := newBoundedQueue(1, resultBudget, weighWriteResult)
			results.Close()
			updateBudget := mustBudget(t, model.MaxNormalizedUpdateBytes)
			mailbox, _ := newFixedMailbox([]viewSlot{0, 1, 2, 3, 4, 5, 6, 7}, updateBudget, weighUpdate)
			shutdown := make(chan struct{})
			close(shutdown)
			drops := &saturatingCounter{}
			coordinator := &realtimeCoordinator{source: newCoreTestSource(), store: defaultHistoryStore(t), policy: coordinatorKeepPolicy{}, clock: newManualClock(writerTestTime), realtimeQ: realtime, liveWriteQ: live, resultQ: results, updates: mailbox, liveWriteBusy: time.Second, localDrops: drops, localDropWake: make(chan struct{}), shutdown: shutdown, finalLimit: limit}
			if err := coordinator.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if entries := live.Stats().Entries; entries != limit {
				t.Fatalf("live entries=%d", entries)
			}
			if drops.load() != 3 || realtime.Stats().Entries != 0 || realtime.Stats().UsedBytes != 0 {
				t.Fatalf("drops=%d realtime=%+v", drops.load(), realtime.Stats())
			}
			live.drainAndRelease()
			mailbox.drainAndRelease()
		})
	}
}

type coreTestSource struct {
	realtime chan model.Event
	metadata chan model.HistoryJob
	onDemand chan model.HistoryJob
	bulk     chan model.HistoryJob
	status   chan struct{}
}

func newCoreTestSource() *coreTestSource {
	return &coreTestSource{realtime: make(chan model.Event, 4), metadata: make(chan model.HistoryJob, 2), onDemand: make(chan model.HistoryJob, 1), bulk: make(chan model.HistoryJob, 1), status: make(chan struct{}, 1)}
}
func (source *coreTestSource) Run(ctx context.Context) error {
	close(source.realtime)
	close(source.metadata)
	close(source.onDemand)
	close(source.bulk)
	close(source.status)
	return nil
}
func (source *coreTestSource) RealtimeEvents() <-chan model.Event           { return source.realtime }
func (source *coreTestSource) MetadataHistoryJobs() <-chan model.HistoryJob { return source.metadata }
func (source *coreTestSource) OnDemandHistoryJobs() <-chan model.HistoryJob { return source.onDemand }
func (source *coreTestSource) BulkHistoryJobs() <-chan model.HistoryJob     { return source.bulk }
func (source *coreTestSource) StatusWake() <-chan struct{}                  { return source.status }
func (*coreTestSource) Status() model.SourceStatus                          { return model.SourceStatus{} }
func (*coreTestSource) NextHistory(context.Context, model.HistoryJob, int, int64) (model.HistoryChunk, bool, error) {
	return model.HistoryChunk{}, true, nil
}
func (*coreTestSource) ReleaseHistory(model.HistoryJob) {}
