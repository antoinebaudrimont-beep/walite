package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// LiveEventCapacity is the fixed lossless delivery window. A slow consumer
// applies backpressure to the single writer after commit; storage transactions
// are never held open while waiting for this channel.
const LiveEventCapacity = 64

type QueueOptions struct {
	Entries int
	Bytes   int64
}
type Options struct {
	Realtime, History                       QueueOptions
	LiveWrites, HistoryWrites               QueueOptions
	ViewUpdates                             QueueOptions
	HistoryChunkRecords                     int
	HistoryChunkBytes                       int64
	BatchMaxOperations                      int
	RetentionSnapshotLimit                  int
	BatchWait, LiveWriteBusy, ShutdownGrace time.Duration
}

type CoreErrorKind uint8

const (
	CoreBusy CoreErrorKind = iota + 1
	CoreCancelled
	CoreClosed
	CoreMalformed
	CoreDegraded
	CoreInvariant
)

type CoreOperation uint8

const (
	CoreOperationNew CoreOperation = iota + 1
	CoreOperationRun
	CoreOperationStartup
	CoreOperationShutdown
	CoreOperationPublish
	CoreOperationSend
)

type CoreError struct {
	Kind      CoreErrorKind
	Operation CoreOperation
	Cause     error
}

func (err *CoreError) Error() string {
	if err == nil {
		return "service failed"
	}
	switch err.Kind {
	case CoreBusy:
		return "service busy"
	case CoreCancelled:
		return "service cancelled"
	case CoreClosed:
		return "service closed"
	case CoreMalformed:
		return "service malformed"
	case CoreDegraded:
		return "service degraded"
	default:
		return "service invariant violation"
	}
}
func (err *CoreError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}
func (err *CoreError) Is(target error) bool {
	other, ok := target.(*CoreError)
	return ok && err.Kind == other.Kind
}

type systemClock struct{}
type systemTimer struct{ timer *time.Timer }

func NewSystemClock() Clock        { return systemClock{} }
func (systemClock) Now() time.Time { return time.Now() }
func (systemClock) NewTimer(duration time.Duration) Timer {
	return &systemTimer{timer: time.NewTimer(duration)}
}
func (timer *systemTimer) C() <-chan time.Time { return timer.timer.C }
func (timer *systemTimer) Stop() bool          { return timer.timer.Stop() }

type Core struct {
	options                   Options
	source                    EventSource
	store                     MessageStore
	policy                    RetentionPolicy
	clock                     Clock
	realtimeQ                 *boundedQueue[model.Event]
	historyQ                  *boundedQueue[model.HistoryChunk]
	liveWriteQ, historyWriteQ *boundedQueue[model.Message]
	resultQ                   *boundedQueue[writeResult]
	updateMailbox             *fixedMailbox[viewSlot, model.Update]
	updates                   chan model.Update
	liveEvents                chan model.LiveEvent
	sender                    TextSender
	mediaSender               MediaSender
	sendSlot                  chan struct{}
	localDrops                *saturatingCounter
	localDropWake             chan struct{}
	publisherPendingHook      func()
	publisherBeforeCommitHook func()
	publisherCommittedHook    func()
	startupAbortedHook        func()
	run                       atomic.Bool
	acceptingSends            atomic.Bool
}

type startupState uint32

const (
	startupWaiting startupState = iota
	startupCommitted
	startupAborted
)

type viewSlot uint8

func updateSlot(kind model.UpdateKind) (viewSlot, bool) {
	if kind < model.UpdateReady || kind > model.UpdateFatal {
		return 0, false
	}
	return viewSlot(kind - 1), true
}

func New(options Options, source EventSource, store MessageStore, policy RetentionPolicy, clock Clock) (*Core, error) {
	return newCore(options, source, store, policy, clock, nil)
}

// NewWithTextSender constructs Core with the narrow offline outgoing-text
// capability. New remains available for receive-only compositions.
func NewWithTextSender(options Options, source EventSource, store MessageStore, policy RetentionPolicy, clock Clock, sender TextSender) (*Core, error) {
	if sender == nil {
		return nil, &CoreError{Kind: CoreMalformed, Operation: CoreOperationNew}
	}
	return newCore(options, source, store, policy, clock, sender)
}

