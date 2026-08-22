package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

var writerTestTime = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

type writeObservation struct {
	origin model.WriteOrigin
	count  int
	ids    [model.MaxWriteBatchMessages]string
	bodies [model.MaxWriteBatchMessages]bool
}

type writerStore struct {
	mu             sync.Mutex
	calls          [32]writeObservation
	count          int
	entered        chan struct{}
	release        <-chan struct{}
	fail           error
	historyEntered chan struct{}
	historyRelease <-chan struct{}
}

type panicWriterStore struct{ writerStore }

func (*panicWriterStore) Write(context.Context, model.WriteBatch) error {
	panic("private writer panic")
}

type discardPruneStore struct {
	writerStore
	snapshots int
	usages    int
	prunes    int
}

func (store *discardPruneStore) RetentionSnapshot(_ context.Context, id model.ChatID) (model.RetentionSnapshot, error) {
	store.snapshots++
	return model.NewRetentionSnapshot(id, nil, nil)
}
func (store *discardPruneStore) Usage(context.Context) (model.CacheUsage, error) {
	store.usages++
	return model.NewCacheUsage(0, 0, 0, 0, 0)
}
func (store *discardPruneStore) ApplyPrune(context.Context, model.PrunePlan) (model.PruneResult, error) {
	store.prunes++
	return model.NewPruneResult(0, 0, false)
}

type discardPrunePolicy struct{ plans int }

func (*discardPrunePolicy) Decide(model.Message, model.RetentionState) (model.RetentionDecision, error) {
	return model.NewRetentionDecision(model.Discard, model.RetentionEligible)
}
func (policy *discardPrunePolicy) PlanPrune(model.RetentionSnapshot, model.RetentionState) (model.PrunePlan, error) {
	policy.plans++
	return model.PrunePlan{}, nil
}

func (store *writerStore) Write(ctx context.Context, batch model.WriteBatch) error {
	store.mu.Lock()
	if store.count == len(store.calls) {
		store.mu.Unlock()
		return errQueueInvariant
	}
	observation := &store.calls[store.count]
	observation.origin = batch.Origin()
	observation.count = batch.Len()
	for index := 0; index < batch.Len(); index++ {
		message, _ := batch.At(index)
		observation.ids[index] = message.MessageID().String()
		observation.bodies[index] = message.BodyRetained()
	}
	store.count++
	entered, release, failure := store.entered, store.release, store.fail
	if batch.Origin() == model.WriteHistory && store.historyEntered != nil {
		entered, release = store.historyEntered, store.historyRelease
	}
	store.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
		}
	}
	return failure
}

func (*writerStore) EnsureChat(context.Context, model.Chat) error { return nil }
func (*writerStore) Page(context.Context, model.ChatID, model.Cursor, int) ([]model.Message, model.Cursor, error) {
	return nil, model.NoCursor(), nil
}
func (*writerStore) RetentionSnapshot(_ context.Context, id model.ChatID) (model.RetentionSnapshot, error) {
	return model.NewRetentionSnapshot(id, nil, nil)
}
func (*writerStore) ApplyPrune(context.Context, model.PrunePlan) (model.PruneResult, error) {
	return model.NewPruneResult(0, 0, false)
}
func (*writerStore) Usage(context.Context) (model.CacheUsage, error) {
	return model.NewCacheUsage(0, 0, 0, 0, 0)
}

func (store *writerStore) observations() []writeObservation {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]writeObservation, store.count)
	copy(result, store.calls[:store.count])
	return result
}

type writerClock struct {
	mu         sync.Mutex
	registered chan struct{}
	timers     [8]*writerTimer
	count      int
}

type writerTimer struct {
	mu     sync.Mutex
	ch     chan time.Time
	active bool
}

func newWriterClock() *writerClock  { return &writerClock{registered: make(chan struct{}, 1)} }
func (*writerClock) Now() time.Time { return writerTestTime }
func (clock *writerClock) NewTimer(time.Duration) Timer {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if clock.count == len(clock.timers) {
		panic("writer test timer capacity")
	}
	timer := &writerTimer{ch: make(chan time.Time, 1), active: true}
	clock.timers[clock.count] = timer
	clock.count++
	signal(clock.registered)
	return timer
}
func (timer *writerTimer) C() <-chan time.Time { return timer.ch }
func (timer *writerTimer) Stop() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	wasActive := timer.active
	timer.active = false
	return wasActive
}
func (clock *writerClock) fireLatest(t *testing.T) {
	t.Helper()
	<-clock.registered
	clock.mu.Lock()
	timer := clock.timers[clock.count-1]
	clock.mu.Unlock()
	timer.mu.Lock()
	if timer.active {
		timer.active = false
		timer.ch <- writerTestTime
	}
	timer.mu.Unlock()
}

