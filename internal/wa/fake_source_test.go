package wa

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

var testStart = time.Unix(1_700_000_000, 0).UTC()

func TestNewHistorySpecValidatesAndNormalizes(t *testing.T) {
	classes := []model.HistoryClass{
		model.HistoryMetadata,
		model.HistoryOnDemand,
		model.HistoryBulk,
		model.HistoryUnknown,
	}
	for _, class := range classes {
		class := class
		t.Run(strconv.Itoa(int(class)), func(t *testing.T) {
			count := maxHistorySpecRecords
			prefix := strings.Repeat("x", model.MaxRetainedTextBytes+1)
			if class == model.HistoryMetadata {
				count = 0
				prefix = ""
			}

			spec := mustHistorySpec(t, "history-valid", class, count, prefix)
			if spec.job.Class() != class || spec.count != count {
				t.Fatalf("spec = (%v, %d), want (%v, %d)", spec.job.Class(), spec.count, class, count)
			}
			if len(spec.textPrefix) > model.MaxRetainedTextBytes {
				t.Fatalf("prefix length = %d, want <= %d", len(spec.textPrefix), model.MaxRetainedTextBytes)
			}
			if !utf8.ValidString(spec.textPrefix) {
				t.Fatal("normalized prefix is not valid UTF-8")
			}
		})
	}

	job := mustHistoryJob(t, "history-malformed", model.HistoryBulk)
	chat := mustChatID(t, "chat-a")
	spec, err := NewHistorySpec(job, 1, chat, testStart, string([]byte{'a', 0xff, 'b'}))
	if err != nil {
		t.Fatalf("NewHistorySpec malformed prefix: %v", err)
	}
	if !utf8.ValidString(spec.textPrefix) || !strings.ContainsRune(spec.textPrefix, utf8.RuneError) {
		t.Fatalf("normalized malformed prefix = %q", spec.textPrefix)
	}
}

func TestNewHistorySpecRejectsInvalidStateWithoutDisclosure(t *testing.T) {
	metadata := mustHistoryJob(t, "history-private-marker", model.HistoryMetadata)
	onDemand := mustHistoryJob(t, "history-on-demand", model.HistoryOnDemand)
	chat := mustChatID(t, "chat-a")

	tests := []struct {
		name   string
		job    model.HistoryJob
		count  int
		chat   model.ChatID
		start  time.Time
		prefix string
	}{
		{name: "zero job", chat: chat, start: testStart},
		{name: "zero chat", job: onDemand, start: testStart},
		{name: "zero time", job: onDemand, chat: chat},
		{name: "metadata count", job: metadata, count: 1, chat: chat, start: testStart},
		{name: "metadata prefix", job: metadata, chat: chat, start: testStart, prefix: "private-prefix-marker"},
		{name: "negative count", job: onDemand, count: -1, chat: chat, start: testStart},
		{name: "excessive count", job: onDemand, count: maxHistorySpecRecords + 1, chat: chat, start: testStart},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewHistorySpec(test.job, test.count, test.chat, test.start, test.prefix)
			requireSourceFailure(t, err, model.SourceRejected)
			for _, private := range []string{"history-private-marker", "private-prefix-marker"} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("error disclosed private input %q: %v", private, err)
				}
			}
		})
	}
}

func TestFakeSourceOwnsExactChannelCapacities(t *testing.T) {
	source := mustFakeSource(t, nil)
	if got := cap(source.RealtimeEvents()); got != 1 {
		t.Fatalf("realtime capacity = %d, want 1", got)
	}
	if got := cap(source.MetadataHistoryJobs()); got != 2 {
		t.Fatalf("metadata capacity = %d, want 2", got)
	}
	if got := cap(source.OnDemandHistoryJobs()); got != 1 {
		t.Fatalf("on-demand capacity = %d, want 1", got)
	}
	if got := cap(source.BulkHistoryJobs()); got != 1 {
		t.Fatalf("bulk capacity = %d, want 1", got)
	}
	if got := cap(source.StatusWake()); got != 1 {
		t.Fatalf("status capacity = %d, want 1", got)
	}
}

func TestTryRealtimeIsNonblockingAndBounded(t *testing.T) {
	source := mustFakeSource(t, nil)
	first := mustEvent(t, "message-a", "text-a")
	second := mustEvent(t, "message-b", "text-b")

	if err := source.TryRealtime(first); err != nil {
		t.Fatalf("first TryRealtime: %v", err)
	}
	requireSourceFailure(t, source.TryRealtime(second), model.SourceFull)

	got := mustReceive(t, source.RealtimeEvents())
	if got.Message().MessageID().String() != "message-a" || got.Message().Text() != "text-a" {
		t.Fatalf("queued event = (%q, %q), want first event", got.Message().MessageID().String(), got.Message().Text())
	}
	status := source.Status()
	if !status.Degraded() {
		t.Fatal("full realtime slot did not mark status degraded")
	}
	if got := len(source.StatusWake()); got != 1 {
		t.Fatalf("status wake length = %d, want 1", got)
	}

	requireSourceFailure(t, source.TryRealtime(model.Event{}), model.SourceRejected)
}

func TestTryRealtimeAfterClosureReturnsClosed(t *testing.T) {
	source := mustFakeSource(t, nil)
	if err := source.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	requireSourceFailure(t, source.TryRealtime(model.Event{}), model.SourceClosed)
}

