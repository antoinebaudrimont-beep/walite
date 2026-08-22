package service

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func intWeight(value int) (int64, error) {
	if value <= 0 {
		return 0, errInvalidWeight
	}
	return int64(value), nil
}

func TestBoundedQueueConstructionAndTryPutBounds(t *testing.T) {
	budget := mustBudget(t, 10)
	for _, entries := range []int{0, -1} {
		if _, err := newBoundedQueue(entries, budget, intWeight); err == nil {
			t.Fatalf("entries %d succeeded", entries)
		}
	}
	if _, err := newBoundedQueue[int](1, nil, intWeight); err == nil {
		t.Fatal("nil budget succeeded")
	}
	if _, err := newBoundedQueue[int](1, budget, nil); err == nil {
		t.Fatal("nil weigh succeeded")
	}
	queue := mustQueue(t, 2, budget)
	if err := queue.TryPut(4); err != nil {
		t.Fatal(err)
	}
	if err := queue.TryPut(6); err != nil {
		t.Fatal(err)
	}
	if stats := queue.Stats(); stats.Entries != 2 || stats.UsedBytes != 10 {
		t.Fatalf("exact stats = %+v", stats)
	}
	if err := queue.TryPut(1); !errors.Is(err, errQueueFull) || budget.usedBytes() != 10 {
		t.Fatalf("entry-full error=%v used=%d", err, budget.usedBytes())
	}
	queue.drainAndRelease()
	if err := queue.TryPut(11); !errors.Is(err, errInvalidWeight) || budget.usedBytes() != 0 {
		t.Fatalf("oversized error=%v used=%d", err, budget.usedBytes())
	}
	if err := queue.TryPut(0); !errors.Is(err, errInvalidWeight) {
		t.Fatalf("invalid weight error=%v", err)
	}
	queue.Close()
	if err := queue.TryPut(1); !errors.Is(err, errQueueStopped) {
		t.Fatalf("stopped TryPut=%v", err)
	}
}

func TestBoundedQueueEntryAndByteSaturationIndependent(t *testing.T) {
	entryQueue := mustQueue(t, 1, mustBudget(t, 100))
	if err := entryQueue.TryPut(1); err != nil {
		t.Fatal(err)
	}
	if err := entryQueue.TryPut(1); !errors.Is(err, errQueueFull) || entryQueue.budget.usedBytes() != 1 {
		t.Fatalf("entry saturation error=%v used=%d", err, entryQueue.budget.usedBytes())
	}
	byteQueue := mustQueue(t, 3, mustBudget(t, 2))
	if err := byteQueue.TryPut(2); err != nil {
		t.Fatal(err)
	}
	if err := byteQueue.TryPut(1); !errors.Is(err, errQueueFull) || byteQueue.Stats().Entries != 1 {
		t.Fatalf("byte saturation error=%v stats=%+v", err, byteQueue.Stats())
	}
	entryQueue.drainAndRelease()
	byteQueue.drainAndRelease()
}

