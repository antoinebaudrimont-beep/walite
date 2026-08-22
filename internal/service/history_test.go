package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

type historyScript struct {
	job          model.HistoryJob
	chunks       [4]model.HistoryChunk
	count        int
	next         int
	fail         error
	emptyNotDone bool
}

type historySource struct {
	metadata chan model.HistoryJob
	onDemand chan model.HistoryJob
	bulk     chan model.HistoryJob
	mu       sync.Mutex
	scripts  [8]historyScript
	scriptN  int
	calls    [32]model.HistoryJob
	callN    int
	records  [32]int
	bytes    [32]int64
	releases [16]model.HistoryJob
	releaseN int
	entered  chan struct{}
	resume   <-chan struct{}
}

func newHistorySource() *historySource {
	return &historySource{metadata: make(chan model.HistoryJob, 2), onDemand: make(chan model.HistoryJob, 1), bulk: make(chan model.HistoryJob, 1)}
}
func (source *historySource) Run(context.Context) error                    { return nil }
func (source *historySource) RealtimeEvents() <-chan model.Event           { return nil }
func (source *historySource) MetadataHistoryJobs() <-chan model.HistoryJob { return source.metadata }
func (source *historySource) OnDemandHistoryJobs() <-chan model.HistoryJob { return source.onDemand }
func (source *historySource) BulkHistoryJobs() <-chan model.HistoryJob     { return source.bulk }
func (source *historySource) StatusWake() <-chan struct{}                  { return nil }
func (source *historySource) Status() model.SourceStatus                   { return model.SourceStatus{} }
func (source *historySource) NextHistory(ctx context.Context, job model.HistoryJob, records int, bytes int64) (model.HistoryChunk, bool, error) {
	source.mu.Lock()
	if source.callN == len(source.calls) {
		source.mu.Unlock()
		return model.HistoryChunk{}, false, errQueueInvariant
	}
	source.calls[source.callN], source.records[source.callN], source.bytes[source.callN] = job, records, bytes
	source.callN++
	entered, resume := source.entered, source.resume
	var script *historyScript
	for index := 0; index < source.scriptN; index++ {
		if source.scripts[index].job.ID().String() == job.ID().String() {
			script = &source.scripts[index]
			break
		}
	}
	if script == nil {
		source.mu.Unlock()
		return model.HistoryChunk{}, false, errQueueInvariant
	}
	failure, emptyNotDone := script.fail, script.emptyNotDone
	var chunk model.HistoryChunk
	done := true
	if script.next < script.count {
		chunk = script.chunks[script.next]
		script.next++
		done = script.next == script.count
	}
	source.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if resume != nil {
		select {
		case <-ctx.Done():
			return model.HistoryChunk{}, false, ctx.Err()
		case <-resume:
		}
	}
	if failure != nil {
		return model.HistoryChunk{}, false, failure
	}
	if emptyNotDone {
		return model.HistoryChunk{}, false, nil
	}
	return chunk, done, nil
}
func (source *historySource) ReleaseHistory(job model.HistoryJob) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.releaseN < len(source.releases) {
		source.releases[source.releaseN] = job
		source.releaseN++
	}
}
func (source *historySource) add(job model.HistoryJob, chunks ...model.HistoryChunk) {
	script := &source.scripts[source.scriptN]
	script.job, script.count = job, len(chunks)
	copy(script.chunks[:], chunks)
	source.scriptN++
}
func (source *historySource) closeAll() {
	close(source.metadata)
	close(source.onDemand)
	close(source.bulk)
}
func (source *historySource) observations() ([]model.HistoryJob, []model.HistoryJob) {
	source.mu.Lock()
	defer source.mu.Unlock()
	calls := make([]model.HistoryJob, source.callN)
	copy(calls, source.calls[:source.callN])
	releases := make([]model.HistoryJob, source.releaseN)
	copy(releases, source.releases[:source.releaseN])
	return calls, releases
}

