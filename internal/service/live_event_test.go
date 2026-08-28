package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

type postCommitMaintenanceStore struct {
	writerStore
	pruneErr error
	prunes   int
}

func (store *postCommitMaintenanceStore) ApplyPrune(context.Context, model.PrunePlan) (model.PruneResult, error) {
	store.prunes++
	return model.PruneResult{}, store.pruneErr
}

func TestLiveEventNotPublishedBeforeCommit(t *testing.T) {
	live, history, results := writerQueues(t)
	putMessage(t, live, "commit-barrier", 1)
	live.Close()
	history.Close()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	committed := make(chan model.LiveEvent, 1)
	store := &writerStore{entered: entered, release: release}
	writer, err := newLiveFirstWriter(store, live, history, results, newWriterClock(), time.Second, 1, writerRetention{liveEvents: committed})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- writer.Run(context.Background()) }()

	<-entered
	select {
	case event := <-committed:
		t.Fatalf("event published before commit: %q", event.Message().MessageID().String())
	default:
	}
	close(release)

	event, ok := <-committed
	if !ok || event.Message().MessageID().String() != "message-1" {
		t.Fatalf("committed event=%+v open=%t", event, ok)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := <-committed; ok {
		t.Fatal("live event channel not closed after writer completion")
	}
	results.drainAndRelease()
}

func TestLiveEventFailedCommitPublishesNothing(t *testing.T) {
	live, history, results := writerQueues(t)
	putMessage(t, live, "failed-commit", 1)
	live.Close()
	history.Close()

	committed := make(chan model.LiveEvent, 1)
	want := errors.New("controlled write failure")
	writer, err := newLiveFirstWriter(&writerStore{fail: want}, live, history, results, newWriterClock(), time.Second, 1, writerRetention{liveEvents: committed})
	if err != nil {
		t.Fatal(err)
	}
	if got := writer.Run(context.Background()); !errors.Is(got, want) {
		t.Fatalf("writer error=%v", got)
	}
	if event, ok := <-committed; ok {
		t.Fatalf("failed commit published event=%+v", event)
	}
	owned, ok := results.TryTake()
	if !ok || owned.Value() != (writeResult{Origin: model.WriteRealtime, Attempted: 1, StoreFailed: true}) {
		t.Fatalf("failed-commit result=%+v open=%t", owned.Value(), ok)
	}
	_ = owned.Release()
	results.drainAndRelease()
}