func TestLiveFirstWriterConstruction(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	clock := newWriterClock()
	tests := []struct {
		name    string
		store   MessageStore
		live    *boundedQueue[model.Message]
		history *boundedQueue[model.Message]
		results *boundedQueue[writeResult]
		clock   Clock
		wait    time.Duration
		max     int
	}{
		{"nil store", nil, live, history, results, clock, time.Second, 1},
		{"nil live", store, nil, history, results, clock, time.Second, 1},
		{"nil history", store, live, nil, results, clock, time.Second, 1},
		{"nil results", store, live, history, nil, clock, time.Second, 1},
		{"nil clock", store, live, history, results, nil, time.Second, 1},
		{"zero wait", store, live, history, results, clock, 0, 1},
		{"negative wait", store, live, history, results, clock, -1, 1},
		{"zero max", store, live, history, results, clock, time.Second, 0},
		{"negative max", store, live, history, results, clock, time.Second, -1},
		{"above max", store, live, history, results, clock, time.Second, 51},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newLiveFirstWriter(test.store, test.live, test.history, test.results, test.clock, test.wait, test.max); err == nil {
				t.Fatal("construction succeeded")
			}
		})
	}
	if _, err := newLiveFirstWriter(store, live, history, results, clock, time.Second, 50); err != nil {
		t.Fatalf("valid construction: %v", err)
	}
}

func TestWriterLiveBatchFIFOAndResult(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	for index := 0; index < 3; index++ {
		putMessage(t, live, "live", index)
	}
	live.Close()
	history.Close()
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 3)
	if err := writer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := store.observations()
	if len(calls) != 1 || calls[0].origin != model.WriteRealtime || calls[0].count != 3 || calls[0].ids[0] != "message-0" || calls[0].ids[2] != "message-2" {
		t.Fatalf("calls = %+v", calls)
	}
	result, ok := results.TryTake()
	if !ok || result.Value() != (writeResult{Origin: model.WriteRealtime, Attempted: 3, Written: 3}) {
		t.Fatalf("result=%+v ok=%t", result.Value(), ok)
	}
	_ = result.Release()
	if _, err := results.Take(context.Background()); !errors.Is(err, errQueueStopped) {
		t.Fatalf("result queue not closed: %v", err)
	}
	assertWriterBudgetsZero(t, live, history)
}

func TestWriterLiveConfiguredAndHardCaps(t *testing.T) {
	for _, maximum := range []int{7, 50} {
		t.Run(strconv.Itoa(maximum), func(t *testing.T) {
			store := &writerStore{}
			live, history, results := writerQueues(t)
			for index := 0; index < maximum+1; index++ {
				putMessage(t, live, "live", index)
			}
			live.Close()
			history.Close()
			writer := mustWriter(t, store, live, history, results, newWriterClock(), maximum)
			runWithResultDrain(t, writer, results)
			calls := store.observations()
			if len(calls) != 2 || calls[0].count != maximum || calls[1].count != 1 {
				t.Fatalf("max %d calls=%+v", maximum, calls)
			}
		})
	}
}

func TestWriterTimerCommitsPartialAndCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	store := &writerStore{entered: entered}
	live, history, results := writerQueues(t)
	clock := newWriterClock()
	putMessage(t, live, "live", 0)
	writer := mustWriter(t, store, live, history, results, clock, 4)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- writer.Run(ctx) }()
	clock.fireLatest(t)
	<-entered
	<-live.budgetChanges()
	if live.budget.usedBytes() != 0 {
		t.Fatalf("lease not released after timer write: %d", live.budget.usedBytes())
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	if calls := store.observations(); len(calls) != 1 || calls[0].count != 1 {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestWriterLeasesChargedUntilStoreReturns(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	store := &writerStore{entered: entered, release: release}
	live, history, results := writerQueues(t)
	putMessage(t, live, "live", 0)
	live.Close()
	history.Close()
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 1)
	done := make(chan error, 1)
	go func() { done <- writer.Run(context.Background()) }()
	<-entered
	if live.budget.usedBytes() == 0 {
		t.Fatal("lease released before store returned")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if live.budget.usedBytes() != 0 {
		t.Fatal("lease remained charged")
	}
}

func TestWriterHistoryCapsAndNoTimer(t *testing.T) {
	for _, maximum := range []int{10, 50} {
		store := &writerStore{}
		live, history, results := writerQueues(t)
		count := maximum
		wantFirst := maximum
		if wantFirst > 25 {
			count = 26
			wantFirst = 25
		}
		for index := 0; index < count; index++ {
			putMessage(t, history, "history", index)
		}
		live.Close()
		history.Close()
		clock := newWriterClock()
		writer := mustWriter(t, store, live, history, results, clock, maximum)
		runWithResultDrain(t, writer, results)
		calls := store.observations()
		if calls[0].origin != model.WriteHistory || calls[0].count != wantFirst || clock.count != 0 {
			t.Fatalf("history max=%d calls=%+v timers=%d", maximum, calls, clock.count)
		}
	}
}

func TestWriterStrictLivePreferenceAndPendingHistory(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	putMessage(t, history, "history", 0)
	reached := make(chan struct{})
	resume := make(chan struct{})
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 1)
	writer.afterHistorySelected = func() { close(reached); <-resume }
	done := make(chan error, 1)
	go func() { done <- writer.Run(context.Background()) }()
	<-reached
	putMessage(t, live, "live", 0)
	live.Close()
	history.Close()
	close(resume)
	runResultDrainUntilDone(t, done, results)
	calls := store.observations()
	if len(calls) != 2 || calls[0].origin != model.WriteRealtime || calls[1].origin != model.WriteHistory {
		t.Fatalf("priority calls=%+v", calls)
	}
}

func TestWriterBothReadyWritesLiveFirst(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	putMessage(t, history, "history", 0)
	putMessage(t, live, "live", 0)
	live.Close()
	history.Close()
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 1)
	runWithResultDrain(t, writer, results)
	calls := store.observations()
	if len(calls) != 2 || calls[0].origin != model.WriteRealtime || calls[1].origin != model.WriteHistory {
		t.Fatalf("both-ready calls=%+v", calls)
	}
}

func TestWriterRechecksLiveAfterHistoryBatch(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	store := &writerStore{entered: entered, release: release}
	live, history, results := writerQueues(t)
	for index := 0; index < 26; index++ {
		putMessage(t, history, "history", index)
	}
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 50)
	done := make(chan error, 1)
	go func() { done <- writer.Run(context.Background()) }()
	<-entered
	putMessage(t, live, "live", 0)
	live.Close()
	history.Close()
	close(release)
	runResultDrainUntilDone(t, done, results)
	calls := store.observations()
	if len(calls) != 3 || calls[0].origin != model.WriteHistory || calls[0].count != 25 || calls[1].origin != model.WriteRealtime || calls[2].origin != model.WriteHistory {
		t.Fatalf("recheck calls=%+v", calls)
	}
}

func TestWriterCancellationDrainsPendingAndBuffered(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	for index := 0; index < 3; index++ {
		putMessage(t, history, "history", index)
		putMessage(t, live, "live", index)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 3)
	if err := writer.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	if len(store.observations()) != 0 {
		t.Fatal("write began after cancellation")
	}
	assertWriterBudgetsZero(t, live, history)
	if !results.Stats().Stopped {
		t.Fatal("result queue remains open")
	}
}

func TestWriterCancellationWhileLiveTimerPending(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	clock := newWriterClock()
	putMessage(t, live, "live", 0)
	writer := mustWriter(t, store, live, history, results, clock, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- writer.Run(ctx) }()
	<-clock.registered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	if len(store.observations()) != 0 {
		t.Fatal("store called while timer cancellation was pending")
	}
	assertWriterBudgetsZero(t, live, history)
}

func TestWriterCancellationReleasesPendingHistory(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	putMessage(t, history, "history", 0)
	reached := make(chan struct{})
	resume := make(chan struct{})
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 1)
	writer.afterHistorySelected = func() { close(reached); <-resume }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- writer.Run(ctx) }()
	<-reached
	cancel()
	close(resume)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	if len(store.observations()) != 0 {
		t.Fatal("pending history was written after cancellation")
	}
	assertWriterBudgetsZero(t, live, history)
}