func TestHistoryCategoryAdmissionBoundsAndIsolation(t *testing.T) {
	source := mustFakeSource(t, nil)
	metadataA := mustHistorySpec(t, "metadata-a", model.HistoryMetadata, 0, "")
	metadataB := mustHistorySpec(t, "metadata-b", model.HistoryMetadata, 0, "")
	onDemand := mustHistorySpec(t, "on-demand-a", model.HistoryOnDemand, 1, "on-demand-")
	bulk := mustHistorySpec(t, "bulk-a", model.HistoryBulk, 1, "bulk-")

	for _, spec := range []HistorySpec{metadataA, metadataB, onDemand, bulk} {
		if err := source.TryHistory(spec); err != nil {
			t.Fatalf("TryHistory(%q): %v", spec.job.ID().String(), err)
		}
	}
	if source.categoryTokens != [3]uint8{2, 1, 1} {
		t.Fatalf("category tokens = %v, want [2 1 1]", source.categoryTokens)
	}
	if got := activeCursorCount(source); got != 4 {
		t.Fatalf("active cursors = %d, want 4", got)
	}

	requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "metadata-c", model.HistoryMetadata, 0, "")), model.SourceFull)
	requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "on-demand-b", model.HistoryOnDemand, 1, "next-")), model.SourceFull)
	requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "bulk-b", model.HistoryBulk, 1, "next-")), model.SourceFull)

	metadataJobs := []model.HistoryJob{
		mustReceive(t, source.MetadataHistoryJobs()),
		mustReceive(t, source.MetadataHistoryJobs()),
	}
	onDemandJob := mustReceive(t, source.OnDemandHistoryJobs())
	bulkJob := mustReceive(t, source.BulkHistoryJobs())

	// Draining an output does not return its admission token.
	requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "bulk-c", model.HistoryBulk, 1, "next-")), model.SourceFull)

	for _, job := range metadataJobs {
		source.ReleaseHistory(job)
	}
	source.ReleaseHistory(onDemandJob)
	source.ReleaseHistory(bulkJob)
	if source.categoryTokens != [3]uint8{} || activeCursorCount(source) != 0 {
		t.Fatalf("state after releases: tokens=%v cursors=%d", source.categoryTokens, activeCursorCount(source))
	}
}

func TestHistoryExactCategoryCapacities(t *testing.T) {
	tests := []struct {
		name     string
		class    model.HistoryClass
		capacity int
	}{
		{name: "metadata", class: model.HistoryMetadata, capacity: 2},
		{name: "on-demand", class: model.HistoryOnDemand, capacity: 1},
		{name: "bulk", class: model.HistoryBulk, capacity: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := mustFakeSource(t, nil)
			for index := 0; index < test.capacity; index++ {
				spec := mustHistorySpec(t, test.name+"-"+strconv.Itoa(index), test.class, historyCount(test.class), historyPrefix(test.class))
				if err := source.TryHistory(spec); err != nil {
					t.Fatalf("admission %d: %v", index, err)
				}
			}
			requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, test.name+"-full", test.class, historyCount(test.class), historyPrefix(test.class))), model.SourceFull)
			if got := len(historyOutput(source, test.class)); got != test.capacity {
				t.Fatalf("output length = %d, want %d", got, test.capacity)
			}
		})
	}
}

func TestDeliveredHistoryTokenPersistsAndFullFailureDoesNotConsumeID(t *testing.T) {
	source := mustFakeSource(t, nil)
	first := mustHistorySpec(t, "history-first", model.HistoryOnDemand, 1, "first-")
	retry := mustHistorySpec(t, "history-retry", model.HistoryOnDemand, 1, "retry-")

	if err := source.TryHistory(first); err != nil {
		t.Fatalf("admit first: %v", err)
	}
	firstJob := mustReceive(t, source.OnDemandHistoryJobs())
	requireSourceFailure(t, source.TryHistory(retry), model.SourceFull)
	if source.hasLifetimeIDLocked(retry.job.ID().String()) {
		t.Fatal("full admission consumed the rejected ID")
	}

	source.ReleaseHistory(firstJob)
	if err := source.TryHistory(retry); err != nil {
		t.Fatalf("retry after release: %v", err)
	}
	retryJob := mustReceive(t, source.OnDemandHistoryJobs())
	source.ReleaseHistory(retryJob)
}

func TestUnknownHistoryRejectsDegradesAndDoesNotConsumeID(t *testing.T) {
	source := mustFakeSource(t, nil)
	unknown := mustHistorySpec(t, "history-shared", model.HistoryUnknown, 1, "unknown-")
	requireSourceFailure(t, source.TryHistory(unknown), model.SourceRejected)
	if source.lifetimeIDCount != 0 || activeCursorCount(source) != 0 {
		t.Fatalf("unknown admission retained identity/cursor: IDs=%d cursors=%d", source.lifetimeIDCount, activeCursorCount(source))
	}
	status := source.Status()
	if status.UnknownDropped() != 1 || !status.Degraded() {
		t.Fatalf("unknown status = dropped %d degraded %t", status.UnknownDropped(), status.Degraded())
	}

	known := mustHistorySpec(t, "history-shared", model.HistoryBulk, 1, "known-")
	if err := source.TryHistory(known); err != nil {
		t.Fatalf("known reuse after unknown rejection: %v", err)
	}
}