func TestHistoryTransformerConstruction(t *testing.T) {
	source := newHistorySource()
	queue := newChunkQueue(t, 4)
	tests := []struct {
		source  EventSource
		queue   *boundedQueue[model.HistoryChunk]
		records int
		bytes   int64
	}{
		{nil, queue, 1, 1}, {source, nil, 1, 1}, {source, queue, 0, 1}, {source, queue, -1, 1},
		{source, queue, model.MaxHistoryChunkRecords + 1, 1}, {source, queue, 1, 0}, {source, queue, 1, -1}, {source, queue, 1, model.MaxHistoryChunkBytes + 1},
	}
	for index, test := range tests {
		if _, err := newHistoryTransformer(test.source, test.queue, test.records, test.bytes); err == nil {
			t.Fatalf("case %d succeeded", index)
		}
	}
	if _, err := newHistoryTransformer(source, queue, model.MaxHistoryChunkRecords, model.MaxHistoryChunkBytes); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryTransformerTraversalQuantumPriorityAndRelease(t *testing.T) {
	source := newHistorySource()
	queue := newChunkQueue(t, 8)
	metadata1 := mustHistoryJob(t, "metadata-1", model.HistoryMetadata)
	metadata2 := mustHistoryJob(t, "metadata-2", model.HistoryMetadata)
	onDemand := mustHistoryJob(t, "on-demand", model.HistoryOnDemand)
	bulk := mustHistoryJob(t, "bulk", model.HistoryBulk)
	one := mustChunk(t, historyMessage(t, "one", 0))
	two := mustChunk(t, historyMessage(t, "two", 1))
	source.add(metadata1)
	source.add(metadata2, one)
	source.add(onDemand, one)
	source.add(bulk, one, two)
	source.metadata <- metadata1
	source.metadata <- metadata2
	source.onDemand <- onDemand
	source.bulk <- bulk
	source.closeAll()
	transformer, _ := newHistoryTransformer(source, queue, 7, 4096)
	done := make(chan error, 1)
	go func() { done <- transformer.Run(context.Background()) }()
	drainChunkQueueUntilDone(t, queue, done)
	calls, releases := source.observations()
	want := []string{"metadata-1", "metadata-2", "on-demand", "bulk", "bulk"}
	if len(calls) != len(want) || len(releases) != 4 {
		t.Fatalf("calls=%d releases=%d", len(calls), len(releases))
	}
	for index := range calls {
		if calls[index].ID().String() != want[index] || source.records[index] != 7 || source.bytes[index] != 4096 {
			t.Fatalf("call %d=%q quantum=%d/%d", index, calls[index].ID().String(), source.records[index], source.bytes[index])
		}
	}
	if !queue.Stats().Stopped || queue.budget.usedBytes() != 0 {
		t.Fatalf("queue stats=%+v", queue.Stats())
	}
}

func TestHistoryTransformerPausesBulkForOnDemand(t *testing.T) {
	source := newHistorySource()
	queue := newChunkQueue(t, 4)
	entered := make(chan struct{}, 1)
	resume := make(chan struct{})
	source.entered, source.resume = entered, resume
	bulk := mustHistoryJob(t, "bulk", model.HistoryBulk)
	onDemand := mustHistoryJob(t, "on-demand", model.HistoryOnDemand)
	source.add(bulk, mustChunk(t, historyMessage(t, "b1", 0)), mustChunk(t, historyMessage(t, "b2", 1)))
	source.add(onDemand, mustChunk(t, historyMessage(t, "o1", 2)))
	source.bulk <- bulk
	transformer, _ := newHistoryTransformer(source, queue, 1, model.MaxHistoryChunkBytes)
	done := make(chan error, 1)
	go func() { done <- transformer.Run(context.Background()) }()
	<-entered
	source.onDemand <- onDemand
	source.closeAll()
	close(resume)
	drainChunkQueueUntilDone(t, queue, done)
	calls, _ := source.observations()
	if len(calls) != 3 || calls[0].ID().String() != "bulk" || calls[1].ID().String() != "on-demand" || calls[2].ID().String() != "bulk" {
		t.Fatalf("calls=%v", jobIDs(calls))
	}
}

func TestHistoryTransformerErrorAndCancellationCleanup(t *testing.T) {
	failure := errors.New("history traversal failure")
	source := newHistorySource()
	queue := newChunkQueue(t, 1)
	selected := mustHistoryJob(t, "selected", model.HistoryMetadata)
	buffered := mustHistoryJob(t, "buffered", model.HistoryMetadata)
	source.add(selected)
	source.scripts[0].fail = failure
	source.add(buffered)
	source.metadata <- selected
	source.metadata <- buffered
	transformer, _ := newHistoryTransformer(source, queue, 1, 1)
	if err := transformer.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("Run=%v", err)
	}
	_, releases := source.observations()
	if len(releases) != 2 || !queue.Stats().Stopped {
		t.Fatalf("releases=%d stopped=%t", len(releases), queue.Stats().Stopped)
	}

	source = newHistorySource()
	queue = newChunkQueue(t, 1)
	job := mustHistoryJob(t, "blocked", model.HistoryBulk)
	source.add(job, mustChunk(t, historyMessage(t, "a", 0)), mustChunk(t, historyMessage(t, "b", 1)))
	source.bulk <- job
	putRawChunk(t, queue, mustChunk(t, historyMessage(t, "full", 2)))
	transformer, _ = newHistoryTransformer(source, queue, 1, model.MaxHistoryChunkBytes)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- transformer.Run(ctx) }()
	waitQueueStats(t, queue, func(stats queueStats) bool { return stats.EntryWaiters == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel Run=%v", err)
	}
	_, releases = source.observations()
	if len(releases) != 1 {
		t.Fatalf("cancel releases=%d", len(releases))
	}
	queue.drainAndRelease()
	if queue.budget.usedBytes() != 0 {
		t.Fatalf("queue leak=%d", queue.budget.usedBytes())
	}
}