func TestWriterStoreErrorReleasesEverything(t *testing.T) {
	failure := errors.New("writer store failure")
	store := &writerStore{fail: failure}
	live, history, results := writerQueues(t)
	putMessage(t, live, "live", 0)
	putMessage(t, history, "history", 0)
	live.Close()
	history.Close()
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 1)
	err := writer.Run(context.Background())
	if !errors.Is(err, failure) || len(store.observations()) != 1 {
		t.Fatalf("Run=%v calls=%d", err, len(store.observations()))
	}
	assertWriterBudgetsZero(t, live, history)
	if !results.Stats().Stopped {
		t.Fatal("result queue remains open")
	}
}

func TestWriterMalformedMessageAndResultBackpressureCancellation(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	putRawMessage(t, live, model.Message{})
	live.Close()
	history.Close()
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 1)
	if err := writer.Run(context.Background()); err == nil {
		t.Fatal("malformed message succeeded")
	}
	assertWriterBudgetsZero(t, live, history)

	entered := make(chan struct{}, 1)
	store = &writerStore{entered: entered}
	live, history, results = writerQueues(t)
	if err := results.TryPut(writeResult{}); err != nil {
		t.Fatal(err)
	}
	putMessage(t, live, "live", 0)
	live.Close()
	history.Close()
	writer = mustWriter(t, store, live, history, results, newWriterClock(), 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- writer.Run(ctx) }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("backpressure Run=%v", err)
	}
	assertWriterBudgetsZero(t, live, history)
}

func TestWriterAuthoritativeRetentionCounts(t *testing.T) {
	for _, test := range []struct {
		name       string
		action     model.RetentionAction
		wantWrites int
		wantKept   int
		wantDrop   int
	}{
		{name: "keep metadata", action: model.KeepMetadata, wantWrites: 1, wantKept: 1},
		{name: "discard", action: model.Discard, wantDrop: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &writerStore{}
			policy := &historyPolicy{actions: [model.MaxHistoryChunkRecords]model.RetentionAction{test.action}}
			live, history, results := writerQueues(t)
			putMessage(t, live, "live", 0)
			live.Close()
			history.Close()
			writer, err := newLiveFirstWriter(store, live, history, results, newWriterClock(), time.Second, 1, writerRetention{policy: policy, snapshotLimit: model.MaxRetentionSnapshotSummaries})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- writer.Run(context.Background()) }()
			var observed writeResult
			for {
				if owned, ok := results.TryTake(); ok {
					observed = owned.Value()
					_ = owned.Release()
				}
				select {
				case runErr := <-done:
					if runErr != nil {
						t.Fatal(runErr)
					}
					if owned, ok := results.TryTake(); ok {
						observed = owned.Value()
						_ = owned.Release()
					}
					results.drainAndRelease()
					if len(store.observations()) != test.wantWrites || observed.Written != test.wantKept || observed.Discarded != test.wantDrop {
						t.Fatalf("writes=%d result=%+v", len(store.observations()), observed)
					}
					if test.action == model.KeepMetadata && store.observations()[0].bodies[0] {
						t.Fatal("KeepMetadata reached store with a body")
					}
					return
				case <-results.notEmptyChanges():
				}
			}
		})
	}
}

func TestWriterHistoryStoreCancellationAbandonsHistoryButKeepsLive(t *testing.T) {
	historyEntered := make(chan struct{}, 1)
	store := &writerStore{historyEntered: historyEntered, historyRelease: make(chan struct{})}
	live, history, results := writerQueues(t)
	putMessage(t, history, "history", 0)
	historyCtx, cancelHistory := context.WithCancel(context.Background())
	writer, err := newLiveFirstWriter(store, live, history, results, newWriterClock(), time.Second, 1, writerRetention{policy: &historyPolicy{}, snapshotLimit: model.MaxRetentionSnapshotSummaries, historyStoreCtx: historyCtx})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- writer.Run(context.Background()) }()
	<-historyEntered
	cancelHistory()
	putMessage(t, live, "live", 0)
	live.Close()
	history.Close()
	runResultDrainUntilDone(t, done, results)
	calls := store.observations()
	if len(calls) != 2 || calls[0].origin != model.WriteHistory || calls[1].origin != model.WriteRealtime {
		t.Fatalf("calls=%+v", calls)
	}
	assertWriterBudgetsZero(t, live, history)
}