func TestHistoryIDIsUniqueForSourceLifetime(t *testing.T) {
	t.Run("duplicate active and released", func(t *testing.T) {
		source := mustFakeSource(t, nil)
		spec := mustHistorySpec(t, "history-duplicate", model.HistoryOnDemand, 1, "item-")
		if err := source.TryHistory(spec); err != nil {
			t.Fatalf("first admission: %v", err)
		}
		requireSourceFailure(t, source.TryHistory(spec), model.SourceRejected)
		job := mustReceive(t, source.OnDemandHistoryJobs())
		source.ReleaseHistory(job)
		requireSourceFailure(t, source.TryHistory(spec), model.SourceRejected)
	})

	t.Run("exact 256", func(t *testing.T) {
		source := mustFakeSource(t, nil)
		for index := 0; index < maxLifetimeHistoryIDs; index++ {
			spec := mustHistorySpec(t, "history-"+strconv.Itoa(index), model.HistoryMetadata, 0, "")
			if err := source.TryHistory(spec); err != nil {
				t.Fatalf("admission %d: %v", index, err)
			}
			job := mustReceive(t, source.MetadataHistoryJobs())
			source.ReleaseHistory(job)
		}
		if source.lifetimeIDCount != maxLifetimeHistoryIDs {
			t.Fatalf("lifetime ID count = %d, want %d", source.lifetimeIDCount, maxLifetimeHistoryIDs)
		}
		requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "history-257", model.HistoryMetadata, 0, "")), model.SourceRejected)
		if source.lifetimeIDCount != maxLifetimeHistoryIDs {
			t.Fatalf("rejection changed lifetime ID count to %d", source.lifetimeIDCount)
		}
	})
}

func TestSourceStatusCountersCoalesceAndSaturate(t *testing.T) {
	t.Run("category counters and coalescing", func(t *testing.T) {
		source := mustFakeSource(t, nil)
		if status := source.Status(); status.Degraded() || status.MetadataDropped() != 0 || status.OnDemandDropped() != 0 || status.BulkDropped() != 0 || status.UnknownDropped() != 0 {
			t.Fatalf("initial status is not empty: %#v", status)
		}

		fillHistoryCategory(t, source, model.HistoryMetadata)
		fillHistoryCategory(t, source, model.HistoryOnDemand)
		fillHistoryCategory(t, source, model.HistoryBulk)
		requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "metadata-drop", model.HistoryMetadata, 0, "")), model.SourceFull)
		requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "on-demand-drop", model.HistoryOnDemand, 1, "drop-")), model.SourceFull)
		requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "bulk-drop", model.HistoryBulk, 1, "drop-")), model.SourceFull)
		unknown := mustHistorySpec(t, "unknown-drop", model.HistoryUnknown, 1, "drop-")
		for range 3 {
			requireSourceFailure(t, source.TryHistory(unknown), model.SourceRejected)
		}

		status := source.Status()
		if status.MetadataDropped() != 1 || status.OnDemandDropped() != 1 || status.BulkDropped() != 1 || status.UnknownDropped() != 3 || !status.Degraded() {
			t.Fatalf("status = (%d, %d, %d, %d, %t)", status.MetadataDropped(), status.OnDemandDropped(), status.BulkDropped(), status.UnknownDropped(), status.Degraded())
		}
		if len(source.StatusWake()) != 1 {
			t.Fatalf("coalesced wake length = %d, want 1", len(source.StatusWake()))
		}
		mustReceive(t, source.StatusWake())
		requireNoValue(t, source.StatusWake())
	})

	tests := []struct {
		name  string
		class model.HistoryClass
		set   func(*sourceStatus)
		read  func(model.SourceStatus) uint64
	}{
		{name: "metadata", class: model.HistoryMetadata, set: func(status *sourceStatus) { status.metadataDropped = math.MaxUint64 }, read: model.SourceStatus.MetadataDropped},
		{name: "on-demand", class: model.HistoryOnDemand, set: func(status *sourceStatus) { status.onDemandDropped = math.MaxUint64 }, read: model.SourceStatus.OnDemandDropped},
		{name: "bulk", class: model.HistoryBulk, set: func(status *sourceStatus) { status.bulkDropped = math.MaxUint64 }, read: model.SourceStatus.BulkDropped},
		{name: "unknown", class: model.HistoryUnknown, set: func(status *sourceStatus) { status.unknownDropped = math.MaxUint64 }, read: model.SourceStatus.UnknownDropped},
	}
	for _, test := range tests {
		t.Run("saturates "+test.name, func(t *testing.T) {
			source := mustFakeSource(t, nil)
			if test.class != model.HistoryUnknown {
				fillHistoryCategory(t, source, test.class)
			}
			source.mu.Lock()
			test.set(&source.status)
			source.mu.Unlock()
			spec := mustHistorySpec(t, "saturation-"+test.name, test.class, historyCount(test.class), historyPrefix(test.class))
			err := source.TryHistory(spec)
			if test.class == model.HistoryUnknown {
				requireSourceFailure(t, err, model.SourceRejected)
			} else {
				requireSourceFailure(t, err, model.SourceFull)
			}
			if got := test.read(source.Status()); got != math.MaxUint64 {
				t.Fatalf("counter = %d, want MaxUint64", got)
			}
		})
	}
}

func TestNewFakeSourceScriptEntryBounds(t *testing.T) {
	steps := make([]ScriptStep, maxScriptSteps)
	for index := range steps {
		steps[index] = NewBarrierStep(nil)
	}
	if _, err := NewFakeSource(steps); err != nil {
		t.Fatalf("exact %d steps: %v", maxScriptSteps, err)
	}
	steps = append(steps, NewBarrierStep(nil))
	_, err := NewFakeSource(steps)
	requireSourceFailure(t, err, model.SourceRejected)
}

func TestNewFakeSourceScriptByteBounds(t *testing.T) {
	exact := scriptWithCharge(t, maxScriptBytes)
	source := mustFakeSource(t, exact)
	if source.scriptByteSize != maxScriptBytes {
		t.Fatalf("script charge = %d, want %d", source.scriptByteSize, maxScriptBytes)
	}

	over := scriptWithCharge(t, maxScriptBytes+1)
	_, err := NewFakeSource(over)
	requireSourceFailure(t, err, model.SourceRejected)
}

