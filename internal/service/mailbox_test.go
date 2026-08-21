package service

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func TestFixedMailboxConstruction(t *testing.T) {
	budget := mustBudget(t, 10)
	if _, err := newFixedMailbox[int, int](nil, budget, intWeight); err == nil {
		t.Fatal("empty keys succeeded")
	}
	if _, err := newFixedMailbox([]int{1, 1}, budget, intWeight); err == nil {
		t.Fatal("duplicate keys succeeded")
	}
	if _, err := newFixedMailbox[int, int]([]int{1}, nil, intWeight); err == nil {
		t.Fatal("nil budget succeeded")
	}
	if _, err := newFixedMailbox[int, int]([]int{1}, budget, nil); err == nil {
		t.Fatal("nil weigh succeeded")
	}
}

func TestFixedMailboxReplaceOrderVersionsAndOwnership(t *testing.T) {
	budget := mustBudget(t, 10)
	mailbox := mustMailbox(t, []int{2, 1}, budget)
	if err := mailbox.Replace(3, 1); !errors.Is(err, errUnknownMailboxKey) {
		t.Fatalf("unknown key=%v", err)
	}
	if err := mailbox.Replace(1, 3); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Replace(2, 2); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Replace(1, 4); err != nil {
		t.Fatal(err)
	}
	if budget.usedBytes() != 6 {
		t.Fatalf("replacement double slot used=%d", budget.usedBytes())
	}
	key, first, version, ok := mailbox.TakeDirty()
	if !ok || key != 2 || first.Value() != 2 || version != 1 {
		t.Fatalf("first=%d/%d/%d/%t", key, first.Value(), version, ok)
	}
	if budget.usedBytes() != 6 {
		t.Fatalf("lease lost charge=%d", budget.usedBytes())
	}
	_ = first.Release()
	key, second, version, ok := mailbox.TakeDirty()
	if !ok || key != 1 || second.Value() != 4 || version != 2 {
		t.Fatalf("second=%d/%d/%d/%t", key, second.Value(), version, ok)
	}
	_ = second.Release()
	if _, _, _, ok := mailbox.TakeDirty(); ok || budget.usedBytes() != 0 {
		t.Fatalf("duplicate dirty=%t used=%d", ok, budget.usedBytes())
	}
	if err := mailbox.Replace(1, 1); err != nil {
		t.Fatal(err)
	}
	_, later, version, _ := mailbox.TakeDirty()
	if version != 3 {
		t.Fatalf("continued version=%d", version)
	}
	_ = later.Release()
}

func TestFixedMailboxAtomicReplacementWeights(t *testing.T) {
	budget := mustBudget(t, 5)
	mailbox := mustMailbox(t, []int{1, 2}, budget)
	if err := mailbox.Replace(1, 3); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Replace(2, 2); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Replace(1, 4); !errors.Is(err, errQueueFull) {
		t.Fatalf("larger failure=%v", err)
	}
	key, old, version, ok := mailbox.TakeDirty()
	if !ok || key != 1 || old.Value() != 3 || version != 1 {
		t.Fatalf("failed replace mutated old=%d/%d/%d/%t", key, old.Value(), version, ok)
	}
	_ = old.Release()
	if err := mailbox.Replace(2, 1); err != nil {
		t.Fatal(err)
	}
	if budget.usedBytes() != 1 {
		t.Fatalf("smaller did not free=%d", budget.usedBytes())
	}
	if err := mailbox.Replace(2, 1); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Replace(2, 5); err != nil {
		t.Fatal(err)
	}
	if budget.usedBytes() != 5 {
		t.Fatalf("exact budget=%d", budget.usedBytes())
	}
	if err := mailbox.Replace(2, 6); !errors.Is(err, errInvalidWeight) {
		t.Fatalf("oversized=%v", err)
	}
	if err := mailbox.Replace(2, 0); !errors.Is(err, errInvalidWeight) {
		t.Fatalf("invalid=%v", err)
	}
	mailbox.drainAndRelease()
}

func TestFixedMailboxVersionSaturation(t *testing.T) {
	mailbox := mustMailbox(t, []int{1}, mustBudget(t, 2))
	mailbox.slots[0].version = math.MaxUint64 - 1
	if err := mailbox.Replace(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Replace(1, 1); err != nil {
		t.Fatal(err)
	}
	_, owned, version, _ := mailbox.TakeDirty()
	if version != math.MaxUint64 {
		t.Fatalf("version wrapped=%d", version)
	}
	_ = owned.Release()
}

func TestFixedMailboxCloseDrainAndTakenLease(t *testing.T) {
	budget := mustBudget(t, 10)
	mailbox := mustMailbox(t, []int{1, 2}, budget)
	_ = mailbox.Replace(1, 2)
	_ = mailbox.Replace(2, 3)
	_, taken, _, _ := mailbox.TakeDirty()
	mailbox.Close()
	mailbox.Close()
	select {
	case <-mailbox.changes():
	default:
		t.Fatal("close did not wake")
	}
	if err := mailbox.Replace(1, 1); !errors.Is(err, errQueueStopped) {
		t.Fatalf("stopped replace=%v", err)
	}
	if drained := mailbox.drainAndRelease(); drained != 1 {
		t.Fatalf("drained=%d", drained)
	}
	if again := mailbox.drainAndRelease(); again != 0 {
		t.Fatalf("repeat drain=%d", again)
	}
	if budget.usedBytes() != 2 {
		t.Fatalf("taken lease affected=%d", budget.usedBytes())
	}
	_ = taken.Release()
}

func TestFixedMailboxChangesCoalesce(t *testing.T) {
	mailbox := mustMailbox(t, []int{1}, mustBudget(t, 4))
	if err := mailbox.Replace(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Replace(1, 2); err != nil {
		t.Fatal(err)
	}
	if len(mailbox.wake) != 1 || mailbox.changes() != mailbox.wake {
		t.Fatalf("coalesced changes len=%d", len(mailbox.wake))
	}
	mailbox.drainAndRelease()
}

func TestFixedMailboxConcurrentReplaceAndTake(t *testing.T) {
	budget := mustBudget(t, 16)
	mailbox := mustMailbox(t, []int{0, 1, 2, 3}, budget)
	start := make(chan struct{})
	var group sync.WaitGroup
	for key := range 4 {
		key := key
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for range 100 {
				if err := mailbox.Replace(key, 1); err != nil {
					t.Errorf("replace: %v", err)
				}
			}
		}()
	}
	group.Add(1)
	go func() {
		defer group.Done()
		<-start
		for range 100 {
			_, owned, _, ok := mailbox.TakeDirty()
			if ok {
				_ = owned.Release()
			}
		}
	}()
	close(start)
	group.Wait()
	mailbox.drainAndRelease()
	if budget.usedBytes() != 0 {
		t.Fatalf("leaked=%d", budget.usedBytes())
	}
}

func mustMailbox(t *testing.T, keys []int, budget *byteBudget) *fixedMailbox[int, int] {
	t.Helper()
	mailbox, err := newFixedMailbox(keys, budget, intWeight)
	if err != nil {
		t.Fatal(err)
	}
	return mailbox
}