func newCore(options Options, source EventSource, store MessageStore, policy RetentionPolicy, clock Clock, sender TextSender) (*Core, error) {
	if err := validateOptions(options, source, store, policy, clock); err != nil {
		return nil, err
	}
	realtimeBudget, _ := newByteBudget(options.Realtime.Bytes)
	historyBudget, _ := newByteBudget(options.History.Bytes)
	liveBudget, _ := newByteBudget(options.LiveWrites.Bytes)
	historyWriteBudget, _ := newByteBudget(options.HistoryWrites.Bytes)
	realtimeQ, _ := newBoundedQueue(options.Realtime.Entries, realtimeBudget, weighEvent)
	historyQ, _ := newBoundedQueue(options.History.Entries, historyBudget, weighChunk)
	liveQ, _ := newBoundedQueue(options.LiveWrites.Entries, liveBudget, weighMessage)
	historyWriteQ, _ := newBoundedQueue(options.HistoryWrites.Entries, historyWriteBudget, weighMessage)
	resultBudget, _ := newByteBudget(int64(8) * writeResultWeight)
	resultQ, _ := newBoundedQueue(8, resultBudget, weighWriteResult)
	updateBudget, _ := newByteBudget(options.ViewUpdates.Bytes)
	keys := make([]viewSlot, 64)
	for index := range keys {
		keys[index] = viewSlot(index)
	}
	mailbox, _ := newFixedMailbox(keys, updateBudget, weighUpdate)
	drops := &saturatingCounter{}
	mediaSender, _ := sender.(MediaSender)
	return &Core{options: options, source: source, store: store, policy: policy, clock: clock, realtimeQ: realtimeQ, historyQ: historyQ, liveWriteQ: liveQ, historyWriteQ: historyWriteQ, resultQ: resultQ, updateMailbox: mailbox, updates: make(chan model.Update, 1), liveEvents: make(chan model.LiveEvent, LiveEventCapacity), sender: sender, mediaSender: mediaSender, sendSlot: make(chan struct{}, 1), localDrops: drops, localDropWake: make(chan struct{}, 1)}, nil
}

func validateOptions(o Options, source EventSource, store MessageStore, policy RetentionPolicy, clock Clock) error {
	malformed := func() error { return &CoreError{Kind: CoreMalformed, Operation: CoreOperationNew} }
	if source == nil || store == nil || policy == nil || clock == nil {
		return malformed()
	}
	queues := []QueueOptions{o.Realtime, o.History, o.LiveWrites, o.HistoryWrites, o.ViewUpdates}
	for _, q := range queues {
		if q.Entries <= 0 || q.Bytes <= 0 {
			return malformed()
		}
	}
	if o.Realtime.Entries > 128 || o.History.Entries > 4 || o.LiveWrites.Entries > 128 || o.HistoryWrites.Entries > 256 || o.ViewUpdates.Entries != 64 || o.HistoryChunkRecords <= 0 || o.HistoryChunkRecords > model.MaxHistoryChunkRecords || o.HistoryChunkBytes <= 0 || o.HistoryChunkBytes > model.MaxHistoryChunkBytes || o.HistoryChunkBytes > o.History.Bytes || o.BatchMaxOperations <= 0 || o.BatchMaxOperations > model.MaxWriteBatchMessages || o.BatchMaxOperations > o.LiveWrites.Entries || o.BatchMaxOperations > o.HistoryWrites.Entries || o.RetentionSnapshotLimit <= 0 || o.RetentionSnapshotLimit > model.MaxRetentionSnapshotSummaries || o.BatchWait <= 0 || o.LiveWriteBusy <= 0 || o.ShutdownGrace <= 0 {
		return malformed()
	}
	// A writer queue must fit a maximum body, bounded quote and sender identity.
	const maxMessageBytes = int64(4*model.MaxIdentifierBytes + model.MaxRetainedTextBytes + model.MaxQuoteTextBytes)
	if o.Realtime.Bytes < model.MaxNormalizedEventBytes || o.History.Bytes < model.MaxHistoryChunkBytes || o.LiveWrites.Bytes < maxMessageBytes || o.HistoryWrites.Bytes < maxMessageBytes || o.ViewUpdates.Bytes < model.MaxNormalizedUpdateBytes {
		return malformed()
	}
	return nil
}

func (core *Core) Updates() <-chan model.Update { return core.updates }

// LiveEvents returns newly inserted realtime messages in successful commit
// order. The service owns and closes the bounded channel.
func (core *Core) LiveEvents() <-chan model.LiveEvent { return core.liveEvents }

