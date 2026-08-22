package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

type lifecycleContextKey struct{}

type observingCoreSource struct {
	*coreTestSource
	runs       atomic.Uint32
	cancelled  atomic.Bool
	value      any
	runEntered chan struct{}
}

type finalEventSource struct {
	*coreTestSource
	event model.Event
}

type panicCoreSource struct{ *coreTestSource }

func (source *panicCoreSource) Run(context.Context) error {
	panic("private panic payload")
}

type failingCoreSource struct {
	*coreTestSource
	failure error
}

func (source *failingCoreSource) Run(context.Context) error { return source.failure }

func (source *finalEventSource) Run(ctx context.Context) error {
	<-ctx.Done()
	source.realtime <- source.event
	return source.coreTestSource.Run(ctx)
}

func newObservingCoreSource() *observingCoreSource {
	return &observingCoreSource{coreTestSource: newCoreTestSource(), runEntered: make(chan struct{})}
}

func (source *observingCoreSource) Run(ctx context.Context) error {
	source.runs.Add(1)
	source.value = ctx.Value(lifecycleContextKey{})
	source.cancelled.Store(ctx.Err() != nil)
	close(source.runEntered)
	return source.coreTestSource.Run(ctx)
}

func TestCoreNormalCompletionAndSingleUse(t *testing.T) {
	core, err := New(validCoreOptions(), newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var kinds []model.UpdateKind
	for update := range core.Updates() {
		kinds = append(kinds, update.Kind())
	}
	if len(kinds) == 0 || kinds[0] != model.UpdateReady {
		t.Fatalf("updates=%v", kinds)
	}
	if err := core.Run(context.Background()); err == nil {
		t.Fatal("second Run succeeded")
	}
}
func TestCoreCancellationDetectable(t *testing.T) {
	source := newObservingCoreSource()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	core, err := New(validCoreOptions(), source, defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	for update := range core.Updates() {
		if update.Kind() == model.UpdateReady {
			t.Fatal("Ready published after pre-commit cancellation")
		}
	}
	if source.runs.Load() != 1 || !source.cancelled.Load() {
		t.Fatalf("runs=%d cancelled=%t", source.runs.Load(), source.cancelled.Load())
	}
}

func TestCorePublicUpdateCapacity(t *testing.T) {
	core, err := New(validCoreOptions(), newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	if capacity := cap(core.Updates()); capacity != 1 {
		t.Fatalf("capacity=%d", capacity)
	}
}

func TestCorePreservesParentContextValues(t *testing.T) {
	source := newObservingCoreSource()
	parent := context.WithValue(context.Background(), lifecycleContextKey{}, "preserved")
	core, err := New(validCoreOptions(), source, defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Run(parent); err != nil {
		t.Fatal(err)
	}
	if source.value != "preserved" {
		t.Fatalf("value=%v", source.value)
	}
	for range core.Updates() {
	}
}

func TestStartupCancellationWinsBeforePublisherCommit(t *testing.T) {
	source := newObservingCoreSource()
	core, err := New(validCoreOptions(), source, defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	beforeCommit := make(chan struct{})
	aborted := make(chan struct{})
	releaseCommit := make(chan struct{})
	core.publisherBeforeCommitHook = func() {
		close(beforeCommit)
		<-releaseCommit
	}
	core.startupAbortedHook = func() { close(aborted) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx) }()
	<-beforeCommit
	cancel()
	<-aborted
	close(releaseCommit)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	for update := range core.Updates() {
		if update.Kind() == model.UpdateReady {
			t.Fatal("Ready published after startup abort")
		}
	}
	if source.runs.Load() != 1 || !source.cancelled.Load() {
		t.Fatalf("runs=%d cancelled=%t", source.runs.Load(), source.cancelled.Load())
	}
}

func TestStartupPublisherCommitWinsBeforeCancellation(t *testing.T) {
	core, err := New(validCoreOptions(), newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	committed := make(chan struct{})
	release := make(chan struct{})
	core.publisherCommittedHook = func() {
		close(committed)
		<-release
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx) }()
	<-committed
	cancel()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	first, ok := <-core.Updates()
	if !ok || first.Kind() != model.UpdateReady {
		t.Fatalf("first=%v open=%t", first.Kind(), ok)
	}
	for range core.Updates() {
	}
}

func TestGraceExpiryClosesPublisherWithoutMailboxLeak(t *testing.T) {
	message := historyMessage(t, "grace-live", 0)
	event, err := model.NewEvent(message, writerTestTime)
	if err != nil {
		t.Fatal(err)
	}
	source := &finalEventSource{coreTestSource: newCoreTestSource(), event: event}
	storeEntered := make(chan struct{}, 1)
	store := &writerStore{entered: storeEntered, release: make(chan struct{})}
	clock := newManualClock(writerTestTime)
	core, err := New(validCoreOptions(), source, store, &historyPolicy{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx) }()
	first := <-core.Updates()
	if first.Kind() != model.UpdateReady {
		t.Fatalf("first=%v", first.Kind())
	}
	cancel()
	<-storeEntered
	if err := clock.waitTimerRegistrations(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	clock.Advance(validCoreOptions().ShutdownGrace)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	for range core.Updates() {
	}
	if used := core.updateMailbox.budget.usedBytes(); used != 0 {
		t.Fatalf("mailbox bytes=%d", used)
	}
	assertCoreAccountingZero(t, core)
}

func TestCoreRecoversComponentPanicAsInvariant(t *testing.T) {
	core, err := New(validCoreOptions(), &panicCoreSource{newCoreTestSource()}, defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- core.Run(context.Background()) }()
	first := <-core.Updates()
	if first.Kind() != model.UpdateReady {
		t.Fatalf("first=%v", first.Kind())
	}
	runErr := <-done
	var coreErr *CoreError
	if !errors.As(runErr, &coreErr) || coreErr.Kind != CoreInvariant || coreErr.Operation != CoreOperationRun {
		t.Fatalf("Run=%v", runErr)
	}
	for range core.Updates() {
	}
	assertCoreAccountingZero(t, core)
}

func TestCorePreservesSourceFailureAsFirstCause(t *testing.T) {
	failure := errors.New("synthetic source failure")
	core, err := New(validCoreOptions(), &failingCoreSource{coreTestSource: newCoreTestSource(), failure: failure}, defaultHistoryStore(t), coordinatorKeepPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- core.Run(context.Background()) }()
	if first := <-core.Updates(); first.Kind() != model.UpdateReady {
		t.Fatalf("first=%v", first.Kind())
	}
	runErr := <-done
	if runErr != failure || !errors.Is(runErr, failure) {
		t.Fatalf("Run=%v", runErr)
	}
	for range core.Updates() {
	}
	assertCoreAccountingZero(t, core)
}

func TestCorePreservesWriterStoreFailureAsFirstCause(t *testing.T) {
	failure := errors.New("synthetic store failure")
	source := newCoreTestSource()
	message := historyMessage(t, "fatal-live", 0)
	event, _ := model.NewEvent(message, writerTestTime)
	source.realtime <- event
	core, err := New(validCoreOptions(), source, &writerStore{fail: failure}, coordinatorKeepPolicy{}, newManualClock(writerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- core.Run(context.Background()) }()
	if first := <-core.Updates(); first.Kind() != model.UpdateReady {
		t.Fatalf("first=%v", first.Kind())
	}
	if runErr := <-done; !errors.Is(runErr, failure) {
		t.Fatalf("Run=%v", runErr)
	}
	for range core.Updates() {
	}
	assertCoreAccountingZero(t, core)
}

func assertCoreAccountingZero(t *testing.T, core *Core) {
	t.Helper()
	stats := []queueStats{core.realtimeQ.Stats(), core.historyQ.Stats(), core.liveWriteQ.Stats(), core.historyWriteQ.Stats(), core.resultQ.Stats()}
	for index, stat := range stats {
		if stat.Entries != 0 || stat.UsedBytes != 0 || stat.EntryWaiters != 0 || stat.ByteWaiters != 0 {
			t.Fatalf("queue %d stats=%+v", index, stat)
		}
	}
	if used := core.updateMailbox.budget.usedBytes(); used != 0 {
		t.Fatalf("mailbox bytes=%d", used)
	}
}
