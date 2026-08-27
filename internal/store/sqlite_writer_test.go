package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const sqliteWriterTestTimeout = 5 * time.Second

type manualSQLiteBatchTimer struct {
	mu        sync.Mutex
	channel   chan time.Time
	resets    chan time.Duration
	active    bool
	fireCount int
}

func newManualSQLiteBatchTimer() *manualSQLiteBatchTimer {
	return &manualSQLiteBatchTimer{
		channel: make(chan time.Time, 1),
		resets:  make(chan time.Duration, 256),
	}
}

func (timer *manualSQLiteBatchTimer) C() <-chan time.Time {
	return timer.channel
}

func (timer *manualSQLiteBatchTimer) Reset(duration time.Duration) bool {
	timer.mu.Lock()
	wasActive := timer.active
	timer.active = true
	timer.mu.Unlock()
	timer.resets <- duration
	return wasActive
}

func (timer *manualSQLiteBatchTimer) Stop() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	wasActive := timer.active
	timer.active = false
	return wasActive
}

func (timer *manualSQLiteBatchTimer) Fire() bool {
	timer.mu.Lock()
	if !timer.active {
		timer.mu.Unlock()
		return false
	}
	timer.active = false
	timer.fireCount++
	timer.mu.Unlock()
	timer.channel <- time.UnixMilli(1).UTC()
	return true
}

func (timer *manualSQLiteBatchTimer) waitReset(t *testing.T) {
	t.Helper()
	if got := receiveSQLiteTest(t, timer.resets); got != sqliteMaxBatchAge {
		t.Fatalf("timer reset=%v want=%v", got, sqliteMaxBatchAge)
	}
}

func (timer *manualSQLiteBatchTimer) fires() int {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	return timer.fireCount
}

type sqliteTransactionObservation struct {
	logicalWrites int
	duration      time.Duration
	err           error
}

type sqliteWriterTestSignals struct {
	enqueued     chan struct{}
	queueWait    chan struct{}
	batchAdds    chan int
	transactions chan sqliteTransactionObservation
}

func newSQLiteWriterTestSignals() *sqliteWriterTestSignals {
	return &sqliteWriterTestSignals{
		enqueued:     make(chan struct{}, 512),
		queueWait:    make(chan struct{}, 16),
		batchAdds:    make(chan int, 512),
		transactions: make(chan sqliteTransactionObservation, 512),
	}
}

func (signals *sqliteWriterTestSignals) hooks() sqliteWriterHooks {
	return sqliteWriterHooks{
		afterEnqueue: func() { signals.enqueued <- struct{}{} },
		beforeQueueWait: func() {
			signals.queueWait <- struct{}{}
		},
		afterBatchAdd: func(logicalWrites int) {
			signals.batchAdds <- logicalWrites
		},
		afterTransaction: func(logicalWrites int, duration time.Duration, err error) {
			signals.transactions <- sqliteTransactionObservation{
				logicalWrites: logicalWrites,
				duration:      duration,
				err:           err,
			}
		},
	}
}

func openSQLiteWriterTestStore(t *testing.T, timer sqliteBatchTimer, hooks sqliteWriterHooks) (*SQLiteStore, string) {
	t.Helper()
	path := testSQLitePath(t)
	store, err := openSQLiteWithWriter(
		context.Background(),
		SQLiteOptions{Path: path},
		operatingSystemFS,
		nil,
		sqliteWriterOptions{
			timer: timer,
			hooks: hooks,
			now:   func() time.Time { return time.Unix(1, 0).UTC() },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store, path
}

func TestSQLiteWriterTimerFlushesSingleQueuedWrite(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())
	message := testMessage(t, "chat-timer", "message-1", "timer", time.UnixMilli(1).UTC(), false)

	result := asyncSQLiteCall(func() error {
		return store.SubmitMessage(context.Background(), message)
	})
	receiveSQLiteTest(t, signals.enqueued)
	if got := receiveSQLiteTest(t, signals.batchAdds); got != 1 {
		t.Fatalf("active logical writes=%d", got)
	}
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active")
	}
	if err := receiveSQLiteTest(t, result); err != nil {
		t.Fatal(err)
	}
	observation := receiveSQLiteTest(t, signals.transactions)
	if observation.logicalWrites != 1 || observation.err != nil {
		t.Fatalf("transaction=%+v", observation)
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); got != 1 {
		t.Fatalf("message count=%d", got)
	}
}