func (core *Core) Run(parent context.Context) error {
	if parent == nil {
		return &CoreError{Kind: CoreMalformed, Operation: CoreOperationRun}
	}
	if !core.run.CompareAndSwap(false, true) {
		return &CoreError{Kind: CoreClosed, Operation: CoreOperationRun}
	}
	defer core.acceptingSends.Store(false)
	base := context.WithoutCancel(parent)
	sourceCtx, sourceCancel := context.WithCancelCause(base)
	ingressCtx, ingressCancel := context.WithCancelCause(base)
	historyCtx, historyCancel := context.WithCancelCause(base)
	historyStoreCtx, historyStoreCancel := context.WithCancelCause(base)
	coordinatorCtx, coordinatorCancel := context.WithCancelCause(base)
	writerCtx, writerCancel := context.WithCancelCause(base)
	publisherCtx, publisherCancel := context.WithCancelCause(base)
	defer func() {
		sourceCancel(nil)
		ingressCancel(nil)
		historyCancel(nil)
		historyStoreCancel(nil)
		coordinatorCancel(nil)
		writerCancel(nil)
		publisherCancel(nil)
	}()

	readyPublished := make(chan struct{})
	var startup atomic.Uint32
	startup.Store(uint32(startupWaiting))
	var pubWG sync.WaitGroup
	pubWG.Add(1)
	go func() { defer pubWG.Done(); core.runPublisher(publisherCtx, readyPublished, &startup) }()
	transformer, _ := newHistoryTransformer(core.source, core.historyQ, core.options.HistoryChunkRecords, core.options.HistoryChunkBytes)
	ingester, _ := newHistoryIngester(core.store, core.policy, core.clock, core.historyQ, core.historyWriteQ)
	writerShutdown := make(chan struct{})
	writer, _ := newLiveFirstWriter(core.store, core.liveWriteQ, core.historyWriteQ, core.resultQ, core.clock, core.options.BatchWait, core.options.BatchMaxOperations, writerRetention{policy: core.policy, snapshotLimit: core.options.RetentionSnapshotLimit, historyStoreCtx: historyStoreCtx, shutdown: writerShutdown, finalLiveLimit: core.options.BatchMaxOperations, liveEvents: core.liveEvents})
	coordinatorShutdown := make(chan struct{})
	coordinator := &realtimeCoordinator{source: core.source, store: core.store, policy: core.policy, clock: core.clock, realtimeQ: core.realtimeQ, liveWriteQ: core.liveWriteQ, resultQ: core.resultQ, updates: core.updateMailbox, liveWriteBusy: core.options.LiveWriteBusy, localDrops: core.localDrops, localDropWake: core.localDropWake, shutdown: coordinatorShutdown, finalLimit: core.options.BatchMaxOperations}
	recorder := &firstCause{channel: make(chan error, 1)}
	startGate := make(chan struct{})
	var gateOnce sync.Once
	var wg sync.WaitGroup
	var started [6]chan struct{}
	var finished [6]chan struct{}
	for index := range started {
		started[index] = make(chan struct{}, 1)
		finished[index] = make(chan struct{})
	}
	contexts := []context.Context{sourceCtx, ingressCtx, historyCtx, historyStoreCtx, writerCtx, coordinatorCtx}
	runs := []func(context.Context) error{core.source.Run, core.runRealtimeIngress, transformer.Run, ingester.Run, writer.Run, coordinator.Run}
	cancelFatal := func(err error) {
		sourceCancel(err)
		ingressCancel(err)
		historyCancel(err)
		historyStoreCancel(err)
		coordinatorCancel(err)
		writerCancel(err)
	}
	start := func(index int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(finished[index])
			defer func() {
				if recover() != nil {
					failure := &CoreError{Kind: CoreInvariant, Operation: CoreOperationRun}
					if recorder.record(failure) {
						cancelFatal(failure)
					}
				}
			}()
			started[index] <- struct{}{}
			<-startGate
			if err := runs[index](contexts[index]); err != nil && !errors.Is(err, context.Canceled) {
				if recorder.record(err) {
					cancelFatal(err)
				}
			}
		}()
	}
	for index := range runs {
		start(index)
	}
	for index := range started {
		<-started[index]
	}
	abortStartup := func(cause error) {
		if core.startupAbortedHook != nil {
			core.startupAbortedHook()
		}
		sourceCancel(cause)
		ingressCancel(cause)
		historyCancel(cause)
		historyStoreCancel(cause)
		coordinatorCancel(cause)
		writerCancel(cause)
		gateOnce.Do(func() { close(startGate) })
	}
	if cause := parent.Err(); cause != nil && startup.CompareAndSwap(uint32(startupWaiting), uint32(startupAborted)) {
		abortStartup(cause)
	} else {
		core.publishSimple(model.UpdateReady)
		select {
		case <-readyPublished:
			gateOnce.Do(func() { close(startGate) })
		case <-parent.Done():
			if startup.CompareAndSwap(uint32(startupWaiting), uint32(startupAborted)) {
				abortStartup(parent.Err())
			} else {
				<-readyPublished
				gateOnce.Do(func() { close(startGate) })
			}
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	var cause error
	publisherClosed := false
	fatalShutdown := false
	select {
	case <-done:
		core.acceptingSends.Store(false)
		if err := recorder.load(); err != nil {
			cause = err
			fatalShutdown = true
			core.publishSimple(model.UpdateFatal)
		} else if err := parent.Err(); err != nil {
			cause = err
			core.publishSimple(model.UpdateStopping)
		}
	case <-parent.Done():
		core.acceptingSends.Store(false)
		if err := recorder.load(); err != nil {
			cause = err
			fatalShutdown = true
			core.publishSimple(model.UpdateFatal)
			cancelFatal(err)
			<-done
			break
		}
		cause = parent.Err()
		core.publishSimple(model.UpdateStopping)
		grace := core.clock.NewTimer(core.options.ShutdownGrace)
		graceWake := timerChannel(grace)
		expired := false
		waitStage := func(stage <-chan struct{}) bool {
			select {
			case <-stage:
				return true
			case <-graceWake:
				expired = true
				graceWake = nil
				return false
			}
		}
		sourceCancel(cause)
		if waitStage(finished[0]) && waitStage(finished[1]) {
			historyCancel(cause)
			historyStoreCancel(cause)
			if waitStage(finished[2]) && waitStage(finished[3]) {
				close(coordinatorShutdown)
				close(writerShutdown)
				waitStage(done)
			}
		}
		if expired {
			sourceCancel(cause)
			ingressCancel(cause)
			historyCancel(cause)
			historyStoreCancel(cause)
			coordinatorCancel(cause)
			writerCancel(cause)
			<-done
			core.updateMailbox.Close()
			publisherCancel(cause)
			pubWG.Wait()
			publisherClosed = true
		} else if grace != nil {
			grace.Stop()
		}
	case err := <-recorder.channel:
		core.acceptingSends.Store(false)
		cause = err
		fatalShutdown = true
		core.publishSimple(model.UpdateFatal)
		cancelFatal(err)
		<-done
	}
	if cause == nil {
		cause = recorder.load()
	}
	core.realtimeQ.drainAndRelease()
	core.historyQ.drainAndRelease()
	core.liveWriteQ.drainAndRelease()
	core.historyWriteQ.drainAndRelease()
	core.resultQ.drainAndRelease()
	if !publisherClosed {
		if !fatalShutdown {
			core.publishSimple(model.UpdateStopped)
		}
		core.updateMailbox.Close()
		publisherCancel(cause)
		pubWG.Wait()
	}
	return cause
}

type firstCause struct {
	once    sync.Once
	channel chan error
	value   atomic.Pointer[firstCauseValue]
}
type firstCauseValue struct{ err error }

func (first *firstCause) record(err error) bool {
	won := false
	first.once.Do(func() {
		won = true
		first.value.Store(&firstCauseValue{err})
		select {
		case first.channel <- err:
		default:
		}
	})
	return won
}
func (first *firstCause) load() error {
	value := first.value.Load()
	if value == nil {
		return nil
	}
	return value.err
}
func timerChannel(timer Timer) <-chan time.Time {
	if timer == nil {
		return nil
	}
	return timer.C()
}

func (core *Core) runRealtimeIngress(ctx context.Context) error {
	defer core.realtimeQ.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-core.source.RealtimeEvents():
			if !ok {
				return nil
			}
			normalized, err := model.NewEvent(event.Message(), event.ReceivedAt())
			if err != nil {
				return &CoreError{Kind: CoreMalformed}
			}
			if err = core.realtimeQ.Put(ctx, normalized); err != nil {
				if errors.Is(err, context.Canceled) {
					return ctx.Err()
				}
				if errors.Is(err, errQueueStopped) {
					return nil
				}
				return err
			}
		}
	}
}

