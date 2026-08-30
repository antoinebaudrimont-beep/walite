package service

import (
	"context"
	"errors"
	"sync"
)

type weighFunc[T any] func(T) (int64, error)

type envelope[T any] struct {
	value  T
	permit *permit
}

type boundedQueue[T any] struct {
	mu             sync.Mutex
	storage        []envelope[T]
	head           int
	tail           int
	count          int
	reservations   int
	stopped        bool
	entryWaiters   int
	byteWaiters    int
	putWaiter      bool
	budget         *byteBudget
	weigh          weighFunc[T]
	spaceWake      chan struct{}
	notEmptyWake   chan struct{}
	stateWake      chan struct{}
	stoppedCh      chan struct{}
	beforeTakeWait func()
}

type queueStats struct {
	Entries      int
	Reservations int
	UsedBytes    int64
	EntryWaiters int
	ByteWaiters  int
	Stopped      bool
}

type lease[T any] struct {
	value  T
	permit *permit
}

// queueReservation owns one fixed queue entry and a byte-budget permit before
// the eventual value exists. It is internal to the one bounded queue; callers
// must either commit one value or release it exactly once.
type queueReservation[T any] struct {
	queue  *boundedQueue[T]
	permit *permit
	active bool
}

func newBoundedQueue[T any](entries int, budget *byteBudget, weigh weighFunc[T]) (*boundedQueue[T], error) {
	if entries <= 0 || budget == nil || weigh == nil {
		return nil, errInvalidWeight
	}
	return &boundedQueue[T]{
		storage:      make([]envelope[T], entries),
		budget:       budget,
		weigh:        weigh,
		spaceWake:    make(chan struct{}, 1),
		notEmptyWake: make(chan struct{}, 1),
		stateWake:    make(chan struct{}, 1),
		stoppedCh:    make(chan struct{}),
	}, nil
}

func (queue *boundedQueue[T]) TryPut(value T) error {
	queue.mu.Lock()
	if queue.stopped {
		queue.mu.Unlock()
		return errQueueStopped
	}
	queue.mu.Unlock()
	owned, err := queue.tryReserve(value)
	if err != nil {
		return err
	}
	if err := queue.putReserved(value, owned); err != nil {
		_ = owned.Release()
		return err
	}
	return nil
}

func (queue *boundedQueue[T]) tryReserve(value T) (*permit, error) {
	if queue == nil || queue.budget == nil || queue.weigh == nil {
		return nil, errInvalidWeight
	}
	weight, err := queue.weigh(value)
	if err != nil || weight <= 0 {
		return nil, errInvalidWeight
	}
	return queue.budget.tryAcquire(weight)
}