func TestBoundedQueueReservationOwnership(t *testing.T) {
	budget := mustBudget(t, 4)
	queue := mustQueue(t, 1, budget)
	owned, err := queue.tryReserve(2)
	if err != nil || budget.usedBytes() != 2 {
		t.Fatalf("reserve err=%v used=%d", err, budget.usedBytes())
	}
	if err := queue.putReserved(2, owned); err != nil {
		t.Fatal(err)
	}
	if budget.usedBytes() != 2 {
		t.Fatalf("double charged used=%d", budget.usedBytes())
	}
	second, _ := queue.tryReserve(1)
	if err := queue.putReserved(1, second); !errors.Is(err, errQueueFull) {
		t.Fatalf("full reserved=%v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("caller lost ownership: %v", err)
	}
	queue.Close()
	third, _ := queue.tryReserve(1)
	if err := queue.putReserved(1, third); !errors.Is(err, errQueueStopped) {
		t.Fatalf("stopped reserved=%v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
	queue.drainAndRelease()
	if budget.usedBytes() != 0 {
		t.Fatalf("leaked=%d", budget.usedBytes())
	}
}

func TestBoundedQueuePutWaitsForEntryAndCancellationReleases(t *testing.T) {
	budget := mustBudget(t, 10)
	queue := mustQueue(t, 1, budget)
	if err := queue.TryPut(2); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- queue.Put(context.Background(), 3) }()
	waitQueueStats(t, queue, func(stats queueStats) bool { return stats.EntryWaiters == 1 && stats.UsedBytes == 5 })
	first, ok := queue.TryTake()
	if !ok {
		t.Fatal("missing first value")
	}
	if err := <-result; err != nil {
		t.Fatalf("woken Put=%v", err)
	}
	if first.Value() != 2 {
		t.Fatalf("first=%d", first.Value())
	}
	_ = first.Release()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { result <- queue.Put(ctx, 4) }()
	waitQueueStats(t, queue, func(stats queueStats) bool { return stats.EntryWaiters == 1 && stats.UsedBytes == 7 })
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled entry wait=%v", err)
	}
	if budget.usedBytes() != 3 {
		t.Fatalf("cancel leaked permit used=%d", budget.usedBytes())
	}
	queue.Close()
	queue.drainAndRelease()
}

func TestBoundedQueuePutWaitsForBytesAndClose(t *testing.T) {
	budget := mustBudget(t, 2)
	queue := mustQueue(t, 2, budget)
	if err := queue.TryPut(2); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- queue.Put(context.Background(), 1) }()
	waitQueueStats(t, queue, func(stats queueStats) bool { return stats.ByteWaiters == 1 })
	lease, _ := queue.TryTake()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("byte-woken Put=%v", err)
	}

	go func() { result <- queue.Put(context.Background(), 2) }()
	waitQueueStats(t, queue, func(stats queueStats) bool { return stats.ByteWaiters == 1 })
	queue.Close()
	if err := <-result; !errors.Is(err, errQueueStopped) {
		t.Fatalf("close wake=%v", err)
	}
	queue.drainAndRelease()
	if budget.usedBytes() != 0 {
		t.Fatalf("close leaked=%d", budget.usedBytes())
	}
}

func TestBoundedQueuePutByteCancellationAndEntryClose(t *testing.T) {
	budget := mustBudget(t, 3)
	queue := mustQueue(t, 1, budget)
	if err := queue.TryPut(2); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { result <- queue.Put(ctx, 2) }()
	waitQueueStats(t, queue, func(stats queueStats) bool { return stats.ByteWaiters == 1 })
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("byte cancellation=%v", err)
	}
	if budget.usedBytes() != 2 {
		t.Fatalf("byte cancellation leaked=%d", budget.usedBytes())
	}
	go func() { result <- queue.Put(context.Background(), 1) }()
	waitQueueStats(t, queue, func(stats queueStats) bool { return stats.EntryWaiters == 1 && stats.UsedBytes == 3 })
	queue.Close()
	if err := <-result; !errors.Is(err, errQueueStopped) {
		t.Fatalf("entry close=%v", err)
	}
	if budget.usedBytes() != 2 {
		t.Fatalf("entry close leaked=%d", budget.usedBytes())
	}
	queue.drainAndRelease()
}