func TestNewFakeSourceDefensivelyCopiesScript(t *testing.T) {
	event := mustEvent(t, "message-copy", "copy-text")
	steps := []ScriptStep{NewRealtimeStep(event, nil)}
	source := mustFakeSource(t, steps)
	steps[0] = NewBarrierStep(make(chan struct{}))

	if err := source.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := mustReceive(t, source.RealtimeEvents())
	if got.Message().MessageID().String() != "message-copy" || got.Message().Text() != "copy-text" {
		t.Fatalf("copied event = (%q, %q)", got.Message().MessageID().String(), got.Message().Text())
	}
}

func TestRunClearsProcessedScriptReferences(t *testing.T) {
	secondGate := make(chan struct{})
	finishGate := make(chan struct{})
	steps := []ScriptStep{
		NewRealtimeStep(mustEvent(t, "message-first", "first-private-reference"), nil),
		NewRealtimeStep(mustEvent(t, "message-second", "second-text"), secondGate),
		NewBarrierStep(finishGate),
	}
	source := mustFakeSource(t, steps)
	runResult := make(chan error, 1)
	go func() { runResult <- source.Run(context.Background()) }()

	mustReceive(t, source.RealtimeEvents())
	close(secondGate)
	mustReceive(t, source.RealtimeEvents())

	source.mu.Lock()
	cleared := source.script[0]
	source.mu.Unlock()
	if cleared.kind != 0 || cleared.event.ByteSize() != 0 || cleared.gate != nil {
		t.Fatalf("processed script step still retains state: %#v", cleared)
	}

	close(finishGate)
	if err := mustReceive(t, runResult); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestScriptBarrierCancellationAndNilGate(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		gate := make(chan struct{})
		source := mustFakeSource(t, []ScriptStep{
			NewRealtimeStep(mustEvent(t, "message-marker", "marker"), nil),
			NewBarrierStep(gate),
		})
		ctx, cancel := context.WithCancel(context.Background())
		runResult := make(chan error, 1)
		go func() { runResult <- source.Run(ctx) }()
		mustReceive(t, source.RealtimeEvents())
		cancel()
		if err := mustReceive(t, runResult); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
		requireClosed(t, source.RealtimeEvents())
	})

	t.Run("nil gate", func(t *testing.T) {
		source := mustFakeSource(t, []ScriptStep{
			NewBarrierStep(nil),
			NewRealtimeStep(mustEvent(t, "message-immediate", "immediate"), nil),
		})
		if err := source.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := mustReceive(t, source.RealtimeEvents()); got.Message().MessageID().String() != "message-immediate" {
			t.Fatalf("message ID = %q", got.Message().MessageID().String())
		}
	})
}

func TestScriptUsesBoundedAdmissionWithoutRetry(t *testing.T) {
	t.Run("realtime", func(t *testing.T) {
		source := mustFakeSource(t, []ScriptStep{
			NewRealtimeStep(mustEvent(t, "message-a", "a"), nil),
			NewRealtimeStep(mustEvent(t, "message-b", "b"), nil),
		})
		if err := source.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		got := mustReceive(t, source.RealtimeEvents())
		if got.Message().MessageID().String() != "message-a" {
			t.Fatalf("retained message = %q, want message-a", got.Message().MessageID().String())
		}
		if !source.Status().Degraded() {
			t.Fatal("scripted realtime shedding did not degrade status")
		}
	})

	t.Run("history", func(t *testing.T) {
		first := mustHistorySpec(t, "bulk-script-a", model.HistoryBulk, 1, "a-")
		second := mustHistorySpec(t, "bulk-script-b", model.HistoryBulk, 1, "b-")
		source := mustFakeSource(t, []ScriptStep{NewHistoryStep(first, nil), NewHistoryStep(second, nil)})
		if err := source.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		job := mustReceive(t, source.BulkHistoryJobs())
		if job.ID().String() != first.job.ID().String() {
			t.Fatalf("retained job = %q, want %q", job.ID().String(), first.job.ID().String())
		}
		if got := source.Status().BulkDropped(); got != 1 {
			t.Fatalf("bulk dropped = %d, want 1", got)
		}
		source.ReleaseHistory(job)
	})
}

func TestNextHistoryIsLazyAndHonorsRecordLimit(t *testing.T) {
	source := mustFakeSource(t, nil)
	spec := mustHistorySpec(t, "history-records", model.HistoryBulk, 33, "record-")
	job := admitAndReceive(t, source, spec)
	if got := cursorIndexFor(t, source, job); got != 0 {
		t.Fatalf("cursor index before traversal = %d, want 0", got)
	}
	assertHistorySpecHasNoCollection(t)

	first, done, err := source.NextHistory(context.Background(), job, math.MaxInt, math.MaxInt64)
	if err != nil {
		t.Fatalf("first NextHistory: %v", err)
	}
	if first.Len() != model.MaxHistoryChunkRecords || done {
		t.Fatalf("first chunk = len %d done %t", first.Len(), done)
	}
	second, done, err := source.NextHistory(context.Background(), job, model.MaxHistoryChunkRecords, model.MaxHistoryChunkBytes)
	if err != nil {
		t.Fatalf("second NextHistory: %v", err)
	}
	if second.Len() != 1 || !done {
		t.Fatalf("second chunk = len %d done %t", second.Len(), done)
	}
	message, ok := second.At(0)
	if !ok || message.MessageID().String() != "history-records-32" || message.Text() != "record-32" {
		t.Fatalf("final message = (%q, %q, %t)", message.MessageID().String(), message.Text(), ok)
	}
	source.ReleaseHistory(job)
}