func TestSQLiteWriterTimerAccountsForTimeAlreadyQueued(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	hooks := signals.hooks()
	hooks.beforeRun = func() {
		close(started)
		<-release
	}
	var clock struct {
		sync.Mutex
		now time.Time
	}
	clock.now = time.Unix(1, 0).UTC()
	now := func() time.Time {
		clock.Lock()
		defer clock.Unlock()
		return clock.now
	}
	path := testSQLitePath(t)
	store, err := openSQLiteWithWriter(
		context.Background(),
		SQLiteOptions{Path: path},
		operatingSystemFS,
		nil,
		sqliteWriterOptions{timer: timer, hooks: hooks, now: now},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	t.Cleanup(releaseWriter)
	receiveSQLiteTest(t, started)

	message := testMessage(t, "chat-aged", "message-1", "aged", time.UnixMilli(1).UTC(), false)
	result := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), message) })
	receiveSQLiteTest(t, signals.enqueued)
	clock.Lock()
	clock.now = clock.now.Add(sqliteMaxBatchAge)
	clock.Unlock()
	releaseWriter()
	if got := receiveSQLiteTest(t, timer.resets); got != 0 {
		t.Fatalf("aged timer reset=%v want=0", got)
	}
	if !timer.Fire() {
		t.Fatal("aged timer was not active")
	}
	if err := receiveSQLiteTest(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteWriterFullBatchFlushesImmediatelyAndStartsNextBatch(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())

	results := make([]<-chan error, sqliteMaxBatchWrites)
	for index := range sqliteMaxBatchWrites {
		message := testMessage(t, "chat-full", fmt.Sprintf("message-%02d", index), "full", time.UnixMilli(int64(index+1)).UTC(), false)
		results[index] = asyncSQLiteCall(func() error {
			return store.SubmitMessage(context.Background(), message)
		})
		receiveSQLiteTest(t, signals.enqueued)
	}
	observation := receiveSQLiteTest(t, signals.transactions)
	if observation.logicalWrites != sqliteMaxBatchWrites || observation.err != nil {
		t.Fatalf("first transaction=%+v", observation)
	}
	for _, result := range results {
		if err := receiveSQLiteTest(t, result); err != nil {
			t.Fatal(err)
		}
	}
	if timer.fires() != 0 {
		t.Fatalf("timer fires=%d", timer.fires())
	}

	message51 := testMessage(t, "chat-full", "message-50", "next", time.UnixMilli(51).UTC(), false)
	result51 := asyncSQLiteCall(func() error {
		return store.SubmitMessage(context.Background(), message51)
	})
	receiveSQLiteTest(t, signals.enqueued)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := receiveSQLiteTest(t, result51); err != nil {
		t.Fatal(err)
	}
	observation = receiveSQLiteTest(t, signals.transactions)
	if observation.logicalWrites != 1 || observation.err != nil {
		t.Fatalf("second transaction=%+v", observation)
	}
}

func TestSQLiteWriterGroups125WritesAs50_50_25(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())

	results := make([]<-chan error, 125)
	for index := range results {
		message := testMessage(t, "chat-125", fmt.Sprintf("message-%03d", index), "group", time.UnixMilli(int64(index+1)).UTC(), false)
		results[index] = asyncSQLiteCall(func() error {
			return store.SubmitMessage(context.Background(), message)
		})
		receiveSQLiteTest(t, signals.enqueued)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if err := receiveSQLiteTest(t, result); err != nil {
			t.Fatal(err)
		}
	}
	got := []int{
		receiveSQLiteTest(t, signals.transactions).logicalWrites,
		receiveSQLiteTest(t, signals.transactions).logicalWrites,
		receiveSQLiteTest(t, signals.transactions).logicalWrites,
	}
	want := []int{50, 50, 25}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("transaction grouping=%v want=%v", got, want)
		}
	}
}