func (core *Core) publishSimple(kind model.UpdateKind) {
	update, err := model.NewUpdate(model.UpdateInput{Kind: kind})
	if err == nil {
		if slot, ok := updateSlot(kind); ok {
			_ = core.updateMailbox.Replace(slot, update)
		}
	}
}
func (core *Core) runPublisher(ctx context.Context, readyPublished chan<- struct{}, startup *atomic.Uint32) {
	var readyOnce sync.Once
	var pending lease[model.Update]
	hasPending := false
	defer func() {
		if hasPending {
			select {
			case core.updates <- pending.Value():
			default:
			}
			_ = pending.Release()
		}
		core.updateMailbox.drainAndRelease()
		close(core.updates)
	}()
	for {
		if !hasPending {
			_, owned, _, ok := core.updateMailbox.TakeDirty()
			if ok {
				pending, hasPending = owned, true
				if core.publisherPendingHook != nil {
					core.publisherPendingHook()
				}
				continue
			}
			if mailboxStopped(core.updateMailbox) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-core.updateMailbox.changes():
			}
			continue
		}
		if startup != nil {
			state := startupState(startup.Load())
			if pending.Value().Kind() == model.UpdateReady && state == startupWaiting {
				if core.publisherBeforeCommitHook != nil {
					core.publisherBeforeCommitHook()
				}
				if startup.CompareAndSwap(uint32(startupWaiting), uint32(startupCommitted)) {
					if core.publisherCommittedHook != nil {
						core.publisherCommittedHook()
					}
					// Send admission must be observable before UpdateReady. A request
					// admitted in this narrow window remains bounded in realtimeQ until
					// the component start gate opens after Ready publication.
					core.acceptingSends.Store(true)
				} else {
					_ = pending.Release()
					hasPending = false
					continue
				}
			} else if state != startupCommitted {
				_ = pending.Release()
				hasPending = false
				continue
			}
		}
		if mailboxStopped(core.updateMailbox) {
			return
		}
		select {
		case core.updates <- pending.Value():
			if pending.Value().Kind() == model.UpdateReady && readyPublished != nil {
				readyOnce.Do(func() { close(readyPublished) })
			}
			_ = pending.Release()
			hasPending = false
		case <-core.updateMailbox.changes():
			// Retain the sole pending lease. The next successful send is followed
			// by the synchronous TakeDirty rescan at the top of the loop.
		case <-ctx.Done():
			return
		}
	}
}

