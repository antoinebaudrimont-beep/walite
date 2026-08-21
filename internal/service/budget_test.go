package service

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func TestByteBudgetConstructionAndTryAcquire(t *testing.T) {
	for _, capacity := range []int64{0, -1} {
		if _, err := newByteBudget(capacity); !errors.Is(err, errInvalidWeight) {
			t.Fatalf("capacity %d error = %v", capacity, err)
		}
	}
	budget := mustBudget(t, 10)
	for _, weight := range []int64{0, -1, 11, math.MaxInt64} {
		if _, err := budget.tryAcquire(weight); !errors.Is(err, errInvalidWeight) {
			t.Fatalf("weight %d error = %v", weight, err)
		}
	}
	owned, err := budget.tryAcquire(10)
	if err != nil || budget.usedBytes() != 10 || budget.capacityBytes() != 10 {
		t.Fatalf("exact acquire: used=%d capacity=%d err=%v", budget.usedBytes(), budget.capacityBytes(), err)
	}
	if _, err := budget.tryAcquire(1); !errors.Is(err, errQueueFull) || budget.usedBytes() != 10 {
		t.Fatalf("full acquire: used=%d err=%v", budget.usedBytes(), err)
	}
	if err := owned.Release(); err != nil || budget.usedBytes() != 0 {
		t.Fatalf("release: used=%d err=%v", budget.usedBytes(), err)
	}
	if err := owned.Release(); !errors.Is(err, errPermitReleased) {
		t.Fatalf("duplicate release = %v", err)
	}
}

func TestByteBudgetAcquireCancellationAndWake(t *testing.T) {
	budget := mustBudget(t, 5)
	full, _ := budget.tryAcquire(5)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := budget.acquire(cancelled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancel error = %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, err := budget.acquire(ctx, 1)
		result <- err
	}()
	<-started
	stop()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked cancellation = %v", err)
	}

	acquired := make(chan *permit, 1)
	go func() {
		owned, _ := budget.acquire(context.Background(), 5)
		acquired <- owned
	}()
	if err := full.Release(); err != nil {
		t.Fatal(err)
	}
	woken := <-acquired
	if woken == nil || budget.usedBytes() != 5 {
		t.Fatalf("wake acquire used=%d", budget.usedBytes())
	}
	_ = woken.Release()
}

func TestByteBudgetRejectsSecondBlockingWaiterAndClearsRegistration(t *testing.T) {
	budget := mustBudget(t, 1)
	full, _ := budget.tryAcquire(1)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := budget.acquire(ctx, 1); first <- err }()
	waitBudgetWaiter(t, budget)
	if _, err := budget.acquire(context.Background(), 1); !errors.Is(err, errQueueInvariant) {
		t.Fatalf("second waiter error = %v", err)
	}
	if budget.usedBytes() != 1 {
		t.Fatalf("invariant rejection changed used=%d", budget.usedBytes())
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first waiter cancellation = %v", err)
	}
	later := make(chan *permit, 1)
	go func() { owned, _ := budget.acquire(context.Background(), 1); later <- owned }()
	waitBudgetWaiter(t, budget)
	if err := full.Release(); err != nil {
		t.Fatal(err)
	}
	owned := <-later
	if owned == nil {
		t.Fatal("later waiter did not acquire")
	}
	_ = owned.Release()
}

func TestByteBudgetReplace(t *testing.T) {
	budget := mustBudget(t, 10)
	old, _ := budget.tryAcquire(6)
	smaller, err := budget.replace(old, 4)
	if err != nil || budget.usedBytes() != 4 {
		t.Fatalf("smaller replace: used=%d err=%v", budget.usedBytes(), err)
	}
	if err := old.Release(); !errors.Is(err, errPermitReleased) {
		t.Fatalf("old permit remains valid: %v", err)
	}
	equal, err := budget.replace(smaller, 4)
	if err != nil || budget.usedBytes() != 4 {
		t.Fatalf("equal replace: used=%d err=%v", budget.usedBytes(), err)
	}
	larger, err := budget.replace(equal, 9)
	if err != nil || budget.usedBytes() != 9 {
		t.Fatalf("larger replace: used=%d err=%v", budget.usedBytes(), err)
	}
	blocker, _ := budget.tryAcquire(1)
	if _, err := budget.replace(larger, 10); !errors.Is(err, errQueueFull) {
		t.Fatalf("failed larger replace = %v", err)
	}
	if budget.usedBytes() != 10 {
		t.Fatalf("failed replace changed used=%d", budget.usedBytes())
	}
	if err := larger.Release(); err != nil {
		t.Fatalf("failed replace invalidated old permit: %v", err)
	}
	_ = blocker.Release()
}

func TestByteBudgetConcurrentAccounting(t *testing.T) {
	budget := mustBudget(t, 32)
	start := make(chan struct{})
	var group sync.WaitGroup
	var maximum atomic.Int64
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			owned, err := budget.acquire(context.Background(), 1)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			for {
				used := budget.usedBytes()
				prior := maximum.Load()
				if used <= prior || maximum.CompareAndSwap(prior, used) {
					break
				}
			}
			if err := owned.Release(); err != nil {
				t.Errorf("release: %v", err)
			}
		}()
	}
	close(start)
	group.Wait()
	if maximum.Load() > budget.capacityBytes() || budget.usedBytes() != 0 {
		t.Fatalf("maximum=%d final=%d", maximum.Load(), budget.usedBytes())
	}
}

func mustBudget(t *testing.T, capacity int64) *byteBudget {
	t.Helper()
	budget, err := newByteBudget(capacity)
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func waitBudgetWaiter(t *testing.T, budget *byteBudget) {
	t.Helper()
	for {
		budget.mu.Lock()
		waiting := budget.waiter
		budget.mu.Unlock()
		if waiting {
			return
		}
		<-budget.stateWake
	}
}