func TestSQLiteWriterPreservesSameKeyOrderAndDuplicateIdempotence(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())
	sentAt := time.UnixMilli(10).UTC()
	first := testMessage(t, "chat-order", "same-message", "first", sentAt, false)
	second := testMessage(t, "chat-order", "same-message", "second", sentAt, false)

	firstResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), first) })
	receiveSQLiteTest(t, signals.enqueued)
	secondResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), second) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 2)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active")
	}
	if err := receiveSQLiteTest(t, firstResult); err != nil {
		t.Fatal(err)
	}
	if err := receiveSQLiteTest(t, secondResult); err != nil {
		t.Fatal(err)
	}
	got, err := store.Message(context.Background(), first.ChatID(), first.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Text() != "second" {
		t.Fatalf("stored text=%q", got.Text())
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 1 {
		t.Fatalf("same-batch duplicate count=%d", count)
	}

	thirdResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), second) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 1)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active for second batch")
	}
	if err := receiveSQLiteTest(t, thirdResult); err != nil {
		t.Fatal(err)
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 1 {
		t.Fatalf("cross-batch duplicate count=%d", count)
	}
}

func TestSQLiteWriterRollbackIsRecoverable(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())
	seed := testMessage(t, "chat-recovery", "message-seed", "seed", time.UnixMilli(1).UTC(), false)
	seedResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), seed) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 1)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active for seed")
	}
	if err := receiveSQLiteTest(t, seedResult); err != nil {
		t.Fatal(err)
	}
	receiveSQLiteTest(t, signals.transactions)
	if _, err := store.db.ExecContext(context.Background(), `
CREATE TRIGGER synthetic_batch_failure
BEFORE INSERT ON messages
WHEN NEW.message_id = 'message-fail'
BEGIN
    SELECT RAISE(ABORT, 'synthetic failure');
END`); err != nil {
		t.Fatal(err)
	}
	okMessage := testMessage(t, "chat-recovery", "message-ok", "ok", time.UnixMilli(2).UTC(), false)
	failMessage := testMessage(t, "chat-recovery", "message-fail", "fail", time.UnixMilli(3).UTC(), false)
	okResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), okMessage) })
	receiveSQLiteTest(t, signals.enqueued)
	failResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), failMessage) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 2)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active")
	}
	for _, result := range []<-chan error{okResult, failResult} {
		if err := receiveSQLiteTest(t, result); !errors.Is(err, ErrCacheIO) {
			t.Fatalf("batch error=%v", err)
		}
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 1 {
		t.Fatalf("rolled-back message count=%d", count)
	}
	if _, err := store.db.ExecContext(context.Background(), "DROP TRIGGER synthetic_batch_failure"); err != nil {
		t.Fatal(err)
	}
	healthy := testMessage(t, "chat-recovery", "message-after", "after", time.UnixMilli(4).UTC(), false)
	healthyResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), healthy) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 1)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active after recovery")
	}
	if err := receiveSQLiteTest(t, healthyResult); err != nil {
		t.Fatal(err)
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 2 {
		t.Fatalf("recovery message count=%d", count)
	}
}