func TestPostCommitMaintenanceFailurePreservesWrittenResult(t *testing.T) {
	live, history, results := writerQueues(t)
	putMessage(t, live, "post-commit-maintenance", 1)
	live.Close()
	history.Close()

	want := errors.New("controlled post-commit maintenance failure")
	store := &postCommitMaintenanceStore{pruneErr: want}
	committed := make(chan model.LiveEvent, 1)
	writer, err := newLiveFirstWriter(store, live, history, results, newWriterClock(), time.Second, 1, writerRetention{
		policy: &historyPolicy{}, snapshotLimit: model.MaxRetentionSnapshotSummaries, liveEvents: committed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := writer.Run(context.Background()); !errors.Is(got, want) {
		t.Fatalf("writer error=%v", got)
	}
	event, ok := <-committed
	if !ok || event.Message().MessageID().String() != "message-1" {
		t.Fatalf("committed event=%+v open=%t", event, ok)
	}
	if _, ok := <-committed; ok {
		t.Fatal("live event channel remained open")
	}
	if len(store.observations()) != 1 || store.prunes != 1 {
		t.Fatalf("writes=%d prunes=%d", len(store.observations()), store.prunes)
	}
	owned, ok := results.TryTake()
	if !ok {
		t.Fatal("post-commit write result missing")
	}
	result := owned.Value()
	_ = owned.Release()
	if result != (writeResult{Origin: model.WriteRealtime, Attempted: 1, Written: 1, StoreFailed: true}) {
		t.Fatalf("post-commit result=%+v", result)
	}

	resultBudget := mustBudget(t, writeResultWeight)
	statusResults, err := newBoundedQueue(1, resultBudget, weighWriteResult)
	if err != nil {
		t.Fatal(err)
	}
	if err := statusResults.TryPut(result); err != nil {
		t.Fatal(err)
	}
	statusResults.Close()
	updateBudget := mustBudget(t, model.MaxNormalizedUpdateBytes)
	summarySlot, _ := updateSlot(model.UpdateSummary)
	mailbox, err := newFixedMailbox([]viewSlot{summarySlot}, updateBudget, weighUpdate)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &realtimeCoordinator{resultQ: statusResults, updates: mailbox}
	resultsOpen := true
	coordinator.handleResults(&resultsOpen)
	_, update, _, ok := mailbox.TakeDirty()
	if !ok || update.Value().Kind() != model.UpdateSummary || update.Value().Accepted() != 1 || !update.Value().Degraded() {
		t.Fatalf("coordinator summary=%+v open=%t", update.Value(), ok)
	}
	_ = update.Release()
	statusResults.drainAndRelease()
	results.drainAndRelease()
}

func TestLiveEventsPreserveCommittedBatchOrderWithoutCoalescing(t *testing.T) {
	live, history, results := writerQueues(t)
	for index := 1; index <= 3; index++ {
		putMessage(t, live, "ordered", index)
	}
	live.Close()
	history.Close()

	committed := make(chan model.LiveEvent, 3)
	writer, err := newLiveFirstWriter(&writerStore{}, live, history, results, newWriterClock(), time.Second, 3, writerRetention{liveEvents: committed})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		event, ok := <-committed
		if !ok || event.Message().MessageID().String() != fmt.Sprintf("message-%d", index) {
			t.Fatalf("event %d=%+v open=%t", index, event, ok)
		}
	}
	if _, ok := <-committed; ok {
		t.Fatal("unexpected fourth event")
	}
	results.drainAndRelease()
}

func TestLiveEventChannelAppliesBoundedBackpressureAndResumes(t *testing.T) {
	live, history, results := writerQueues(t)
	for index := 1; index <= 3; index++ {
		putMessage(t, live, "saturation", index)
	}
	live.Close()
	history.Close()

	committed := make(chan model.LiveEvent, 2)
	writer, err := newLiveFirstWriter(&writerStore{}, live, history, results, newWriterClock(), time.Second, 3, writerRetention{liveEvents: committed})
	if err != nil {
		t.Fatal(err)
	}
	published := make(chan int, 3)
	writer.afterLivePublished = func(index int) { published <- index }
	done := make(chan error, 1)
	go func() { done <- writer.Run(context.Background()) }()

	if first, second := <-published, <-published; first != 0 || second != 1 {
		t.Fatalf("published indexes=%d,%d", first, second)
	}
	if cap(committed) != 2 || len(committed) != 2 {
		t.Fatalf("live queue cap=%d len=%d", cap(committed), len(committed))
	}
	select {
	case err := <-done:
		t.Fatalf("writer bypassed backpressure: %v", err)
	default:
	}
	first := <-committed
	if first.Message().MessageID().String() != "message-1" {
		t.Fatalf("first event=%q", first.Message().MessageID().String())
	}
	if third := <-published; third != 2 {
		t.Fatalf("third published index=%d", third)
	}
	second, third := <-committed, <-committed
	if second.Message().MessageID().String() != "message-2" || third.Message().MessageID().String() != "message-3" {
		t.Fatalf("remaining order=%q,%q", second.Message().MessageID().String(), third.Message().MessageID().String())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := <-committed; ok {
		t.Fatal("live event channel remained open")
	}
	results.drainAndRelease()
}

func TestHistoryWritesNeverPublishLiveEvents(t *testing.T) {
	live, history, results := writerQueues(t)
	putMessage(t, history, "history-only", 1)
	live.Close()
	history.Close()

	committed := make(chan model.LiveEvent, 1)
	writer, err := newLiveFirstWriter(&writerStore{}, live, history, results, newWriterClock(), time.Second, 1, writerRetention{liveEvents: committed})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if event, ok := <-committed; ok {
		t.Fatalf("history published live event=%+v", event)
	}
	results.drainAndRelease()
}

func TestCommittedEventsRemainDrainableAcrossWriterShutdown(t *testing.T) {
	live, history, results := writerQueues(t)
	for index := 1; index <= 3; index++ {
		putMessage(t, live, "shutdown", index)
	}
	live.Close()
	history.Close()

	committed := make(chan model.LiveEvent, 3)
	writer, err := newLiveFirstWriter(&writerStore{}, live, history, results, newWriterClock(), time.Second, 3, writerRetention{liveEvents: committed})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		event, ok := <-committed
		if !ok || event.Message().MessageID().String() != fmt.Sprintf("message-%d", index) {
			t.Fatalf("shutdown event %d=%+v open=%t", index, event, ok)
		}
	}
	if _, ok := <-committed; ok {
		t.Fatal("live channel did not close exactly after buffered events")
	}
	results.drainAndRelease()
}