func TestHistoryTransformerRejectsEmptyNonfinalChunk(t *testing.T) {
	source := newHistorySource()
	queue := newChunkQueue(t, 1)
	job := mustHistoryJob(t, "empty-nonfinal", model.HistoryOnDemand)
	source.add(job)
	source.scripts[0].emptyNotDone = true
	source.onDemand <- job
	source.closeAll()
	transformer, _ := newHistoryTransformer(source, queue, 1, 1)
	if err := transformer.Run(context.Background()); !errors.Is(err, errQueueInvariant) {
		t.Fatalf("Run=%v", err)
	}
	calls, releases := source.observations()
	if len(calls) != 1 || len(releases) != 1 || !queue.Stats().Stopped {
		t.Fatalf("calls=%d releases=%d stopped=%t", len(calls), len(releases), queue.Stats().Stopped)
	}
}

func TestHistoryTransformerRejectsMisroutedJobClasses(t *testing.T) {
	tests := []struct {
		name  string
		class model.HistoryClass
		put   func(*historySource, model.HistoryJob)
	}{
		{"bulk through metadata", model.HistoryBulk, func(source *historySource, job model.HistoryJob) { source.metadata <- job }},
		{"metadata through on-demand", model.HistoryMetadata, func(source *historySource, job model.HistoryJob) { source.onDemand <- job }},
		{"on-demand through bulk", model.HistoryOnDemand, func(source *historySource, job model.HistoryJob) { source.bulk <- job }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := newHistorySource()
			queue := newChunkQueue(t, 1)
			job := mustHistoryJob(t, "misrouted", test.class)
			test.put(source, job)
			source.closeAll()
			transformer, _ := newHistoryTransformer(source, queue, 1, 1)
			if err := transformer.Run(context.Background()); !errors.Is(err, errQueueInvariant) {
				t.Fatalf("Run=%v", err)
			}
			calls, releases := source.observations()
			if len(calls) != 0 || releaseCount(releases, job) != 1 {
				t.Fatalf("calls=%d releases=%d", len(calls), releaseCount(releases, job))
			}
			if !queue.Stats().Stopped || queue.budget.usedBytes() != 0 {
				t.Fatalf("historyQ stats=%+v used=%d", queue.Stats(), queue.budget.usedBytes())
			}
		})
	}
}

func TestHistoryTransformerMisrouteReleasesOwnedAndBufferedJobs(t *testing.T) {
	source := newHistorySource()
	queue := newChunkQueue(t, 1)
	owned := mustHistoryJob(t, "owned", model.HistoryMetadata)
	misrouted := mustHistoryJob(t, "misrouted", model.HistoryMetadata)
	buffered := mustHistoryJob(t, "buffered", model.HistoryBulk)
	source.metadata <- owned
	source.onDemand <- misrouted
	source.bulk <- buffered
	source.closeAll()
	transformer, _ := newHistoryTransformer(source, queue, 1, 1)
	if err := transformer.Run(context.Background()); !errors.Is(err, errQueueInvariant) {
		t.Fatalf("Run=%v", err)
	}
	calls, releases := source.observations()
	if len(calls) != 0 || releaseCount(releases, owned) != 1 || releaseCount(releases, misrouted) != 1 || releaseCount(releases, buffered) != 1 {
		t.Fatalf("calls=%d owned=%d misrouted=%d buffered=%d", len(calls), releaseCount(releases, owned), releaseCount(releases, misrouted), releaseCount(releases, buffered))
	}
	if !queue.Stats().Stopped || queue.budget.usedBytes() != 0 {
		t.Fatalf("historyQ stats=%+v used=%d", queue.Stats(), queue.budget.usedBytes())
	}
}

