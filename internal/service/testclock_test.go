package service

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"
)

type manualClock struct {
	mu            sync.Mutex
	now           time.Time
	timers        [8]*manualTimer
	registrations uint64
	wake          chan struct{}
}
type manualTimer struct {
	clock    *manualClock
	slot     int
	deadline time.Time
	ch       chan time.Time
	active   bool
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{now: now, wake: make(chan struct{}, 1)}
}
func (clock *manualClock) Now() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return clock.now }
func (clock *manualClock) NewTimer(duration time.Duration) Timer {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	slot := -1
	for i := range clock.timers {
		if clock.timers[i] == nil {
			slot = i
			break
		}
	}
	if slot < 0 || clock.registrations == math.MaxUint64 {
		panic("manual clock invariant")
	}
	timer := &manualTimer{clock: clock, slot: slot, deadline: clock.now.Add(duration), ch: make(chan time.Time, 1), active: true}
	clock.timers[slot] = timer
	clock.registrations++
	signal(clock.wake)
	return timer
}
func (timer *manualTimer) C() <-chan time.Time { return timer.ch }
func (timer *manualTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	if !timer.active {
		return false
	}
	timer.active = false
	timer.clock.timers[timer.slot] = nil
	return true
}
func (clock *manualClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	for i, timer := range clock.timers {
		if timer != nil && timer.active && !timer.deadline.After(clock.now) {
			timer.active = false
			clock.timers[i] = nil
			select {
			case timer.ch <- clock.now:
			default:
			}
		}
	}
	clock.mu.Unlock()
}
func (clock *manualClock) waitTimerRegistrations(ctx context.Context, count uint64) error {
	for {
		clock.mu.Lock()
		ready := clock.registrations >= count
		clock.mu.Unlock()
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-clock.wake:
		}
	}
}
func TestManualClockTimer(t *testing.T) {
	clock := newManualClock(time.Unix(1, 0))
	timer := clock.NewTimer(time.Second)
	clock.Advance(time.Second)
	select {
	case <-timer.C():
	default:
		t.Fatal("timer did not fire")
	}
	if timer.Stop() {
		t.Fatal("fired timer stopped as active")
	}
}
