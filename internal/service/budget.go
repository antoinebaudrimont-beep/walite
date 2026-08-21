package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	errInvalidWeight     = errors.New("invalid weight")
	errQueueFull         = errors.New("queue full")
	errQueueStopped      = errors.New("queue stopped")
	errUnknownMailboxKey = errors.New("unknown mailbox key")
	errPermitReleased    = errors.New("permit released")
	errQueueInvariant    = errors.New("queue invariant violation")
)

type byteBudget struct {
	mu        sync.Mutex
	capacity  int64
	used      int64
	wake      chan struct{}
	stateWake chan struct{}
	waiter    bool
}

type permit struct {
	budget   *byteBudget
	weight   int64
	released atomic.Bool
}

func newByteBudget(capacity int64) (*byteBudget, error) {
	if capacity <= 0 {
		return nil, errInvalidWeight
	}
	return &byteBudget{capacity: capacity, wake: make(chan struct{}, 1), stateWake: make(chan struct{}, 1)}, nil
}

func (budget *byteBudget) tryAcquire(weight int64) (*permit, error) {
	if budget == nil || weight <= 0 {
		return nil, errInvalidWeight
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if weight > budget.capacity {
		return nil, errInvalidWeight
	}
	if budget.used > budget.capacity-weight {
		return nil, errQueueFull
	}
	budget.used += weight
	return &permit{budget: budget, weight: weight}, nil
}

func (budget *byteBudget) acquire(ctx context.Context, weight int64) (*permit, error) {
	if ctx == nil {
		return nil, errQueueStopped
	}
	if budget == nil || weight <= 0 || weight > budget.capacityBytes() {
		return nil, errInvalidWeight
	}
	if acquired, err := budget.tryAcquire(weight); err == nil {
		return acquired, nil
	} else if !errors.Is(err, errQueueFull) {
		return nil, err
	}
	budget.mu.Lock()
	if budget.used <= budget.capacity-weight {
		budget.used += weight
		budget.mu.Unlock()
		return &permit{budget: budget, weight: weight}, nil
	}
	if budget.waiter {
		budget.mu.Unlock()
		return nil, errQueueInvariant
	}
	budget.waiter = true
	signal(budget.stateWake)
	budget.mu.Unlock()
	defer func() {
		budget.mu.Lock()
		budget.waiter = false
		signal(budget.stateWake)
		budget.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		acquired, err := budget.tryAcquire(weight)
		if err == nil {
			return acquired, nil
		}
		if !errors.Is(err, errQueueFull) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-budget.wake:
		}
	}
}

func (budget *byteBudget) replace(existing *permit, newWeight int64) (*permit, error) {
	if budget == nil || existing == nil || existing.budget != budget || newWeight <= 0 {
		return nil, errInvalidWeight
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if newWeight > budget.capacity {
		return nil, errInvalidWeight
	}
	if existing.released.Load() {
		return nil, errPermitReleased
	}
	extra := newWeight - existing.weight
	if extra > 0 && budget.used > budget.capacity-extra {
		return nil, errQueueFull
	}
	if !existing.released.CompareAndSwap(false, true) {
		return nil, errPermitReleased
	}
	budget.used += extra
	replacement := &permit{budget: budget, weight: newWeight}
	if extra < 0 {
		signal(budget.wake)
	}
	return replacement, nil
}

func (budget *byteBudget) usedBytes() int64 {
	if budget == nil {
		return 0
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.used
}

func (budget *byteBudget) capacityBytes() int64 {
	if budget == nil {
		return 0
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.capacity
}

func (owned *permit) Release() error {
	if owned == nil || owned.budget == nil {
		return errPermitReleased
	}
	if !owned.released.CompareAndSwap(false, true) {
		return errPermitReleased
	}
	budget := owned.budget
	budget.mu.Lock()
	if owned.weight <= 0 || budget.used < owned.weight {
		budget.mu.Unlock()
		return errPermitReleased
	}
	budget.used -= owned.weight
	budget.mu.Unlock()
	signal(budget.wake)
	return nil
}

func signal(wake chan struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