type historyStore struct {
	snapshot      model.RetentionSnapshot
	usage         model.CacheUsage
	snapshotErr   error
	usageErr      error
	snapshotCalls int
	usageCalls    int
}

type panicHistoryStore struct{ historyStore }

func (*panicHistoryStore) RetentionSnapshot(context.Context, model.ChatID) (model.RetentionSnapshot, error) {
	panic("private history panic")
}

func (*historyStore) EnsureChat(context.Context, model.Chat) error  { return nil }
func (*historyStore) Write(context.Context, model.WriteBatch) error { return nil }
func (*historyStore) Page(context.Context, model.ChatID, model.Cursor, int) ([]model.Message, model.Cursor, error) {
	return nil, model.NoCursor(), nil
}
func (store *historyStore) RetentionSnapshot(context.Context, model.ChatID) (model.RetentionSnapshot, error) {
	store.snapshotCalls++
	return store.snapshot, store.snapshotErr
}
func (*historyStore) ApplyPrune(context.Context, model.PrunePlan) (model.PruneResult, error) {
	return model.NewPruneResult(0, 0, false)
}
func (store *historyStore) Usage(context.Context) (model.CacheUsage, error) {
	store.usageCalls++
	return store.usage, store.usageErr
}

func TestHistoryIngesterReleasesCurrentAndBufferedChunksOnPanic(t *testing.T) {
	historyQ := newChunkQueue(t, 2)
	historyWrites := newMessageQueue(t, 2)
	putRawChunk(t, historyQ, mustChunk(t, historyMessage(t, "panic-current", 0)))
	putRawChunk(t, historyQ, mustChunk(t, historyMessage(t, "panic-tail", 1)))
	historyQ.Close()
	ingester, err := newHistoryIngester(&panicHistoryStore{}, &historyPolicy{}, newManualClock(writerTestTime), historyQ, historyWrites)
	if err != nil {
		t.Fatal(err)
	}
	var panicValue any
	func() {
		defer func() { panicValue = recover() }()
		_ = ingester.Run(context.Background())
	}()
	if panicValue == nil {
		t.Fatal("store panic was not propagated")
	}
	stats := historyQ.Stats()
	if stats.Entries != 0 || stats.UsedBytes != 0 {
		t.Fatalf("historyQ=%+v", stats)
	}
	if !historyWrites.Stats().Stopped || historyWrites.Stats().UsedBytes != 0 {
		t.Fatalf("historyWriteQ=%+v", historyWrites.Stats())
	}
}

type historyPolicy struct {
	actions [model.MaxHistoryChunkRecords]model.RetentionAction
	states  [model.MaxHistoryChunkRecords]model.RetentionState
	count   int
	fail    error
}

func (policy *historyPolicy) Decide(_ model.Message, state model.RetentionState) (model.RetentionDecision, error) {
	policy.states[policy.count] = state
	action := policy.actions[policy.count]
	policy.count++
	if policy.fail != nil {
		return model.RetentionDecision{}, policy.fail
	}
	if action == 0 {
		action = model.KeepBody
	}
	return model.NewRetentionDecision(action, model.RetentionEligible)
}
func (*historyPolicy) PlanPrune(model.RetentionSnapshot, model.RetentionState) (model.PrunePlan, error) {
	return model.PrunePlan{}, nil
}

type historyClock struct{ now time.Time }

func (clock historyClock) Now() time.Time         { return clock.now }
func (historyClock) NewTimer(time.Duration) Timer { return nil }

