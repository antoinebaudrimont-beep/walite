package service

import (
	"math"
	"sync"
)

type mailboxSlot[V any] struct {
	value   V
	permit  *permit
	version uint64
	dirty   bool
}

type fixedMailbox[K comparable, V any] struct {
	mu      sync.Mutex
	keys    []K
	index   map[K]int
	slots   []mailboxSlot[V]
	budget  *byteBudget
	weigh   weighFunc[V]
	stopped bool
	wake    chan struct{}
}

func newFixedMailbox[K comparable, V any](keys []K, budget *byteBudget, weigh weighFunc[V]) (*fixedMailbox[K, V], error) {
	if len(keys) == 0 || budget == nil || weigh == nil {
		return nil, errInvalidWeight
	}
	ownedKeys := append([]K(nil), keys...)
	index := make(map[K]int, len(keys))
	for position, key := range ownedKeys {
		if _, exists := index[key]; exists {
			return nil, errUnknownMailboxKey
		}
		index[key] = position
	}
	return &fixedMailbox[K, V]{keys: ownedKeys, index: index, slots: make([]mailboxSlot[V], len(keys)), budget: budget, weigh: weigh, wake: make(chan struct{}, 1)}, nil
}

func (mailbox *fixedMailbox[K, V]) Replace(key K, value V) error {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	position, exists := mailbox.index[key]
	if !exists {
		return errUnknownMailboxKey
	}
	if mailbox.stopped {
		return errQueueStopped
	}
	weight, err := mailbox.weigh(value)
	if err != nil || weight <= 0 {
		return errInvalidWeight
	}
	slot := &mailbox.slots[position]
	var owned *permit
	if slot.permit == nil {
		owned, err = mailbox.budget.tryAcquire(weight)
	} else {
		owned, err = mailbox.budget.replace(slot.permit, weight)
	}
	if err != nil {
		return err
	}
	slot.value = value
	slot.permit = owned
	slot.dirty = true
	if slot.version < math.MaxUint64 {
		slot.version++
	}
	signal(mailbox.wake)
	return nil
}

func (mailbox *fixedMailbox[K, V]) TakeDirty() (K, lease[V], uint64, bool) {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	for position, key := range mailbox.keys {
		slot := &mailbox.slots[position]
		if !slot.dirty || slot.permit == nil {
			continue
		}
		owned := lease[V]{value: slot.value, permit: slot.permit}
		version := slot.version
		var zero V
		slot.value = zero
		slot.permit = nil
		slot.dirty = false
		return key, owned, version, true
	}
	var key K
	return key, lease[V]{}, 0, false
}

func (mailbox *fixedMailbox[K, V]) Close() {
	mailbox.mu.Lock()
	if !mailbox.stopped {
		mailbox.stopped = true
		signal(mailbox.wake)
	}
	mailbox.mu.Unlock()
}

func (mailbox *fixedMailbox[K, V]) drainAndRelease() int {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	drained := 0
	for position := range mailbox.slots {
		slot := &mailbox.slots[position]
		if slot.permit == nil {
			continue
		}
		_ = slot.permit.Release()
		var zero V
		slot.value = zero
		slot.permit = nil
		slot.dirty = false
		drained++
	}
	return drained
}

func (mailbox *fixedMailbox[K, V]) changes() <-chan struct{} { return mailbox.wake }