func mailboxStopped[K comparable, V any](mailbox *fixedMailbox[K, V]) bool {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	return mailbox.stopped
}

func weighEvent(event model.Event) (int64, error) {
	normalized, err := model.NewEvent(event.Message(), event.ReceivedAt())
	if err != nil || normalized.ByteSize() <= 0 {
		return 0, errInvalidWeight
	}
	return int64(normalized.ByteSize()), nil
}
func weighChunk(chunk model.HistoryChunk) (int64, error) {
	var messages [model.MaxHistoryChunkRecords]model.Message
	for i := 0; i < chunk.Len(); i++ {
		m, ok := chunk.At(i)
		if !ok {
			return 0, errInvalidWeight
		}
		messages[i] = m
	}
	normalized, err := model.NewHistoryChunk(messages[:chunk.Len()])
	if err != nil || normalized.ByteSize() <= 0 {
		return 0, errInvalidWeight
	}
	return int64(normalized.ByteSize()), nil
}
func weighMessage(message model.Message) (int64, error) {
	var messages [1]model.Message
	messages[0] = message
	batch, err := model.NewWriteBatch(model.WriteRealtime, messages[:])
	if err != nil {
		return 0, errInvalidWeight
	}
	m, _ := batch.At(0)
	if m.ByteSize() <= 0 {
		return 0, errInvalidWeight
	}
	return int64(m.ByteSize()), nil
}
func weighUpdate(update model.Update) (int64, error) {
	normalized, err := model.NewUpdate(model.UpdateInput{Kind: update.Kind(), ChatID: update.ChatID(), MessageID: update.MessageID(), Accepted: update.Accepted(), Discarded: update.Discarded(), Degraded: update.Degraded()})
	if err != nil || normalized.ByteSize() <= 0 {
		return 0, errInvalidWeight
	}
	return int64(normalized.ByteSize()), nil
}