func TestHistoryIngesterConstruction(t *testing.T) {
	store := defaultHistoryStore(t)
	policy := &historyPolicy{}
	clock := historyClock{writerTestTime}
	input := newChunkQueue(t, 1)
	output := newMessageQueue(t, 1)
	tests := []struct {
		store  MessageStore
		policy RetentionPolicy
		clock  Clock
		input  *boundedQueue[model.HistoryChunk]
		output *boundedQueue[model.Message]
	}{
		{nil, policy, clock, input, output}, {store, nil, clock, input, output}, {store, policy, nil, input, output}, {store, policy, clock, nil, output}, {store, policy, clock, input, nil},
	}
	for index, test := range tests {
		if _, err := newHistoryIngester(test.store, test.policy, test.clock, test.input, test.output); err == nil {
			t.Fatalf("case %d succeeded", index)
		}
	}
	if _, err := newHistoryIngester(store, policy, clock, input, output); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryIngesterRetentionOrderStateAndClosure(t *testing.T) {
	store := defaultHistoryStore(t)
	policy := &historyPolicy{actions: [model.MaxHistoryChunkRecords]model.RetentionAction{model.KeepBody, model.KeepMetadata, model.Discard}}
	input := newChunkQueue(t, 2)
	output := newMessageQueue(t, 4)
	chunk := mustChunk(t, historyMessage(t, "a", 0), historyMessage(t, "b", 1), historyMessage(t, "c", 2))
	putRawChunk(t, input, chunk)
	input.Close()
	ingester, _ := newHistoryIngester(store, policy, historyClock{writerTestTime}, input, output)
	if err := ingester.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, _ := output.TryTake()
	second, _ := output.TryTake()
	if first.Value().MessageID().String() != "a" || first.Value().Text() == "" || second.Value().MessageID().String() != "b" || second.Value().BodyRetained() {
		t.Fatalf("outputs=%q/%q", first.Value().MessageID().String(), second.Value().MessageID().String())
	}
	_ = first.Release()
	_ = second.Release()
	if policy.count != 3 || store.snapshotCalls != 3 || store.usageCalls != 3 || policy.states[0].Origin != model.WriteHistory || !policy.states[0].Now.Equal(writerTestTime) {
		t.Fatalf("calls/state policy=%d snapshot=%d usage=%d state=%+v", policy.count, store.snapshotCalls, store.usageCalls, policy.states[0])
	}
	if !output.Stats().Stopped || input.budget.usedBytes() != 0 {
		t.Fatalf("output/input state=%+v/%d", output.Stats(), input.budget.usedBytes())
	}
}

func TestCountNewerBodiesOrdering(t *testing.T) {
	candidate := historyMessage(t, "m", 10)
	messages := []model.Message{
		historyMessage(t, "new-time", 11),
		historyMessageAt(t, "z", candidate.SentAt()),
		historyMessageAt(t, "a", candidate.SentAt()),
		historyMessageAt(t, "m", candidate.SentAt()),
		historyMessage(t, "old", 9),
		historyMessage(t, "bodyless", 12).WithoutBody(),
	}
	summaries := make([]model.MessageSummary, len(messages))
	for index, message := range messages {
		summaries[index] = model.NewMessageSummary(message)
	}
	snapshot, _ := model.NewRetentionSnapshot(candidate.ChatID(), summaries, nil)
	if count := countNewerBodies(snapshot, candidate); count != 2 {
		t.Fatalf("newer bodies=%d", count)
	}
}

func TestCountNewerBodiesMaximumSnapshot(t *testing.T) {
	candidate := historyMessage(t, "candidate", 0)
	summaries := make([]model.MessageSummary, model.MaxRetentionSnapshotSummaries)
	for index := range summaries {
		message := historyMessage(t, "summary-"+strconv.Itoa(index), index+1)
		summaries[index] = model.NewMessageSummary(message)
	}
	snapshot, err := model.NewRetentionSnapshot(candidate.ChatID(), summaries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := countNewerBodies(snapshot, candidate); got != model.MaxRetentionSnapshotSummaries {
		t.Fatalf("newer bodies=%d", got)
	}
}

func TestHistoryIngesterBackpressureKeepsChunkChargedAndCancellation(t *testing.T) {
	store := defaultHistoryStore(t)
	policy := &historyPolicy{}
	input := newChunkQueue(t, 2)
	output := newMessageQueue(t, 1)
	putMessage(t, output, "occupied", 0)
	putRawChunk(t, input, mustChunk(t, historyMessage(t, "pending", 0)))
	ingester, _ := newHistoryIngester(store, policy, historyClock{writerTestTime}, input, output)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ingester.Run(ctx) }()
	waitQueueStats(t, output, func(stats queueStats) bool { return stats.EntryWaiters == 1 })
	if input.budget.usedBytes() == 0 {
		t.Fatal("chunk released while downstream blocked")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	if input.budget.usedBytes() != 0 || output.budget.usedBytes() == 0 || !output.Stats().Stopped {
		t.Fatalf("budgets input=%d output=%d", input.budget.usedBytes(), output.budget.usedBytes())
	}
	output.drainAndRelease()
}

func TestHistoryIngesterFailuresDrainAndPreserveError(t *testing.T) {
	for _, failureAt := range []string{"snapshot", "usage", "policy"} {
		t.Run(failureAt, func(t *testing.T) {
			failure := errors.New("history ingester failure")
			store := defaultHistoryStore(t)
			policy := &historyPolicy{}
			if failureAt == "snapshot" {
				store.snapshotErr = failure
			}
			if failureAt == "usage" {
				store.usageErr = failure
			}
			if failureAt == "policy" {
				policy.fail = failure
			}
			input := newChunkQueue(t, 2)
			output := newMessageQueue(t, 2)
			putRawChunk(t, input, mustChunk(t, historyMessage(t, "first", 0), historyMessage(t, "later", 1)))
			putRawChunk(t, input, mustChunk(t, historyMessage(t, "tail", 2)))
			input.Close()
			ingester, _ := newHistoryIngester(store, policy, historyClock{writerTestTime}, input, output)
			if err := ingester.Run(context.Background()); !errors.Is(err, failure) {
				t.Fatalf("Run=%v", err)
			}
			if input.budget.usedBytes() != 0 || !output.Stats().Stopped {
				t.Fatalf("cleanup input=%d output=%+v", input.budget.usedBytes(), output.Stats())
			}
		})
	}
}

func defaultHistoryStore(t *testing.T) *historyStore {
	t.Helper()
	id, _ := model.NewChatID("history-chat")
	snapshot, _ := model.NewRetentionSnapshot(id, nil, nil)
	usage, _ := model.NewCacheUsage(0, 0, 0, 0, 0)
	return &historyStore{snapshot: snapshot, usage: usage}
}
func newChunkQueue(t *testing.T, entries int) *boundedQueue[model.HistoryChunk] {
	t.Helper()
	budget := mustBudget(t, 1<<20)
	queue, err := newBoundedQueue(entries, budget, func(chunk model.HistoryChunk) (int64, error) {
		if chunk.ByteSize() <= 0 {
			return 1, nil
		}
		return int64(chunk.ByteSize()), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}
func newMessageQueue(t *testing.T, entries int) *boundedQueue[model.Message] {
	t.Helper()
	budget := mustBudget(t, 1<<20)
	queue, err := newBoundedQueue(entries, budget, func(message model.Message) (int64, error) {
		if message.ByteSize() <= 0 {
			return 1, nil
		}
		return int64(message.ByteSize()), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}
func mustHistoryJob(t *testing.T, id string, class model.HistoryClass) model.HistoryJob {
	t.Helper()
	job, err := model.NewHistoryJob(id, class)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func historyMessage(t *testing.T, id string, offset int) model.Message {
	return historyMessageAt(t, id, writerTestTime.Add(time.Duration(offset)*time.Second))
}
func historyMessageAt(t *testing.T, id string, sent time.Time) model.Message {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{ChatID: "history-chat", MessageID: id, SentAt: sent, Text: "body"})
	if err != nil {
		t.Fatal(err)
	}
	return message
}
func mustChunk(t *testing.T, messages ...model.Message) model.HistoryChunk {
	t.Helper()
	chunk, err := model.NewHistoryChunk(messages)
	if err != nil {
		t.Fatal(err)
	}
	return chunk
}
func putRawChunk(t *testing.T, queue *boundedQueue[model.HistoryChunk], chunk model.HistoryChunk) {
	t.Helper()
	if err := queue.TryPut(chunk); err != nil {
		t.Fatal(err)
	}
}
func drainChunkQueueUntilDone(t *testing.T, queue *boundedQueue[model.HistoryChunk], done <-chan error) {
	t.Helper()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			queue.drainAndRelease()
			return
		case <-queue.notEmptyChanges():
			if owned, ok := queue.TryTake(); ok {
				_ = owned.Release()
			}
		}
	}
}
func jobIDs(jobs []model.HistoryJob) []string {
	result := make([]string, len(jobs))
	for index, job := range jobs {
		result[index] = job.ID().String()
	}
	return result
}

func releaseCount(jobs []model.HistoryJob, target model.HistoryJob) int {
	count := 0
	for _, job := range jobs {
		if job.ID().String() == target.ID().String() {
			count++
		}
	}
	return count
}
