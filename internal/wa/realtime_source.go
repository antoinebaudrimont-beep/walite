package wa

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const RealtimeSourceCapacity = 64

const BootstrapRecordCapacity = 1

type realtimeEntry struct {
	event      model.Event
	reaction   model.Reaction
	isReaction bool
	alternate  model.ChatID
}

var errRealtimeHistoryUnavailable = errors.New("WhatsApp realtime source has no history")

// RealtimeSource is the single bounded bridge from the long-lived WhatsApp
// callback into service.Core. The callback applies backpressure when the ring
// is full; Run is the sole owner and closer of the service-facing channel.
type RealtimeSource struct {
	mu sync.Mutex

	events  [RealtimeSourceCapacity]realtimeEntry
	head    int
	tail    int
	count   int
	waiters int
	closed  bool

	notEmpty  chan struct{}
	space     chan struct{}
	state     chan struct{}
	stopped   chan struct{}
	realtime  chan model.Event
	reactions chan model.Reaction
	bootstrap chan model.BootstrapRecord
	history   chan model.HistoryJob
	status    chan struct{}

	started   atomic.Bool
	closeOnce sync.Once
	aliases   *chatAliases
	lookup    alternateJIDLookup
	display   *displayResolver
}

func newRealtimeSource() *RealtimeSource {
	history := make(chan model.HistoryJob)
	status := make(chan struct{})
	close(history)
	close(status)
	return &RealtimeSource{
		aliases:   &chatAliases{},
		notEmpty:  make(chan struct{}, 1),
		space:     make(chan struct{}, 1),
		state:     make(chan struct{}, 1),
		stopped:   make(chan struct{}),
		realtime:  make(chan model.Event),
		reactions: make(chan model.Reaction),
		bootstrap: make(chan model.BootstrapRecord, BootstrapRecordCapacity),
		history:   history,
		status:    status,
	}
}

func (source *RealtimeSource) admitReaction(reaction model.Reaction, alternate model.ChatID) bool {
	if source == nil {
		return false
	}
	owned, err := model.NewReaction(model.ReactionInput{
		ChatID: reaction.ChatID().String(), TargetMessageID: reaction.TargetMessageID().String(),
		ReactorID: reaction.ReactorID().String(), Emoji: reaction.Emoji(), UpdatedAt: reaction.UpdatedAt(),
	})
	if err != nil {
		return false
	}
	return source.admitEntry(realtimeEntry{reaction: owned, isReaction: true, alternate: alternate})
}

func (source *RealtimeSource) admit(event model.Event) bool {
	return source.admitWithAlternate(event, model.ChatID{})
}

func (source *RealtimeSource) admitWithAlternate(event model.Event, alternate model.ChatID) bool {
	if source == nil {
		return false
	}
	normalized, err := model.NewEvent(event.Message(), event.ReceivedAt())
	if err != nil {
		return false
	}
	return source.admitEntry(realtimeEntry{event: normalized, alternate: alternate})
}

