package model

import "time"

const (
	// MaxLiveEventsPerCommit matches the bounded store write batch.
	MaxLiveEventsPerCommit = MaxWriteBatchMessages
	// MaxNormalizedLiveEventBytes is the conservative charge for one event.
	MaxNormalizedLiveEventBytes = MaxNormalizedEventBytes

	liveEventEnvelopeBytes = 128
)

// LiveEventKind is a closed classification for lossless live-data events.
type LiveEventKind uint8

const (
	// LiveMessageCommitted identifies one newly inserted realtime message after
	// its store transaction has committed.
	LiveMessageCommitted LiveEventKind = iota + 1
)

// LiveEvent is an immutable, bounded post-commit presentation event.
type LiveEvent struct {
	kind         LiveEventKind
	message      Message
	unreadCount  uint32
	activityTime time.Time
	byteSize     int
}

// NewLiveMessageCommitted validates and owns one newly committed realtime
// message with authoritative chat metadata observed for that logical insert.
func NewLiveMessageCommitted(message Message, unreadCount uint32, activityTime time.Time) (LiveEvent, error) {
	owned, messageBytes, err := cloneNormalizedMessage(message)
	if err != nil {
		return LiveEvent{}, err
	}
	if activityTime.IsZero() {
		return LiveEvent{}, newValidationError(Required, "activity_time", 0)
	}
	if activityTime.Before(owned.SentAt()) {
		return LiveEvent{}, newValidationError(InvalidValue, "activity_time", 0)
	}
	byteSize, ok := checkedByteSum(liveEventEnvelopeBytes, messageBytes)
	if !ok || byteSize > MaxNormalizedLiveEventBytes {
		return LiveEvent{}, newValidationError(SizeExceeded, "live_event", MaxNormalizedLiveEventBytes)
	}
	return LiveEvent{
		kind:         LiveMessageCommitted,
		message:      owned,
		unreadCount:  unreadCount,
		activityTime: activityTime,
		byteSize:     byteSize,
	}, nil
}

// Kind returns the event classification.
func (event LiveEvent) Kind() LiveEventKind { return event.kind }

// Message returns the immutable committed message.
func (event LiveEvent) Message() Message { return event.message }

// UnreadCount returns authoritative unread state after the logical insert.
func (event LiveEvent) UnreadCount() uint32 { return event.unreadCount }

// ActivityTime returns authoritative last-message activity after the insert.
func (event LiveEvent) ActivityTime() time.Time { return event.activityTime }

// ByteSize returns the conservative bounded event charge.
func (event LiveEvent) ByteSize() int { return event.byteSize }

// LiveEventBatch is a fixed-capacity immutable result from one realtime store
// commit. It contains only newly inserted messages in request order.
type LiveEventBatch struct {
	events [MaxLiveEventsPerCommit]LiveEvent
	count  int
}

// NewLiveEventBatch validates and copies a bounded committed-event batch.
func NewLiveEventBatch(events []LiveEvent) (LiveEventBatch, error) {
	if len(events) > MaxLiveEventsPerCommit {
		return LiveEventBatch{}, newValidationError(SizeExceeded, "live_event_batch", MaxLiveEventsPerCommit)
	}
	var batch LiveEventBatch
	for index, event := range events {
		if event.Kind() != LiveMessageCommitted {
			return LiveEventBatch{}, newValidationError(InvalidValue, "live_event_kind", 0)
		}
		owned, err := NewLiveMessageCommitted(event.Message(), event.UnreadCount(), event.ActivityTime())
		if err != nil {
			return LiveEventBatch{}, err
		}
		batch.events[index] = owned
	}
	batch.count = len(events)
	return batch, nil
}

// Len returns the event count.
func (batch LiveEventBatch) Len() int { return batch.count }

// At returns one committed event and whether index was in bounds.
func (batch LiveEventBatch) At(index int) (LiveEvent, bool) {
	if index < 0 || index >= batch.count {
		return LiveEvent{}, false
	}
	return batch.events[index], true
}