func (queue *boundedQueue[T]) tryReserveCapacity(weight int64) (*queueReservation[T], error) {
	if queue == nil || queue.budget == nil || weight <= 0 {
		return nil, errInvalidWeight
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.stopped {
		return nil, errQueueStopped
	}
	if queue.count+queue.reservations == len(queue.storage) {
		return nil, errQueueFull
	}
	owned, err := queue.budget.tryAcquire(weight)
	if err != nil {
		return nil, err
	}
	queue.reservations++
	signal(queue.stateWake)
	return &queueReservation[T]{queue: queue, permit: owned, active: true}, nil
}

func (queue *boundedQueue[T]) putReserved(value T, owned *permit) error {
	if queue == nil || owned == nil || owned.budget != queue.budget || owned.released.Load() {
		return errInvalidWeight
	}
	weight, err := queue.weigh(value)
	if err != nil || weight <= 0 || weight != owned.weight {
		return errInvalidWeight
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.stopped {
		return errQueueStopped
	}
	if queue.count+queue.reservations == len(queue.storage) {
		return errQueueFull
	}
	wasEmpty := queue.count == 0
	queue.storage[queue.tail] = envelope[T]{value: value, permit: owned}
	queue.tail = (queue.tail + 1) % len(queue.storage)
	queue.count++
	if wasEmpty {
		signal(queue.notEmptyWake)
	}
	signal(queue.stateWake)
	return nil
}

func (queue *boundedQueue[T]) Put(ctx context.Context, value T) error {
	if ctx == nil {
		return errQueueStopped
	}
	weight, err := queue.weigh(value)
	if err != nil || weight <= 0 || weight > queue.budget.capacityBytes() {
		return errInvalidWeight
	}
	var owned *permit
	registered := false
	waitingForEntry := false
	defer func() {
		if registered {
			queue.clearPutWaiter(waitingForEntry)
		}
	}()
	for owned == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		queue.mu.Lock()
		if queue.stopped {
			queue.mu.Unlock()
			return errQueueStopped
		}
		queue.mu.Unlock()
		owned, err = queue.budget.tryAcquire(weight)
		if err == nil {
			break
		}
		if !errors.Is(err, errQueueFull) {
			return err
		}
		if !registered {
			if !queue.registerPutWaiter(false) {
				return errQueueInvariant
			}
			registered = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-queue.budget.wake:
		case <-queue.stoppedCh:
			return errQueueStopped
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = owned.Release()
			return err
		}
		err = queue.putReserved(value, owned)
		if err == nil {
			return nil
		}
		if errors.Is(err, errQueueStopped) {
			_ = owned.Release()
			return err
		}
		if !errors.Is(err, errQueueFull) {
			_ = owned.Release()
			return err
		}
		if !registered {
			if !queue.registerPutWaiter(true) {
				_ = owned.Release()
				return errQueueInvariant
			}
			registered = true
			waitingForEntry = true
		} else if !waitingForEntry {
			queue.switchPutWaiterToEntry()
			waitingForEntry = true
		}
		select {
		case <-ctx.Done():
			_ = owned.Release()
			return ctx.Err()
		case <-queue.spaceWake:
		case <-queue.stoppedCh:
			_ = owned.Release()
			return errQueueStopped
		}
	}
}

func (queue *boundedQueue[T]) Take(ctx context.Context) (lease[T], error) {
	if ctx == nil {
		return lease[T]{}, errQueueStopped
	}
	for {
		if err := ctx.Err(); err != nil {
			return lease[T]{}, err
		}
		if value, ok := queue.TryTake(); ok {
			return value, nil
		}
		queue.mu.Lock()
		stopped := queue.stopped && queue.count == 0
		queue.mu.Unlock()
		if stopped {
			return lease[T]{}, errQueueStopped
		}
		if queue.beforeTakeWait != nil {
			queue.beforeTakeWait()
		}
		select {
		case <-ctx.Done():
			return lease[T]{}, ctx.Err()
		case <-queue.notEmptyWake:
		case <-queue.stoppedCh:
		}
	}
}

func (queue *boundedQueue[T]) TryTake() (lease[T], bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.count == 0 {
		return lease[T]{}, false
	}
	item := queue.storage[queue.head]
	var zero envelope[T]
	queue.storage[queue.head] = zero
	queue.head = (queue.head + 1) % len(queue.storage)
	queue.count--
	signal(queue.spaceWake)
	signal(queue.stateWake)
	return lease[T]{value: item.value, permit: item.permit}, true
}

func (queue *boundedQueue[T]) Close() {
	queue.mu.Lock()
	if !queue.stopped {
		queue.stopped = true
		close(queue.stoppedCh)
		signal(queue.spaceWake)
		signal(queue.notEmptyWake)
		signal(queue.stateWake)
	}
	queue.mu.Unlock()
}

func (queue *boundedQueue[T]) drainAndRelease() int {
	drained := 0
	for {
		owned, ok := queue.TryTake()
		if !ok {
			return drained
		}
		_ = owned.Release()
		drained++
	}
}

func (queue *boundedQueue[T]) Stats() queueStats {
	queue.mu.Lock()
	stats := queueStats{Entries: queue.count, Reservations: queue.reservations, EntryWaiters: queue.entryWaiters, ByteWaiters: queue.byteWaiters, Stopped: queue.stopped}
	queue.mu.Unlock()
	stats.UsedBytes = queue.budget.usedBytes()
	return stats
}

func (queue *boundedQueue[T]) spaceChanges() <-chan struct{}    { return queue.spaceWake }
func (queue *boundedQueue[T]) notEmptyChanges() <-chan struct{} { return queue.notEmptyWake }
func (queue *boundedQueue[T]) budgetChanges() <-chan struct{}   { return queue.budget.wake }
func (queue *boundedQueue[T]) stateChanges() <-chan struct{}    { return queue.stateWake }

func (owned lease[T]) Value() T { return owned.value }
func (owned *lease[T]) Release() error {
	if owned == nil || owned.permit == nil {
		return errPermitReleased
	}
	return owned.permit.Release()
}

func (reservation *queueReservation[T]) commit(value T) error {
	if reservation == nil || reservation.queue == nil || reservation.permit == nil || !reservation.active {
		return errPermitReleased
	}
	queue := reservation.queue
	weight, err := queue.weigh(value)
	if err != nil || weight <= 0 {
		return errInvalidWeight
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if !reservation.active || reservation.queue != queue || queue.reservations <= 0 || queue.count >= len(queue.storage) {
		return errQueueInvariant
	}
	replacement, err := queue.budget.replace(reservation.permit, weight)
	if err != nil {
		return err
	}
	wasEmpty := queue.count == 0
	queue.storage[queue.tail] = envelope[T]{value: value, permit: replacement}
	queue.tail = (queue.tail + 1) % len(queue.storage)
	queue.count++
	queue.reservations--
	reservation.permit = nil
	reservation.active = false
	if wasEmpty {
		signal(queue.notEmptyWake)
	}
	signal(queue.stateWake)
	return nil
}

func (reservation *queueReservation[T]) release() error {
	if reservation == nil || reservation.queue == nil || reservation.permit == nil || !reservation.active {
		return errPermitReleased
	}
	queue := reservation.queue
	queue.mu.Lock()
	if !reservation.active || reservation.queue != queue || queue.reservations <= 0 {
		queue.mu.Unlock()
		return errQueueInvariant
	}
	owned := reservation.permit
	queue.reservations--
	reservation.permit = nil
	reservation.active = false
	signal(queue.spaceWake)
	signal(queue.notEmptyWake)
	signal(queue.stateWake)
	queue.mu.Unlock()
	return owned.Release()
}

func (queue *boundedQueue[T]) registerPutWaiter(entry bool) bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.putWaiter {
		return false
	}
	queue.putWaiter = true
	if entry {
		queue.entryWaiters = 1
	} else {
		queue.byteWaiters = 1
	}
	signal(queue.stateWake)
	return true
}

func (queue *boundedQueue[T]) switchPutWaiterToEntry() {
	queue.mu.Lock()
	queue.byteWaiters = 0
	queue.entryWaiters = 1
	signal(queue.stateWake)
	queue.mu.Unlock()
}

func (queue *boundedQueue[T]) clearPutWaiter(entry bool) {
	queue.mu.Lock()
	queue.putWaiter = false
	if entry {
		queue.entryWaiters = 0
	} else {
		queue.byteWaiters = 0
	}
	signal(queue.stateWake)
	queue.mu.Unlock()
}