func TestNextHistoryHonorsByteLimit(t *testing.T) {
	source := mustFakeSource(t, nil)
	prefix := strings.Repeat("x", model.MaxRetainedTextBytes)
	spec := mustHistorySpec(t, "history-bytes", model.HistoryBulk, 3, prefix)
	job := admitAndReceive(t, source, spec)
	const byteLimit = int64(17 * 1024)

	for index := 0; index < 3; index++ {
		chunk, done, err := source.NextHistory(context.Background(), job, model.MaxHistoryChunkRecords, byteLimit)
		if err != nil {
			t.Fatalf("NextHistory %d: %v", index, err)
		}
		if chunk.Len() != 1 || int64(chunk.ByteSize()) > byteLimit {
			t.Fatalf("chunk %d = len %d bytes %d", index, chunk.Len(), chunk.ByteSize())
		}
		if done != (index == 2) {
			t.Fatalf("chunk %d done = %t", index, done)
		}
	}
	source.ReleaseHistory(job)
}

func TestNextHistoryGeneratedTimestampsRemainNonzero(t *testing.T) {
	job := mustHistoryJob(t, "history-time", model.HistoryBulk)
	spec, err := NewHistorySpec(
		job,
		2,
		mustChatID(t, "chat-a"),
		time.Time{}.Add(-time.Second),
		"time-",
	)
	if err != nil {
		t.Fatalf("NewHistorySpec: %v", err)
	}
	source := mustFakeSource(t, nil)
	delivered := admitAndReceive(t, source, spec)
	chunk, done, err := source.NextHistory(
		context.Background(),
		delivered,
		2,
		model.MaxHistoryChunkBytes,
	)
	if err != nil || !done || chunk.Len() != 2 {
		t.Fatalf("NextHistory = len %d done %t err %v", chunk.Len(), done, err)
	}
	for index := 0; index < chunk.Len(); index++ {
		message, ok := chunk.At(index)
		if !ok || message.SentAt().IsZero() {
			t.Fatalf("generated message %d has zero timestamp", index)
		}
	}
	source.ReleaseHistory(delivered)
}

func TestNextHistoryProducesEveryRecordAtMostOnce(t *testing.T) {
	source := mustFakeSource(t, nil)
	spec := mustHistorySpec(t, "history-once", model.HistoryBulk, 65, "value-")
	job := admitAndReceive(t, source, spec)
	seen := make(map[string]bool, 65)
	total := 0
	for {
		chunk, done, err := source.NextHistory(context.Background(), job, 7, model.MaxHistoryChunkBytes)
		if err != nil {
			t.Fatalf("NextHistory: %v", err)
		}
		for index := 0; index < chunk.Len(); index++ {
			message, ok := chunk.At(index)
			if !ok {
				t.Fatalf("At(%d) failed", index)
			}
			identifier := message.MessageID().String()
			if seen[identifier] {
				t.Fatalf("duplicate generated ID %q", identifier)
			}
			seen[identifier] = true
			total++
		}
		if done {
			if chunk.Len() == 0 {
				t.Fatal("final traversal chunk was unexpectedly empty")
			}
			break
		}
	}
	if total != 65 {
		t.Fatalf("generated records = %d, want 65", total)
	}
	for index := 0; index < 65; index++ {
		if !seen["history-once-"+strconv.Itoa(index)] {
			t.Fatalf("missing generated record %d", index)
		}
	}

	empty, done, err := source.NextHistory(context.Background(), job, 1, model.MaxHistoryChunkBytes)
	if err != nil || empty.Len() != 0 || !done {
		t.Fatalf("completed cursor = len %d done %t err %v", empty.Len(), done, err)
	}
	source.ReleaseHistory(job)
}

func TestConcurrentNextHistoryDoesNotDuplicateRecords(t *testing.T) {
	source := mustFakeSource(t, nil)
	job := admitAndReceive(t, source, mustHistorySpec(t, "history-concurrent", model.HistoryBulk, 64, "item-"))
	start := make(chan struct{})
	results := make(chan traversalResult, 2)
	for range 2 {
		go func() {
			<-start
			chunk, done, err := source.NextHistory(context.Background(), job, model.MaxHistoryChunkRecords, model.MaxHistoryChunkBytes)
			results <- traversalResult{chunk: chunk, done: done, err: err}
		}()
	}
	close(start)

	seen := make(map[string]bool, 64)
	doneCount := 0
	for range 2 {
		result := mustReceive(t, results)
		if result.err != nil {
			t.Fatalf("NextHistory: %v", result.err)
		}
		if result.done {
			doneCount++
		}
		for index := 0; index < result.chunk.Len(); index++ {
			message, _ := result.chunk.At(index)
			identifier := message.MessageID().String()
			if seen[identifier] {
				t.Fatalf("duplicate concurrent record %q", identifier)
			}
			seen[identifier] = true
		}
	}
	if len(seen) != 64 || doneCount != 1 {
		t.Fatalf("concurrent traversal = %d records, %d done results", len(seen), doneCount)
	}
	source.ReleaseHistory(job)
}