func TestSQLiteWriterCancellationSemantics(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())
	message := testMessage(t, "chat-cancel", "message-1", "accepted", time.UnixMilli(1).UTC(), false)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.SubmitMessage(canceled, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-enqueue cancellation=%v", err)
	}

	waiting, cancelWaiting := context.WithCancel(context.Background())
	result := asyncSQLiteCall(func() error { return store.SubmitMessage(waiting, message) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 1)
	timer.waitReset(t)
	cancelWaiting()
	if err := receiveSQLiteTest(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("acknowledgement cancellation=%v", err)
	}
	if !timer.Fire() {
		t.Fatal("timer was not active")
	}
	observation := receiveSQLiteTest(t, signals.transactions)
	if observation.err != nil {
		t.Fatal(observation.err)
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 1 {
		t.Fatalf("accepted canceled write count=%d", count)
	}
}

func TestSQLiteWriterQueueSaturationUsesCancellableBackpressure(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	hooks := signals.hooks()
	hooks.beforeRun = func() {
		close(started)
		<-release
	}
	store, _ := openSQLiteWriterTestStore(t, timer, hooks)
	t.Cleanup(releaseWriter)
	receiveSQLiteTest(t, started)

	results := make([]<-chan error, sqliteWriteQueueCapacity)
	for index := range results {
		message := testMessage(t, "chat-queue", fmt.Sprintf("message-%02d", index), "queued", time.UnixMilli(int64(index+1)).UTC(), false)
		results[index] = asyncSQLiteCall(func() error {
			return store.SubmitMessage(context.Background(), message)
		})
		receiveSQLiteTest(t, signals.enqueued)
	}
	if got := len(store.writer.queue); got != sqliteWriteQueueCapacity {
		t.Fatalf("queue length=%d capacity=%d", got, sqliteWriteQueueCapacity)
	}

	blockedContext, cancelBlocked := context.WithCancel(context.Background())
	extra := testMessage(t, "chat-queue", "message-extra", "extra", time.UnixMilli(100).UTC(), false)
	extraResult := asyncSQLiteCall(func() error {
		return store.SubmitMessage(blockedContext, extra)
	})
	receiveSQLiteTest(t, signals.queueWait)
	cancelBlocked()
	if err := receiveSQLiteTest(t, extraResult); !errors.Is(err, context.Canceled) {
		t.Fatalf("saturated submission error=%v", err)
	}
	if got := len(store.writer.queue); got != sqliteWriteQueueCapacity {
		t.Fatalf("queue grew to %d", got)
	}

	releaseWriter()
	timer.waitReset(t)
	first := receiveSQLiteTest(t, signals.transactions)
	if first.logicalWrites != 50 || first.err != nil {
		t.Fatalf("first transaction=%+v", first)
	}
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active for remaining queue")
	}
	second := receiveSQLiteTest(t, signals.transactions)
	if second.logicalWrites != 14 || second.err != nil {
		t.Fatalf("second transaction=%+v", second)
	}
	for _, result := range results {
		if err := receiveSQLiteTest(t, result); err != nil {
			t.Fatal(err)
		}
	}

	healthy := testMessage(t, "chat-queue", "message-after", "after", time.UnixMilli(101).UTC(), false)
	healthyResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), healthy) })
	receiveSQLiteTest(t, signals.enqueued)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active after saturation")
	}
	if err := receiveSQLiteTest(t, healthyResult); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteWriterBusyErrorAndRecovery(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, path := openSQLiteWriterTestStore(t, timer, signals.hooks())
	seed := testMessage(t, "chat-busy", "message-seed", "seed", time.UnixMilli(1).UTC(), false)
	seedResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), seed) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 1)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active for seed")
	}
	if err := receiveSQLiteTest(t, seedResult); err != nil {
		t.Fatal(err)
	}
	receiveSQLiteTest(t, signals.transactions)

	lockingDB := rawSQLite(t, path)
	lockingDB.SetMaxOpenConns(1)
	connection, err := lockingDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	lockedMessage := testMessage(t, "chat-busy", "message-locked", "locked", time.UnixMilli(2).UTC(), false)
	lockedResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), lockedMessage) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 1)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active for locked write")
	}
	if err := receiveSQLiteTest(t, lockedResult); !errors.Is(err, ErrBusy) {
		t.Fatalf("locked write error=%v", err)
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 1 {
		t.Fatalf("message count under lock=%d", count)
	}
	if _, err := connection.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}

	healthy := testMessage(t, "chat-busy", "message-after", "after", time.UnixMilli(3).UTC(), false)
	healthyResult := asyncSQLiteCall(func() error { return store.SubmitMessage(context.Background(), healthy) })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 1)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer was not active after busy recovery")
	}
	if err := receiveSQLiteTest(t, healthyResult); err != nil {
		t.Fatal(err)
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 2 {
		t.Fatalf("message count after recovery=%d", count)
	}
}