func TestWriterHistoryPerformsAuthoritativeRetentionDecision(t *testing.T) {
	store := &writerStore{}
	policy := &historyPolicy{actions: [model.MaxHistoryChunkRecords]model.RetentionAction{model.KeepMetadata}}
	live, history, results := writerQueues(t)
	putMessage(t, history, "history", 0)
	live.Close()
	history.Close()
	writer, err := newLiveFirstWriter(store, live, history, results, newWriterClock(), time.Second, 1, writerRetention{policy: policy, snapshotLimit: model.MaxRetentionSnapshotSummaries})
	if err != nil {
		t.Fatal(err)
	}
	runWithResultDrain(t, writer, results)
	calls := store.observations()
	if policy.count != 1 || len(calls) != 1 || calls[0].origin != model.WriteHistory || calls[0].bodies[0] {
		t.Fatalf("decisions=%d calls=%+v", policy.count, calls)
	}
}

func TestWriterCancelledHistoryContextPreventsHistoryStoreCall(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	putMessage(t, history, "history", 0)
	live.Close()
	history.Close()
	historyCtx, cancelHistory := context.WithCancel(context.Background())
	cancelHistory()
	writer, err := newLiveFirstWriter(store, live, history, results, newWriterClock(), time.Second, 1, writerRetention{policy: &historyPolicy{}, snapshotLimit: model.MaxRetentionSnapshotSummaries, historyStoreCtx: historyCtx})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls := store.observations(); len(calls) != 0 {
		t.Fatalf("calls=%+v", calls)
	}
	assertWriterBudgetsZero(t, live, history)
}

func TestWriterShutdownAbandonsHistoryAndWritesOneFinalLiveQuantum(t *testing.T) {
	store := &writerStore{}
	live, history, results := writerQueues(t)
	for index := 0; index < 10; index++ {
		putMessage(t, live, "live", index)
		putMessage(t, history, "history", index)
	}
	live.Close()
	history.Close()
	shutdown := make(chan struct{})
	close(shutdown)
	writer, err := newLiveFirstWriter(store, live, history, results, newWriterClock(), time.Second, 7, writerRetention{policy: &historyPolicy{}, snapshotLimit: model.MaxRetentionSnapshotSummaries, historyStoreCtx: context.Background(), shutdown: shutdown, finalLiveLimit: 7})
	if err != nil {
		t.Fatal(err)
	}
	runWithResultDrain(t, writer, results)
	calls := store.observations()
	if len(calls) != 1 || calls[0].origin != model.WriteRealtime || calls[0].count != 7 {
		t.Fatalf("calls=%+v", calls)
	}
	assertWriterBudgetsZero(t, live, history)
}

func TestWriterReleasesLiveBatchWhenStorePanics(t *testing.T) {
	store := &panicWriterStore{}
	live, history, results := writerQueues(t)
	putMessage(t, live, "live", 0)
	live.Close()
	history.Close()
	writer := mustWriter(t, store, live, history, results, newWriterClock(), 1)
	panicValue := runWriterAndRecover(writer)
	if panicValue == nil {
		t.Fatal("store panic was not propagated")
	}
	assertWriterBudgetsZero(t, live, history)
}

func TestWriterReleasesHistoryBatchWhenPolicyPanics(t *testing.T) {
	live, history, results := writerQueues(t)
	putMessage(t, history, "history", 0)
	live.Close()
	history.Close()
	writer, err := newLiveFirstWriter(&writerStore{}, live, history, results, newWriterClock(), time.Second, 1, writerRetention{policy: panicWriterPolicy{}, snapshotLimit: model.MaxRetentionSnapshotSummaries})
	if err != nil {
		t.Fatal(err)
	}
	panicValue := runWriterAndRecover(writer)
	if panicValue == nil {
		t.Fatal("policy panic was not propagated")
	}
	assertWriterBudgetsZero(t, live, history)
}

type panicWriterPolicy struct{}

func (panicWriterPolicy) Decide(model.Message, model.RetentionState) (model.RetentionDecision, error) {
	panic("private policy panic")
}
func (panicWriterPolicy) PlanPrune(model.RetentionSnapshot, model.RetentionState) (model.PrunePlan, error) {
	return model.PrunePlan{}, nil
}

func runWriterAndRecover(writer *liveFirstWriter) (panicValue any) {
	func() {
		defer func() { panicValue = recover() }()
		_ = writer.Run(context.Background())
	}()
	return panicValue
}

