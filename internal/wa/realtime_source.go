package wa

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const RealtimeSourceCapacity = 64

var errRealtimeHistoryUnavailable = errors.New("WhatsApp realtime source has no history")

// RealtimeSource is the single bounded bridge from the long-lived WhatsApp
// callback into service.Core. The callback applies backpressure when the ring
// is full; Run is the sole owner and closer of the service-facing channel.
type RealtimeSource struct {
	mu sync.Mutex

	events  [RealtimeSourceCapacity]model.Event
	head    int
	tail    int
	count   int
	waiters int
	closed  bool

	notEmpty chan struct{}
	space    chan struct{}
	state    chan struct{}
	stopped  chan struct{}
	realtime chan model.Event
	history  chan model.HistoryJob
	status   chan struct{}

	started   atomic.Bool
	closeOnce sync.Once
}

func newRealtimeSource() *RealtimeSource {
	history := make(chan model.HistoryJob)
	status := make(chan struct{})
	close(history)
	close(status)
	return &RealtimeSource{
		notEmpty: make(chan struct{}, 1),
		space:    make(chan struct{}, 1),
		state:    make(chan struct{}, 1),
		stopped:  make(chan struct{}),
		realtime: make(chan model.Event),
		history:  history,
		status:   status,
	}
}

func (source *RealtimeSource) admit(event model.Event) bool {
	if source == nil {
		return false
	}
	normalized, err := model.NewEvent(event.Message(), event.ReceivedAt())
	if err != nil {
		return false
	}
	waiting := false
	for {
		source.mu.Lock()
		if source.closed {
			if waiting {
				source.waiters--
				signalRealtimeSource(source.state)
			}
			source.mu.Unlock()
			return false
		}
		if source.count < len(source.events) {
			if waiting {
				source.waiters--
			}
			source.events[source.tail] = normalized
			source.tail = (source.tail + 1) % len(source.events)
			source.count++
			signalRealtimeSource(source.notEmpty)
			signalRealtimeSource(source.state)
			source.mu.Unlock()
			return true
		}
		if !waiting {
			waiting = true
			source.waiters++
			signalRealtimeSource(source.state)
		}
		source.mu.Unlock()
		select {
		case <-source.space:
		case <-source.stopped:
		}
	}
}

// Run delivers admitted messages in callback order until cancellation or
// connection shutdown. It is single-use.
func (source *RealtimeSource) Run(ctx context.Context) error {
	if source == nil || ctx == nil || !source.started.CompareAndSwap(false, true) {
		return errors.New("WhatsApp realtime source rejected")
	}
	defer close(source.realtime)
	defer source.closeAdmission()
	for {
		event, ok, err := source.take(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		select {
		case source.realtime <- event:
		case <-ctx.Done():
			return ctx.Err()
		case <-source.stopped:
			return nil
		}
	}
}

func (source *RealtimeSource) take(ctx context.Context) (model.Event, bool, error) {
	for {
		source.mu.Lock()
		if source.count > 0 {
			event := source.events[source.head]
			source.events[source.head] = model.Event{}
			source.head = (source.head + 1) % len(source.events)
			source.count--
			signalRealtimeSource(source.space)
			signalRealtimeSource(source.state)
			source.mu.Unlock()
			return event, true, nil
		}
		closed := source.closed
		source.mu.Unlock()
		if closed {
			return model.Event{}, false, nil
		}
		select {
		case <-ctx.Done():
			return model.Event{}, false, ctx.Err()
		case <-source.notEmpty:
		case <-source.stopped:
		}
	}
}

func (source *RealtimeSource) closeAdmission() {
	if source == nil {
		return
	}
	source.closeOnce.Do(func() {
		source.mu.Lock()
		source.closed = true
		close(source.stopped)
		signalRealtimeSource(source.notEmpty)
		signalRealtimeSource(source.space)
		signalRealtimeSource(source.state)
		source.mu.Unlock()
	})
}

func (source *RealtimeSource) RealtimeEvents() <-chan model.Event {
	if source == nil {
		return nil
	}
	return source.realtime
}

func (source *RealtimeSource) MetadataHistoryJobs() <-chan model.HistoryJob {
	if source == nil {
		return nil
	}
	return source.history
}

func (source *RealtimeSource) OnDemandHistoryJobs() <-chan model.HistoryJob {
	if source == nil {
		return nil
	}
	return source.history
}

func (source *RealtimeSource) BulkHistoryJobs() <-chan model.HistoryJob {
	if source == nil {
		return nil
	}
	return source.history
}

func (source *RealtimeSource) StatusWake() <-chan struct{} {
	if source == nil {
		return nil
	}
	return source.status
}

func (*RealtimeSource) Status() model.SourceStatus {
	return model.NewSourceStatus(model.SourceStatusInput{})
}

func (*RealtimeSource) NextHistory(context.Context, model.HistoryJob, int, int64) (model.HistoryChunk, bool, error) {
	return model.HistoryChunk{}, true, errRealtimeHistoryUnavailable
}

func (*RealtimeSource) ReleaseHistory(model.HistoryJob) {}

func signalRealtimeSource(channel chan<- struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