func TestSQLiteWriterCloseFlushesAndJoins(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, path := openSQLiteWriterTestStore(t, timer, signals.hooks())

	results := make([]<-chan error, 5)
	for index := range results {
		message := testMessage(t, "chat-close", fmt.Sprintf("message-%d", index), "pending", time.UnixMilli(int64(index+1)).UTC(), false)
		results[index] = asyncSQLiteCall(func() error {
			return store.SubmitMessage(context.Background(), message)
		})
		receiveSQLiteTest(t, signals.enqueued)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if err := receiveSQLiteTest(t, result); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-store.writer.done:
	default:
		t.Fatal("writer goroutine did not join")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close error=%v", err)
	}
	message := testMessage(t, "chat-close", "message-after", "closed", time.UnixMilli(10).UTC(), false)
	if err := store.SubmitMessage(context.Background(), message); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("write after Close error=%v", err)
	}
	database := rawSQLite(t, path)
	if count := queryCount(t, database, "SELECT COUNT(*) FROM messages"); count != len(results) {
		t.Fatalf("closed database message count=%d", count)
	}
}

func TestSQLiteBatchTransactionLatency(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())
	const samples = 20
	for _, size := range []int{1, 10, 50} {
		durations := make([]time.Duration, 0, samples)
		for sample := range samples {
			batch := testSQLiteWriteBatch(t, fmt.Sprintf("latency-%d-%d", size, sample), size)
			result := asyncSQLiteCall(func() error { return store.Write(context.Background(), batch) })
			waitSQLiteBatchLogical(t, signals.batchAdds, size)
			if size < sqliteMaxBatchWrites {
				timer.waitReset(t)
				if !timer.Fire() {
					t.Fatal("timer was not active for latency sample")
				}
			}
			if err := receiveSQLiteTest(t, result); err != nil {
				t.Fatal(err)
			}
			observation := receiveSQLiteTest(t, signals.transactions)
			if observation.logicalWrites != size || observation.err != nil {
				t.Fatalf("latency transaction=%+v", observation)
			}
			durations = append(durations, observation.duration)
		}
		sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
		p50 := durations[(len(durations)-1)/2]
		p95 := durations[(95*len(durations)+99)/100-1]
		t.Logf("SQLite transaction batch=%d samples=%d p50=%v p95=%v max=%v", size, samples, p50, p95, durations[len(durations)-1])
	}
}

func TestSQLiteWriterResourceBounds(t *testing.T) {
	if sqliteWriteQueueCapacity != 64 || sqliteMaxBatchWrites != 50 || sqliteMaxBatchAge != 25*time.Millisecond {
		t.Fatalf("writer bounds queue=%d batch=%d age=%v", sqliteWriteQueueCapacity, sqliteMaxBatchWrites, sqliteMaxBatchAge)
	}
}

func testSQLiteWriteBatch(t *testing.T, prefix string, size int) model.WriteBatch {
	t.Helper()
	messages := make([]model.Message, size)
	for index := range messages {
		messages[index] = testMessage(
			t,
			"chat-"+prefix,
			fmt.Sprintf("message-%03d", index),
			"synthetic batch message",
			time.UnixMilli(int64(index+1)).UTC(),
			false,
		)
	}
	batch, err := model.NewWriteBatch(model.WriteRealtime, messages)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func waitSQLiteBatchLogical(t *testing.T, values <-chan int, want int) {
	t.Helper()
	for {
		if got := receiveSQLiteTest(t, values); got == want {
			return
		}
	}
}

func asyncSQLiteCall(call func() error) <-chan error {
	result := make(chan error, 1)
	go func() { result <- call() }()
	return result
}

func receiveSQLiteTest[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(sqliteWriterTestTimeout):
		t.Fatal("timed out waiting for SQLite writer test event")
		var zero T
		return zero
	}
}

var _ sqliteBatchTimer = (*manualSQLiteBatchTimer)(nil)