func TestWriterPrunesAffectedDiscardedChats(t *testing.T) {
	for _, test := range []struct {
		name      string
		chatIDs   [2]string
		wantChats int
	}{
		{name: "same chat", chatIDs: [2]string{"discard-chat", "discard-chat"}, wantChats: 1},
		{name: "different chats", chatIDs: [2]string{"discard-chat-a", "discard-chat-b"}, wantChats: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &discardPruneStore{}
			policy := &discardPrunePolicy{}
			live, history, results := writerQueues(t)
			for index, chatID := range test.chatIDs {
				message, err := model.NewMessage(model.MessageInput{ChatID: chatID, MessageID: "discard-" + strconv.Itoa(index), SentAt: writerTestTime.Add(time.Duration(index) * time.Second), Text: "body"})
				if err != nil {
					t.Fatal(err)
				}
				putRawMessage(t, live, message)
			}
			live.Close()
			history.Close()
			writer, err := newLiveFirstWriter(store, live, history, results, newWriterClock(), time.Second, 2, writerRetention{policy: policy, snapshotLimit: model.MaxRetentionSnapshotSummaries})
			if err != nil {
				t.Fatal(err)
			}
			runWithResultDrain(t, writer, results)
			if len(store.observations()) != 0 || policy.plans != test.wantChats || store.prunes != test.wantChats {
				t.Fatalf("writes=%d plans=%d prunes=%d", len(store.observations()), policy.plans, store.prunes)
			}
			if store.snapshots != 2+test.wantChats || store.usages != 2+test.wantChats {
				t.Fatalf("snapshots=%d usages=%d", store.snapshots, store.usages)
			}
		})
	}
}

func writerQueues(t *testing.T) (*boundedQueue[model.Message], *boundedQueue[model.Message], *boundedQueue[writeResult]) {
	t.Helper()
	weighMessage := func(message model.Message) (int64, error) {
		size := message.ByteSize()
		if size <= 0 {
			return 1, nil
		}
		return int64(size), nil
	}
	liveBudget := mustBudget(t, 1<<20)
	historyBudget := mustBudget(t, 1<<20)
	resultBudget := mustBudget(t, writeResultWeight)
	live, err := newBoundedQueue(64, liveBudget, weighMessage)
	if err != nil {
		t.Fatal(err)
	}
	history, err := newBoundedQueue(64, historyBudget, weighMessage)
	if err != nil {
		t.Fatal(err)
	}
	results, err := newBoundedQueue(1, resultBudget, weighWriteResult)
	if err != nil {
		t.Fatal(err)
	}
	return live, history, results
}

func mustWriter(t *testing.T, store MessageStore, live *boundedQueue[model.Message], history *boundedQueue[model.Message], results *boundedQueue[writeResult], clock Clock, maximum int) *liveFirstWriter {
	t.Helper()
	writer, err := newLiveFirstWriter(store, live, history, results, clock, time.Second, maximum)
	if err != nil {
		t.Fatal(err)
	}
	return writer
}

func putMessage(t *testing.T, queue *boundedQueue[model.Message], lane string, index int) {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{ChatID: lane + "-chat", MessageID: "message-" + strconv.Itoa(index), SentAt: writerTestTime.Add(time.Duration(index) * time.Second), Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	putRawMessage(t, queue, message)
}
func putRawMessage(t *testing.T, queue *boundedQueue[model.Message], message model.Message) {
	t.Helper()
	if err := queue.TryPut(message); err != nil {
		t.Fatal(err)
	}
}

func assertWriterBudgetsZero(t *testing.T, queues ...*boundedQueue[model.Message]) {
	t.Helper()
	for _, queue := range queues {
		if used := queue.budget.usedBytes(); used != 0 {
			t.Fatalf("budget used=%d", used)
		}
	}
}

func runWithResultDrain(t *testing.T, writer *liveFirstWriter, results *boundedQueue[writeResult]) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- writer.Run(context.Background()) }()
	runResultDrainUntilDone(t, done, results)
}
func runResultDrainUntilDone(t *testing.T, done <-chan error, results *boundedQueue[writeResult]) {
	t.Helper()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			results.drainAndRelease()
			return
		case <-results.notEmptyChanges():
			if owned, ok := results.TryTake(); ok {
				_ = owned.Release()
			}
		}
	}
}