func TestNextHistoryCancellationAndBoundsDoNotAdvanceCursor(t *testing.T) {
	source := mustFakeSource(t, nil)
	job := admitAndReceive(t, source, mustHistorySpec(t, "history-cancel", model.HistoryBulk, 2, "item-"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := source.NextHistory(ctx, job, 1, model.MaxHistoryChunkBytes)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled traversal error = %v", err)
	}
	if got := cursorIndexFor(t, source, job); got != 0 {
		t.Fatalf("cursor advanced to %d after cancellation", got)
	}

	invalidBounds := []struct {
		records int
		bytes   int64
	}{
		{records: 0, bytes: 1},
		{records: -1, bytes: 1},
		{records: 1, bytes: 0},
		{records: 1, bytes: -1},
		{records: 1, bytes: 1},
	}
	for _, bounds := range invalidBounds {
		_, _, err := source.NextHistory(context.Background(), job, bounds.records, bounds.bytes)
		requireSourceFailure(t, err, model.SourceRejected)
		if got := cursorIndexFor(t, source, job); got != 0 {
			t.Fatalf("cursor advanced to %d for bounds %+v", got, bounds)
		}
	}

	chunk, _, err := source.NextHistory(context.Background(), job, 1, model.MaxHistoryChunkBytes)
	if err != nil {
		t.Fatalf("retry traversal: %v", err)
	}
	message, _ := chunk.At(0)
	if message.MessageID().String() != "history-cancel-0" {
		t.Fatalf("first ID after cancellation = %q", message.MessageID().String())
	}
	source.ReleaseHistory(job)
}

func TestEmptyMetadataHistoryIsDone(t *testing.T) {
	source := mustFakeSource(t, nil)
	job := admitAndReceive(t, source, mustHistorySpec(t, "history-metadata", model.HistoryMetadata, 0, ""))
	chunk, done, err := source.NextHistory(context.Background(), job, 1, model.MaxHistoryChunkBytes)
	if err != nil || chunk.Len() != 0 || !done {
		t.Fatalf("metadata traversal = len %d done %t err %v", chunk.Len(), done, err)
	}
	source.ReleaseHistory(job)
}

func TestHistoryCursorSurvivesRunUntilRelease(t *testing.T) {
	spec := mustHistorySpec(t, "history-after-run", model.HistoryBulk, 1, "after-")
	source := mustFakeSource(t, []ScriptStep{NewHistoryStep(spec, nil)})
	if err := source.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	job := mustReceive(t, source.BulkHistoryJobs())
	chunk, done, err := source.NextHistory(context.Background(), job, 1, model.MaxHistoryChunkBytes)
	if err != nil || chunk.Len() != 1 || !done {
		t.Fatalf("post-run traversal = len %d done %t err %v", chunk.Len(), done, err)
	}
	source.ReleaseHistory(job)
	source.ReleaseHistory(job)
	if source.categoryTokens[2] != 0 || activeCursorCount(source) != 0 {
		t.Fatalf("duplicate release changed state: token=%d cursors=%d", source.categoryTokens[2], activeCursorCount(source))
	}
	_, _, err = source.NextHistory(context.Background(), job, 1, model.MaxHistoryChunkBytes)
	requireSourceFailure(t, err, model.SourceRejected)
}

func TestDuplicateReleaseDoesNotFreeAnotherToken(t *testing.T) {
	source := mustFakeSource(t, nil)
	first := mustHistorySpec(t, "release-first", model.HistoryOnDemand, 1, "first-")
	firstJob := admitAndReceive(t, source, first)
	source.ReleaseHistory(firstJob)
	source.ReleaseHistory(firstJob)

	second := mustHistorySpec(t, "release-second", model.HistoryOnDemand, 1, "second-")
	secondJob := admitAndReceive(t, source, second)
	third := mustHistorySpec(t, "release-third", model.HistoryOnDemand, 1, "third-")
	requireSourceFailure(t, source.TryHistory(third), model.SourceFull)
	source.ReleaseHistory(firstJob)
	requireSourceFailure(t, source.TryHistory(third), model.SourceFull)

	source.ReleaseHistory(secondJob)
	if err := source.TryHistory(third); err != nil {
		t.Fatalf("third admission after second release: %v", err)
	}
	thirdJob := mustReceive(t, source.OnDemandHistoryJobs())
	source.ReleaseHistory(thirdJob)
}

func TestUnadmittedHistoryTraversalIsRejected(t *testing.T) {
	source := mustFakeSource(t, nil)
	job := mustHistoryJob(t, "history-unadmitted", model.HistoryBulk)
	_, _, err := source.NextHistory(context.Background(), job, 1, model.MaxHistoryChunkBytes)
	requireSourceFailure(t, err, model.SourceRejected)
}

func TestRunIsSingleUseSoleCloser(t *testing.T) {
	source := mustFakeSource(t, nil)
	if err := source.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if source.runState != runFinished || !source.admissionClosed {
		t.Fatalf("finished state = run %d closed %t", source.runState, source.admissionClosed)
	}
	requireClosed(t, source.RealtimeEvents())
	requireClosed(t, source.MetadataHistoryJobs())
	requireClosed(t, source.OnDemandHistoryJobs())
	requireClosed(t, source.BulkHistoryJobs())
	requireClosed(t, source.StatusWake())
	requireSourceFailure(t, source.Run(context.Background()), model.SourceRejected)
	requireSourceFailure(t, source.TryRealtime(mustEvent(t, "message-closed", "closed")), model.SourceClosed)
	requireSourceFailure(t, source.TryHistory(mustHistorySpec(t, "history-closed", model.HistoryMetadata, 0, "")), model.SourceClosed)
}

func TestConcurrentRunIsRejected(t *testing.T) {
	gate := make(chan struct{})
	source := mustFakeSource(t, []ScriptStep{
		NewRealtimeStep(mustEvent(t, "message-run-marker", "marker"), nil),
		NewBarrierStep(gate),
	})
	firstResult := make(chan error, 1)
	go func() { firstResult <- source.Run(context.Background()) }()
	mustReceive(t, source.RealtimeEvents())
	requireSourceFailure(t, source.Run(context.Background()), model.SourceRejected)
	close(gate)
	if err := mustReceive(t, firstResult); err != nil {
		t.Fatalf("first Run: %v", err)
	}
}

func TestRunCancellationReturnsCauseAndClosesOutputs(t *testing.T) {
	gate := make(chan struct{})
	source := mustFakeSource(t, []ScriptStep{
		NewRealtimeStep(mustEvent(t, "message-cause-marker", "marker"), nil),
		NewBarrierStep(gate),
	})
	ctx, cancel := context.WithCancelCause(context.Background())
	result := make(chan error, 1)
	go func() { result <- source.Run(ctx) }()
	mustReceive(t, source.RealtimeEvents())
	cause := errors.New("synthetic cancellation")
	cancel(cause)
	if err := mustReceive(t, result); !errors.Is(err, cause) {
		t.Fatalf("Run error = %v, want cancellation cause", err)
	}
	requireClosed(t, source.RealtimeEvents())
	requireClosed(t, source.MetadataHistoryJobs())
	requireClosed(t, source.OnDemandHistoryJobs())
	requireClosed(t, source.BulkHistoryJobs())
	requireClosed(t, source.StatusWake())
}

func TestConcurrentAdmissionAndShutdownNeverBlocksOrPanics(t *testing.T) {
	start := make(chan struct{})
	source := mustFakeSource(t, []ScriptStep{
		NewRealtimeStep(mustEvent(t, "message-shutdown-marker", "marker"), nil),
		NewBarrierStep(start),
	})
	runResult := make(chan error, 1)
	go func() { runResult <- source.Run(context.Background()) }()
	mustReceive(t, source.RealtimeEvents())

	realtimeResult := make(chan error, 1)
	historyResult := make(chan error, 1)
	realtimeReady := make(chan struct{})
	historyReady := make(chan struct{})
	spec := mustHistorySpec(t, "history-shutdown", model.HistoryBulk, 1, "shutdown-")
	shutdownEvent := mustEvent(t, "message-shutdown", "shutdown")
	go func() {
		close(realtimeReady)
		<-start
		realtimeResult <- source.TryRealtime(shutdownEvent)
	}()
	go func() {
		close(historyReady)
		<-start
		historyResult <- source.TryHistory(spec)
	}()
	<-realtimeReady
	<-historyReady
	close(start)

	realtimeErr := mustReceive(t, realtimeResult)
	historyErr := mustReceive(t, historyResult)
	if realtimeErr != nil {
		requireSourceFailure(t, realtimeErr, model.SourceClosed)
	}
	if historyErr != nil {
		requireSourceFailure(t, historyErr, model.SourceClosed)
	}
	if err := mustReceive(t, runResult); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if realtimeErr == nil {
		mustReceive(t, source.RealtimeEvents())
	}
	if historyErr == nil {
		job := mustReceive(t, source.BulkHistoryJobs())
		chunk, done, err := source.NextHistory(context.Background(), job, 1, model.MaxHistoryChunkBytes)
		if err != nil || chunk.Len() != 1 || !done {
			t.Fatalf("raced cursor traversal = len %d done %t err %v", chunk.Len(), done, err)
		}
		source.ReleaseHistory(job)
	}
	requireClosed(t, source.RealtimeEvents())
	requireClosed(t, source.MetadataHistoryJobs())
	requireClosed(t, source.OnDemandHistoryJobs())
	requireClosed(t, source.BulkHistoryJobs())
	requireClosed(t, source.StatusWake())
}

func TestSourceErrorIsStructuredContentFreeAndUnwrapsFixedCauses(t *testing.T) {
	source := mustFakeSource(t, nil)
	privateID := "history-private-marker"
	spec := mustHistorySpec(t, privateID, model.HistoryOnDemand, 1, "private-text-marker")
	if err := source.TryHistory(spec); err != nil {
		t.Fatalf("first admission: %v", err)
	}
	err := source.TryHistory(spec)
	requireSourceFailure(t, err, model.SourceRejected)
	for _, private := range []string{privateID, "private-text-marker"} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("error disclosed %q: %v", private, err)
		}
	}

	cancelled := newSourceError(operationRun, model.SourceClosed, context.Canceled)
	if !errors.Is(cancelled, context.Canceled) {
		t.Fatalf("fixed context cause was not unwrapped: %v", cancelled)
	}
	privateCause := errors.New("private-cause-marker")
	rejected := newSourceError(operationRun, model.SourceRejected, privateCause)
	if rejected.Unwrap() != nil || strings.Contains(rejected.Error(), "private-cause-marker") {
		t.Fatalf("arbitrary cause retained or rendered: %#v", rejected)
	}
}