func (source *RealtimeSource) admitEntry(entry realtimeEntry) bool {
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
			source.events[source.tail] = entry
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
	defer close(source.reactions)
	defer source.closeAdmission()
	if source.display != nil {
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); source.display.run(workerCtx) }()
		defer func() { cancel(); <-done }()
	}
	for {
		entry, ok, err := source.take(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if entry.isReaction {
			reaction, err := source.resolveReactionEntry(ctx, entry)
			if err != nil {
				return err
			}
			select {
			case source.reactions <- reaction:
			case <-ctx.Done():
				return ctx.Err()
			case <-source.stopped:
				return nil
			}
		} else {
			event, err := source.resolveEntry(ctx, entry)
			if err != nil {
				return err
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
}

// BootstrapRecords is the bounded, backpressured history-bootstrap stream.
// Historical records are deliberately separate from lossless live events.
func (source *RealtimeSource) BootstrapRecords() <-chan model.BootstrapRecord {
	if source == nil {
		return nil
	}
	return source.bootstrap
}

// SeedChatIDs establishes identities already persisted by the application
// cache before the connection starts. A later authoritative PN/LID alternate
// is attached to that entry, so reconnect traffic cannot create a second row
// merely because the transport presents the opposite identifier first.
func (source *RealtimeSource) SeedChatIDs(ids []model.ChatID) error {
	if source == nil || len(ids) > model.MaxChatSummaries {
		return errors.New("WhatsApp chat identity seed rejected")
	}
	for _, id := range ids {
		if id.String() == "" {
			return errors.New("WhatsApp chat identity seed rejected")
		}
		if _, err := source.aliases.resolve(id, model.ChatID{}); err != nil {
			return err
		}
	}
	if source.display != nil {
		source.display.requestPeople(ids)
	}
	return nil
}

// RequestDisplayMetadata schedules local contact-store resolution for a
// bounded presentation page. It does not perform network I/O or change IDs.
func (source *RealtimeSource) RequestDisplayMetadata(ids []model.ChatID) {
	if source == nil || source.display == nil || len(ids) > model.MaxBootstrapMessagesPerChat+1 {
		return
	}
	source.display.requestPeople(ids)
}

func (source *RealtimeSource) admitBootstrap(record model.BootstrapRecord) bool {
	if source == nil {
		return false
	}
	select {
	case source.bootstrap <- record:
		return true
	case <-source.stopped:
		return false
	}
}

func (source *RealtimeSource) take(ctx context.Context) (realtimeEntry, bool, error) {
	for {
		source.mu.Lock()
		if source.count > 0 {
			event := source.events[source.head]
			source.events[source.head] = realtimeEntry{}
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
			return realtimeEntry{}, false, nil
		}
		select {
		case <-ctx.Done():
			return realtimeEntry{}, false, ctx.Err()
		case <-source.notEmpty:
		case <-source.stopped:
		}
	}
}

func (source *RealtimeSource) resolveEntry(ctx context.Context, entry realtimeEntry) (model.Event, error) {
	message := entry.event.Message()
	alternate, err := lookupAlternate(ctx, message.ChatID(), entry.alternate, source.lookup)
	if err != nil {
		return model.Event{}, err
	}
	id, err := source.aliases.resolve(message.ChatID(), alternate)
	if err != nil {
		return model.Event{}, err
	}
	if id == message.ChatID() {
		return entry.event, nil
	}
	message, err = message.WithChatID(id)
	if err != nil {
		return model.Event{}, err
	}
	return model.NewEvent(message, entry.event.ReceivedAt())
}

func (source *RealtimeSource) resolveReactionEntry(ctx context.Context, entry realtimeEntry) (model.Reaction, error) {
	reaction := entry.reaction
	alternate, err := lookupAlternate(ctx, reaction.ChatID(), entry.alternate, source.lookup)
	if err != nil {
		return model.Reaction{}, err
	}
	original := reaction.ChatID()
	id, err := source.aliases.resolve(original, alternate)
	if err != nil {
		return model.Reaction{}, err
	}
	if id != original {
		reaction, err = reaction.WithChatID(id)
		if err != nil {
			return model.Reaction{}, err
		}
	}
	if reaction.ReactorID().String() == original.String() || reaction.ReactorID().String() == alternate.String() {
		reactor, err := model.NewContactID(id.String())
		if err != nil {
			return model.Reaction{}, err
		}
		return reaction.WithReactorID(reactor)
	}
	return reaction, nil
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

// ReactionEvents is the lossless bounded mutation stream sharing callback
// ordering and backpressure with RealtimeEvents.
func (source *RealtimeSource) ReactionEvents() <-chan model.Reaction {
	if source == nil {
		return nil
	}
	return source.reactions
}

// DisplayUpdates is advisory and independently coalesced; it never masquerades
// as a committed message or consumes the lossless LiveEvents capacity.
func (source *RealtimeSource) DisplayUpdates() <-chan model.DisplayMetadata {
	if source == nil || source.display == nil {
		return nil
	}
	return source.display.updates
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