func TestBoundedQueueTakeFIFOLeaseCloseAndDrain(t *testing.T) {
	budget := mustBudget(t, 10)
	queue := mustQueue(t, 3, budget)
	if _, ok := queue.TryTake(); ok {
		t.Fatal("empty TryTake succeeded")
	}
	for _, value := range []int{1, 2, 3} {
		if err := queue.TryPut(value); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := queue.TryTake()
	if first.Value() != 1 || budget.usedBytes() != 6 {
		t.Fatalf("first=%d used=%d", first.Value(), budget.usedBytes())
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); !errors.Is(err, errPermitReleased) {
		t.Fatalf("duplicate lease release=%v", err)
	}
	queue.Close()
	second, err := queue.Take(context.Background())
	if err != nil || second.Value() != 2 {
		t.Fatalf("second=%d err=%v", second.Value(), err)
	}
	if drained := queue.drainAndRelease(); drained != 1 {
		t.Fatalf("drained=%d", drained)
	}
	if again := queue.drainAndRelease(); again != 0 {
		t.Fatalf("repeated drain=%d", again)
	}
	if budget.usedBytes() != 2 {
		t.Fatalf("outstanding lease charge=%d", budget.usedBytes())
	}
	_ = second.Release()
	if _, err := queue.Take(context.Background()); !errors.Is(err, errQueueStopped) {
		t.Fatalf("closed empty Take=%v", err)
	}
}

func TestBoundedQueueTakeWaitCancellationAndWakes(t *testing.T) {
	queue := mustQueue(t, 1, mustBudget(t, 2))
	type takeResult struct {
		value int
		err   error
	}
	result := make(chan takeResult, 1)
	go func() {
		lease, err := queue.Take(context.Background())
		if err != nil {
			result <- takeResult{err: err}
			return
		}
		value := lease.Value()
		releaseErr := lease.Release()
		result <- takeResult{value: value, err: releaseErr}
	}()
	if err := queue.TryPut(1); err != nil {
		t.Fatal(err)
	}
	if observed := <-result; observed.value != 1 || observed.err != nil {
		t.Fatalf("result=%+v", observed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := queue.Take(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Take=%v", err)
	}
	queue.Close()
	if stats := queue.Stats(); !stats.Stopped || stats.Entries != 0 || stats.UsedBytes != 0 {
		t.Fatalf("final stats=%+v", stats)
	}
	assertCoalesced(t, queue.spaceWake)
	assertCoalesced(t, queue.notEmptyWake)
	assertCoalesced(t, queue.stateWake)
	assertCoalesced(t, queue.budget.wake)
	if queue.spaceChanges() != queue.spaceWake || queue.notEmptyChanges() != queue.notEmptyWake || queue.budgetChanges() != queue.budget.wake || queue.stateChanges() != queue.stateWake {
		t.Fatal("wake accessor returned a different channel")
	}
}

func TestBoundedQueueRejectsSecondBlockingPut(t *testing.T) {
	budget := mustBudget(t, 3)
	queue := mustQueue(t, 1, budget)
	if err := queue.TryPut(2); err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { first <- queue.Put(context.Background(), 1) }()
	waitQueueStats(t, queue, func(stats queueStats) bool {
		return stats.EntryWaiters == 1 && stats.ByteWaiters == 0
	})
	if err := queue.Put(context.Background(), 1); !errors.Is(err, errQueueInvariant) {
		t.Fatalf("second blocking Put = %v", err)
	}
	if stats := queue.Stats(); stats.EntryWaiters > 1 || stats.ByteWaiters > 1 || stats.UsedBytes != 3 {
		t.Fatalf("invariant stats = %+v", stats)
	}
	queued, _ := queue.TryTake()
	if err := <-first; err != nil {
		t.Fatalf("first waiter failed: %v", err)
	}
	_ = queued.Release()
	queue.drainAndRelease()
}

func TestBoundedQueueConcurrentTryPut(t *testing.T) {
	queue := mustQueue(t, 16, mustBudget(t, 16))
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			err := queue.TryPut(1)
			if err != nil && !errors.Is(err, errQueueFull) {
				t.Errorf("TryPut: %v", err)
			}
		}()
	}
	close(start)
	group.Wait()
	stats := queue.Stats()
	if stats.Entries > 16 || stats.UsedBytes > 16 || stats.EntryWaiters != 0 || stats.ByteWaiters != 0 {
		t.Fatalf("concurrent TryPut stats = %+v", stats)
	}
	queue.drainAndRelease()
}

func TestBoundedQueueTakeRechecksStoppedBeforeReturning(t *testing.T) {
	queue := mustQueue(t, 1, mustBudget(t, 1))
	reached := make(chan struct{})
	resume := make(chan struct{})
	queue.beforeTakeWait = func() {
		close(reached)
		<-resume
	}
	result := make(chan lease[int], 1)
	errorsSeen := make(chan error, 1)
	go func() {
		owned, err := queue.Take(context.Background())
		result <- owned
		errorsSeen <- err
	}()
	<-reached
	if err := queue.TryPut(1); err != nil {
		t.Fatal(err)
	}
	queue.Close()
	close(resume)
	owned := <-result
	if err := <-errorsSeen; err != nil || owned.Value() != 1 {
		t.Fatalf("buffered Take = value %d err %v", owned.Value(), err)
	}
	_ = owned.Release()
	if _, err := queue.Take(context.Background()); !errors.Is(err, errQueueStopped) {
		t.Fatalf("subsequent Take = %v", err)
	}
}

func mustQueue(t *testing.T, entries int, budget *byteBudget) *boundedQueue[int] {
	t.Helper()
	queue, err := newBoundedQueue(entries, budget, intWeight)
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func waitQueueStats[T any](t *testing.T, queue *boundedQueue[T], ready func(queueStats) bool) {
	t.Helper()
	for !ready(queue.Stats()) {
		<-queue.stateChanges()
	}
}

func assertCoalesced(t *testing.T, wake chan struct{}) {
	t.Helper()
	signal(wake)
	signal(wake)
	if len(wake) != 1 {
		t.Fatalf("wake len=%d", len(wake))
	}
}