type traversalResult struct {
	chunk model.HistoryChunk
	done  bool
	err   error
}

func mustFakeSource(t *testing.T, script []ScriptStep) *FakeSource {
	t.Helper()
	source, err := NewFakeSource(script)
	if err != nil {
		t.Fatalf("NewFakeSource: %v", err)
	}
	return source
}

func mustHistoryJob(t *testing.T, identifier string, class model.HistoryClass) model.HistoryJob {
	t.Helper()
	job, err := model.NewHistoryJob(identifier, class)
	if err != nil {
		t.Fatalf("NewHistoryJob(%q): %v", identifier, err)
	}
	return job
}

func mustChatID(t *testing.T, value string) model.ChatID {
	t.Helper()
	chat, err := model.NewChatID(value)
	if err != nil {
		t.Fatalf("NewChatID(%q): %v", value, err)
	}
	return chat
}

func mustHistorySpec(
	t *testing.T,
	identifier string,
	class model.HistoryClass,
	count int,
	prefix string,
) HistorySpec {
	t.Helper()
	spec, err := NewHistorySpec(
		mustHistoryJob(t, identifier, class),
		count,
		mustChatID(t, "chat-a"),
		testStart,
		prefix,
	)
	if err != nil {
		t.Fatalf("NewHistorySpec(%q): %v", identifier, err)
	}
	return spec
}

func mustEvent(t *testing.T, identifier, text string) model.Event {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{
		ChatID:    "chat-a",
		MessageID: identifier,
		SentAt:    testStart,
		Text:      text,
	})
	if err != nil {
		t.Fatalf("NewMessage(%q): %v", identifier, err)
	}
	event, err := model.NewEvent(message, testStart.Add(time.Second))
	if err != nil {
		t.Fatalf("NewEvent(%q): %v", identifier, err)
	}
	return event
}

