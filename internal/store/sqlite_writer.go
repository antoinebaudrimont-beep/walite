package store

import (
	"context"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const (
	sqliteWriteQueueCapacity = 64
	sqliteMaxBatchWrites     = model.MaxWriteBatchMessages
	sqliteMaxBatchAge        = 25 * time.Millisecond
)

type pendingWriteKind uint8

const (
	pendingMessageWrite pendingWriteKind = iota + 1
	pendingContactWrite
	pendingChatWrite
	pendingDisplayWrite
	pendingLocalReadWrite
	pendingReactionWrite
)

// A single mailbox carries only this request's definitive transaction outcome.
// Live results are allocated only for realtime callers, never queued history.
type sqliteWriteResult struct {
	committed *model.LiveEventBatch
	display   model.DisplayMetadata
	reaction  model.ReactionSummary
	err       error
}

type pendingWrite struct {
	kind        pendingWriteKind
	contact     model.Contact
	chat        model.Chat
	messages    model.WriteBatch
	display     model.DisplayMetadata
	chatID      model.ChatID
	readThrough time.Time
	reaction    model.Reaction
	emitLive    bool
	result      sqliteWriteResult
	ack         chan sqliteWriteResult
	queuedAt    time.Time
}

func newPendingMessages(batch model.WriteBatch) pendingWrite {
	return pendingWrite{kind: pendingMessageWrite, messages: batch}
}

func newPendingContact(contact model.Contact) pendingWrite {
	return pendingWrite{kind: pendingContactWrite, contact: contact}
}

func newPendingChat(chat model.Chat) pendingWrite {
	return pendingWrite{kind: pendingChatWrite, chat: chat}
}

func newPendingLocalRead(chatID model.ChatID, through time.Time) pendingWrite {
	return pendingWrite{kind: pendingLocalReadWrite, chatID: chatID, readThrough: through}
}

func newPendingReaction(reaction model.Reaction) pendingWrite {
	return pendingWrite{kind: pendingReactionWrite, reaction: reaction}
}

func (pending pendingWrite) logicalWrites() int {
	if pending.kind != pendingMessageWrite {
		return 1
	}
	return pending.messages.Len()
}

type sqliteBatchTimer interface {
	C() <-chan time.Time
	Reset(time.Duration) bool
	Stop() bool
}

type realSQLiteBatchTimer struct {
	timer *time.Timer
}

func newRealSQLiteBatchTimer() sqliteBatchTimer {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	return &realSQLiteBatchTimer{timer: timer}
}

func (timer *realSQLiteBatchTimer) C() <-chan time.Time {
	return timer.timer.C
}

func (timer *realSQLiteBatchTimer) Reset(duration time.Duration) bool {
	return timer.timer.Reset(duration)
}

func (timer *realSQLiteBatchTimer) Stop() bool {
	return timer.timer.Stop()
}

type sqliteWriterHooks struct {
	beforeRun          func()
	beforePendingWrite func(pendingWriteKind) error
	beforeCommit       func() error
	afterCommit        func()
	checkFiles         func() error
	afterEnqueue       func()
	beforeQueueWait    func()
	afterBatchAdd      func(logicalWrites int)
	afterTransaction   func(
		logicalWrites int,
		duration time.Duration,
		err error,
	)
}

type sqliteWriterOptions struct {
	timer sqliteBatchTimer
	hooks sqliteWriterHooks
	now   func() time.Time
}

type sqliteBatchWriter struct {
	store *SQLiteStore
	queue chan pendingWrite
	timer sqliteBatchTimer
	hooks sqliteWriterHooks
	now   func() time.Time

	acceptMu  sync.Mutex
	accepting bool
	acceptEnd chan struct{}
	enqueueWG sync.WaitGroup

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newSQLiteBatchWriter(store *SQLiteStore, options sqliteWriterOptions) *sqliteBatchWriter {
	timer := options.timer
	if timer == nil {
		timer = newRealSQLiteBatchTimer()
	}
	now := options.now
	if now == nil {
		now = time.Now
	}
	return &sqliteBatchWriter{
		store:     store,
		queue:     make(chan pendingWrite, sqliteWriteQueueCapacity),
		timer:     timer,
		hooks:     options.hooks,
		now:       now,
		accepting: true,
		acceptEnd: make(chan struct{}),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

func (writer *sqliteBatchWriter) submit(ctx context.Context, pending pendingWrite) error {
	return writer.submitResult(ctx, pending).err
}

func (writer *sqliteBatchWriter) submitResult(ctx context.Context, pending pendingWrite) sqliteWriteResult {
	if err := sqliteContextError(ctx); err != nil {
		return sqliteWriteResult{err: err}
	}
	logicalWrites := pending.logicalWrites()
	if logicalWrites <= 0 || logicalWrites > sqliteMaxBatchWrites {
		return sqliteWriteResult{err: newSQLiteError(ErrStoreRejected, nil)}
	}
	if !writer.beginEnqueue() {
		return sqliteWriteResult{err: newSQLiteError(ErrStoreClosed, nil)}
	}
	pending.ack = make(chan sqliteWriteResult, 1)
	pending.queuedAt = writer.now()
	enqueued := false
	select {
	case writer.queue <- pending:
		enqueued = true
	default:
		if writer.hooks.beforeQueueWait != nil {
			writer.hooks.beforeQueueWait()
		}
		select {
		case writer.queue <- pending:
			enqueued = true
		case <-ctx.Done():
		case <-writer.acceptEnd:
		}
	}
	writer.enqueueWG.Done()
	if !enqueued {
		if err := ctx.Err(); err != nil {
			return sqliteWriteResult{err: err}
		}
		return sqliteWriteResult{err: newSQLiteError(ErrStoreClosed, nil)}
	}
	if writer.hooks.afterEnqueue != nil {
		writer.hooks.afterEnqueue()
	}
	// Queue admission transfers ownership. Cancellation can no longer describe
	// accepted work as failed while it commits later. Close drains this mailbox.
	return <-pending.ack
}

func (writer *sqliteBatchWriter) beginEnqueue() bool {
	writer.acceptMu.Lock()
	defer writer.acceptMu.Unlock()
	if !writer.accepting {
		return false
	}
	writer.enqueueWG.Add(1)
	return true
}

func (writer *sqliteBatchWriter) shutdown() {
	writer.stopOnce.Do(func() {
		writer.acceptMu.Lock()
		writer.accepting = false
		close(writer.acceptEnd)
		writer.acceptMu.Unlock()

		writer.enqueueWG.Wait()
		close(writer.stop)
		<-writer.done
	})
}

func (writer *sqliteBatchWriter) run() {
	defer close(writer.done)
	if writer.hooks.beforeRun != nil {
		writer.hooks.beforeRun()
	}

	var (
		batch         [sqliteMaxBatchWrites]pendingWrite
		requestCount  int
		logicalWrites int
		timerChannel  <-chan time.Time
	)
	armTimer := func(oldestQueuedAt time.Time) {
		stopSQLiteBatchTimer(writer.timer)
		remaining := sqliteMaxBatchAge - writer.now().Sub(oldestQueuedAt)
		if remaining < 0 {
			remaining = 0
		}
		writer.timer.Reset(remaining)
		timerChannel = writer.timer.C()
	}
	add := func(pending pendingWrite) {
		batch[requestCount] = pending
		requestCount++
		logicalWrites += pending.logicalWrites()
		if requestCount == 1 {
			armTimer(pending.queuedAt)
		}
		if writer.hooks.afterBatchAdd != nil {
			writer.hooks.afterBatchAdd(logicalWrites)
		}
	}
	flush := func() {
		if requestCount == 0 {
			return
		}
		stopSQLiteBatchTimer(writer.timer)
		timerChannel = nil
		duration, err := writer.store.commitPendingWrites(&batch, requestCount)
		if writer.hooks.afterTransaction != nil {
			writer.hooks.afterTransaction(logicalWrites, duration, err)
		}
		for index := 0; index < requestCount; index++ {
			result := batch[index].result
			if err != nil {
				result = sqliteWriteResult{err: err}
			}
			batch[index].ack <- result
			batch[index] = pendingWrite{}
		}
		requestCount = 0
		logicalWrites = 0
	}
	drainAndStop := func() {
		stopSQLiteBatchTimer(writer.timer)
		for {
			select {
			case pending := <-writer.queue:
				if logicalWrites+pending.logicalWrites() > sqliteMaxBatchWrites {
					flush()
				}
				add(pending)
				if logicalWrites == sqliteMaxBatchWrites {
					flush()
				}
			default:
				flush()
				return
			}
		}
	}

	for {
		if logicalWrites == sqliteMaxBatchWrites {
			flush()
			continue
		}
		select {
		case pending := <-writer.queue:
			if logicalWrites+pending.logicalWrites() > sqliteMaxBatchWrites {
				flush()
			}
			add(pending)
		case <-timerChannel:
			flush()
		case <-writer.stop:
			drainAndStop()
			return
		}
	}
}

func stopSQLiteBatchTimer(timer sqliteBatchTimer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C():
	default:
	}
}