func requireSourceFailure(t *testing.T, err error, want model.SourceFailureKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want source failure %d", want)
	}
	var sourceErr *SourceError
	if !errors.As(err, &sourceErr) {
		t.Fatalf("error type = %T, want *SourceError", err)
	}
	if got := sourceErr.SourceFailureKind(); got != want {
		t.Fatalf("source failure kind = %d, want %d", got, want)
	}
	var structural interface {
		SourceFailureKind() model.SourceFailureKind
	}
	if !errors.As(err, &structural) || structural.SourceFailureKind() != want {
		t.Fatalf("structural source classification failed for %v", err)
	}
}

func mustReceive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	value, ok := <-channel
	if !ok {
		t.Fatal("channel closed before expected value")
	}
	return value
}

func requireNoValue[T any](t *testing.T, channel <-chan T) {
	t.Helper()
	select {
	case value, ok := <-channel:
		t.Fatalf("unexpected channel result: value=%v open=%t", value, ok)
	default:
	}
}

func requireClosed[T any](t *testing.T, channel <-chan T) {
	t.Helper()
	for {
		select {
		case _, ok := <-channel:
			if !ok {
				return
			}
		default:
			t.Fatal("channel remains open")
		}
	}
}

func historyOutput(source *FakeSource, class model.HistoryClass) <-chan model.HistoryJob {
	switch class {
	case model.HistoryMetadata:
		return source.MetadataHistoryJobs()
	case model.HistoryOnDemand:
		return source.OnDemandHistoryJobs()
	case model.HistoryBulk:
		return source.BulkHistoryJobs()
	default:
		return nil
	}
}

func historyCount(class model.HistoryClass) int {
	if class == model.HistoryMetadata {
		return 0
	}
	return 1
}

func historyPrefix(class model.HistoryClass) string {
	if class == model.HistoryMetadata {
		return ""
	}
	return "item-"
}

func activeCursorCount(source *FakeSource) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	count := 0
	for _, cursor := range source.cursors {
		if cursor.active {
			count++
		}
	}
	return count
}

func fillHistoryCategory(t *testing.T, source *FakeSource, class model.HistoryClass) {
	t.Helper()
	_, capacity, ok := historyCategory(class)
	if !ok {
		t.Fatalf("no category for class %d", class)
	}
	for index := 0; index < int(capacity); index++ {
		identifier := "fill-" + strconv.Itoa(int(class)) + "-" + strconv.Itoa(index)
		if err := source.TryHistory(mustHistorySpec(t, identifier, class, historyCount(class), historyPrefix(class))); err != nil {
			t.Fatalf("fill category %d: %v", class, err)
		}
	}
}

func scriptWithCharge(t *testing.T, target int64) []ScriptStep {
	t.Helper()
	const stepCount = 32
	base := make([]ScriptStep, stepCount)
	var baseCharge int64
	for index := range base {
		spec := mustHistorySpec(t, "history-script", model.HistoryBulk, 0, "")
		base[index] = NewHistoryStep(spec, nil)
		_, charge, err := normalizeScriptStep(base[index])
		if err != nil {
			t.Fatalf("normalizeScriptStep: %v", err)
		}
		baseCharge += charge
	}
	remaining := target - baseCharge
	if remaining < 0 || remaining > int64(stepCount*model.MaxRetainedTextBytes) {
		t.Fatalf("target %d is not constructible: base=%d capacity=%d", target, baseCharge, stepCount*model.MaxRetainedTextBytes)
	}

	steps := make([]ScriptStep, stepCount)
	for index := range steps {
		prefixBytes := int64(model.MaxRetainedTextBytes)
		if remaining < prefixBytes {
			prefixBytes = remaining
		}
		remaining -= prefixBytes
		spec := mustHistorySpec(t, "history-script", model.HistoryBulk, 0, strings.Repeat("x", int(prefixBytes)))
		steps[index] = NewHistoryStep(spec, nil)
	}
	if remaining != 0 {
		t.Fatalf("unallocated script charge = %d", remaining)
	}
	var total int64
	for _, step := range steps {
		_, charge, err := normalizeScriptStep(step)
		if err != nil {
			t.Fatalf("normalizeScriptStep: %v", err)
		}
		total += charge
	}
	if total != target {
		t.Fatalf("constructed script charge = %d, want %d", total, target)
	}
	return steps
}

func admitAndReceive(t *testing.T, source *FakeSource, spec HistorySpec) model.HistoryJob {
	t.Helper()
	if err := source.TryHistory(spec); err != nil {
		t.Fatalf("TryHistory(%q): %v", spec.job.ID().String(), err)
	}
	return mustReceive(t, historyOutput(source, spec.job.Class()))
}

func cursorIndexFor(t *testing.T, source *FakeSource, job model.HistoryJob) int {
	t.Helper()
	source.mu.Lock()
	defer source.mu.Unlock()
	index := source.cursorForJobLocked(job)
	if index < 0 {
		t.Fatalf("no cursor for %q", job.ID().String())
	}
	return source.cursors[index].index
}

func assertHistorySpecHasNoCollection(t *testing.T) {
	t.Helper()
	typeOfSpec := reflect.TypeOf(HistorySpec{})
	for index := 0; index < typeOfSpec.NumField(); index++ {
		kind := typeOfSpec.Field(index).Type.Kind()
		if kind == reflect.Slice || kind == reflect.Map {
			t.Fatalf("HistorySpec field %q retains an unbounded collection", typeOfSpec.Field(index).Name)
		}
	}
}
