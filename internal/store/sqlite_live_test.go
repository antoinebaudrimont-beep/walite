package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func sqliteLiveBatch(t *testing.T, messages ...model.Message) model.WriteBatch {
	t.Helper()
	batch, err := model.NewWriteBatch(model.WriteRealtime, messages)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestSQLiteRealtimeMatchesMemory(t *testing.T) {
	ctx := context.Background()
	disk, path := openTestSQLite(t)
	memory, _ := NewMemory(4)
	base := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	chat, _ := model.NewChat(model.ChatInput{ID: "live-chat", UnreadCount: 2, LastMessageAt: base})
	if err := disk.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	if err := memory.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	incoming := testMessage(t, "live-chat", "incoming", "Café 東京 👋", base.Add(time.Minute), false)
	quote, _ := model.NewTextQuote("incoming", strings.Repeat("é👋", 200), false)
	sender, _ := model.NewContactID("synthetic-sender")
	reply := testMessage(t, "live-chat", "reply", "reply ❤️", base.Add(2*time.Minute), true).WithQuote(quote).WithSender(sender, true)
	delayed := testMessage(t, "live-chat", "delayed", "older", base.Add(-time.Minute), false)
	bodyless := testMessage(t, "live-chat", "bodyless", "later body", base.Add(3*time.Minute), false).WithoutBody()
	truncated := testMessage(t, "live-chat", "truncated", strings.Repeat("é", model.MaxRetainedTextBytes), base.Add(4*time.Minute), false)
	steps := []model.WriteBatch{
		sqliteLiveBatch(t, incoming, reply, delayed, bodyless, truncated),
		sqliteLiveBatch(t, reply.WithQuote(model.TextQuote{}), incoming.WithoutBody().WithSender(sender, true)),
		sqliteLiveBatch(t, testMessage(t, "live-chat", "bodyless", "later body", bodyless.SentAt(), false).WithQuote(quote)),
	}
	for step, batch := range steps {
		want, err := memory.WriteRealtime(ctx, batch)
		if err != nil {
			t.Fatal(err)
		}
		got, err := disk.WriteRealtime(ctx, batch)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d committed result differs from Memory", step)
		}
		if step == 0 {
			for i, count := range []uint32{3, 3, 4, 5, 6} {
				event, _ := got.At(i)
				if event.UnreadCount() != count {
					t.Fatalf("event %d unread=%d", i, event.UnreadCount())
				}
			}
		} else if got.Len() != 0 {
			t.Fatal("duplicate published")
		}
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	disk, err := OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	want, _, err := memory.Page(ctx, chat.ID(), model.NoCursor(), 100)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := disk.Page(ctx, chat.ID(), model.NoCursor(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("quote/sender/body/truncation metadata changed across restart")
	}
	stored, err := disk.Message(ctx, chat.ID(), reply.MessageID())
	if err != nil || stored.Quote() != quote || len(stored.Quote().Text()) > model.MaxQuoteTextBytes {
		t.Fatalf("quote lookup: %v", err)
	}
	state, err := disk.Chat(ctx, chat.ID())
	if err != nil || state.UnreadCount() != 6 || !state.IsGroup() || !state.LastMessageAt().Equal(truncated.SentAt()) {
		t.Fatalf("chat metadata: %+v %v", state, err)
	}
}

func TestSQLiteRealtimeSameRequestEnrichmentAndRollback(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestSQLite(t)
	message := testMessage(t, "chat", "one", "reply", time.Unix(1, 0).UTC(), true)
	quote, _ := model.NewTextQuote("prior", "quoted 👨‍👩‍👧‍👦", false)
	result, err := store.WriteRealtime(ctx, sqliteLiveBatch(t, message, message.WithQuote(quote)))
	if err != nil || result.Len() != 1 {
		t.Fatalf("result=%d error=%v", result.Len(), err)
	}
	event, _ := result.At(0)
	if event.Message().Quote() != quote {
		t.Fatal("same-request duplicate did not enrich inserted event")
	}
	next := testMessage(t, "chat", "two", "rollback", time.Unix(2, 0).UTC(), false)
	conflict := testMessage(t, "chat", "one", "invalid identity", time.Unix(3, 0).UTC(), true)
	result, err = store.WriteRealtime(ctx, sqliteLiveBatch(t, next, conflict))
	if err == nil || result.Len() != 0 {
		t.Fatalf("failed transaction leaked result: %d %v", result.Len(), err)
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 1 {
		t.Fatalf("rollback count=%d", count)
	}
	chat, err := store.Chat(ctx, message.ChatID())
	if err != nil || chat.UnreadCount() != 0 || !chat.LastMessageAt().Equal(message.SentAt()) {
		t.Fatalf("rollback changed activity/unread: %+v %v", chat, err)
	}
}

func TestSQLiteRealtimeUnreadSaturatesAndHistoryDoesNotIncrement(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx := context.Background()
	chat, _ := model.NewChat(model.ChatInput{ID: "unread", UnreadCount: maxSQLiteUnreadCount})
	if err := store.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	message := testMessage(t, "unread", "live", "body", time.Unix(1, 0).UTC(), false)
	result, err := store.WriteRealtime(ctx, sqliteLiveBatch(t, message))
	if err != nil || result.Len() != 1 {
		t.Fatalf("realtime: %v", err)
	}
	event, _ := result.At(0)
	if event.UnreadCount() != maxSQLiteUnreadCount {
		t.Fatal("unread overflowed")
	}
	history := testMessage(t, "history", "one", "body", time.Unix(2, 0).UTC(), false)
	batch, _ := model.NewWriteBatch(model.WriteHistory, []model.Message{history})
	if err := store.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	state, err := store.Chat(ctx, history.ChatID())
	if err != nil || state.UnreadCount() != 0 {
		t.Fatalf("history incremented unread: %v", err)
	}
	if _, err := store.WriteRealtime(ctx, batch); !errors.Is(err, ErrStoreRejected) {
		t.Fatalf("realtime accepted history origin: %v", err)
	}
}

func TestSQLiteRealtimeCancellationPhases(t *testing.T) {
	for _, phase := range []string{"queued", "transaction", "after-commit"} {
		t.Run(phase, func(t *testing.T) {
			timer := newManualSQLiteBatchTimer()
			signals := newSQLiteWriterTestSignals()
			hooks := signals.hooks()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if phase == "transaction" {
				hooks.beforePendingWrite = func(pendingWriteKind) error { cancel(); return nil }
			}
			if phase == "after-commit" {
				hooks.afterCommit = cancel
			}
			store, _ := openSQLiteWriterTestStore(t, timer, hooks)
			message := testMessage(t, "cancel", "one", "accepted", time.Unix(1, 0).UTC(), false)
			batch := sqliteLiveBatch(t, message)
			result := make(chan sqliteWriteResult, 1)
			go func() {
				events, err := store.WriteRealtime(ctx, batch)
				result <- sqliteWriteResult{committed: &events, err: err}
			}()
			receiveSQLiteTest(t, signals.enqueued)
			waitSQLiteBatchLogical(t, signals.batchAdds, 1)
			timer.waitReset(t)
			if phase == "queued" {
				cancel()
			}
			select {
			case <-result:
				t.Fatal("result preceded transaction")
			default:
			}
			if !timer.Fire() {
				t.Fatal("timer inactive")
			}
			got := receiveSQLiteTest(t, result)
			if !errors.Is(ctx.Err(), context.Canceled) || got.err != nil || got.committed.Len() != 1 {
				t.Fatalf("ambiguous cancellation: %v", got.err)
			}
			stored, err := store.Message(context.Background(), message.ChatID(), message.MessageID())
			if err != nil || stored.Text() != message.Text() {
				t.Fatalf("not committed: %v", err)
			}
		})
	}
}

func TestSQLiteRealtimePostCommitPermissionFailure(t *testing.T) {
	var fail atomic.Bool
	hooks := sqliteWriterHooks{
		afterCommit: func() { fail.Store(true) },
		checkFiles: func() error {
			if fail.Load() {
				return errors.New("synthetic permission failure")
			}
			return nil
		},
	}
	store, _ := openSQLiteWriterTestStore(t, nil, hooks)
	ctx := context.Background()
	message := testMessage(t, "permissions", "one", "committed", time.Unix(1, 0).UTC(), false)
	result, err := store.WriteRealtime(ctx, sqliteLiveBatch(t, message))
	if err != nil || result.Len() != 1 {
		t.Fatalf("commit misreported: %d %v", result.Len(), err)
	}
	if store.CacheDegradation() != CacheWriteUnavailable {
		t.Fatal("permission failure not exposed as degradation")
	}
	next := testMessage(t, "permissions", "two", "not committed", time.Unix(2, 0).UTC(), false)
	result, err = store.WriteRealtime(ctx, sqliteLiveBatch(t, next))
	if !errors.Is(err, ErrUnsafeCachePath) || result.Len() != 0 {
		t.Fatalf("unsafe next write accepted: %v", err)
	}
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 1 {
		t.Fatalf("count=%d", count)
	}
	fail.Store(false)
	result, err = store.WriteRealtime(ctx, sqliteLiveBatch(t, message))
	if err != nil || result.Len() != 0 {
		t.Fatalf("committed duplicate re-published: %v", err)
	}
}

func TestSQLiteRealtimeCloseDrainsResult(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	store, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())
	batch := sqliteLiveBatch(t, testMessage(t, "close", "one", "committed", time.Unix(1, 0).UTC(), false))
	result := make(chan sqliteWriteResult, 1)
	go func() {
		events, err := store.WriteRealtime(context.Background(), batch)
		result <- sqliteWriteResult{committed: &events, err: err}
	}()
	receiveSQLiteTest(t, signals.enqueued)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	got := receiveSQLiteTest(t, result)
	if got.err != nil || got.committed.Len() != 1 {
		t.Fatalf("close lost result: %v", got.err)
	}
}

func TestSQLiteRealtimeSharedTransactionResults(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback_%t", rollback), func(t *testing.T) {
			timer := newManualSQLiteBatchTimer()
			signals := newSQLiteWriterTestSignals()
			hooks := signals.hooks()
			if rollback {
				hooks.beforeCommit = func() error { return errors.New("synthetic rollback") }
			}
			store, _ := openSQLiteWriterTestStore(t, timer, hooks)
			var results [2]chan sqliteWriteResult
			for i := range results {
				batch := sqliteLiveBatch(t, testMessage(t, "shared", fmt.Sprintf("message-%d", i), "body", time.Unix(int64(i+1), 0).UTC(), false))
				results[i] = make(chan sqliteWriteResult, 1)
				go func(ch chan sqliteWriteResult) {
					events, err := store.WriteRealtime(context.Background(), batch)
					ch <- sqliteWriteResult{committed: &events, err: err}
				}(results[i])
				receiveSQLiteTest(t, signals.enqueued)
				waitSQLiteBatchLogical(t, signals.batchAdds, i+1)
			}
			timer.waitReset(t)
			if !timer.Fire() {
				t.Fatal("timer inactive")
			}
			for i, result := range results {
				got := receiveSQLiteTest(t, result)
				if rollback {
					if got.err == nil || got.committed.Len() != 0 {
						t.Fatal("rolled-back transaction leaked an insert")
					}
				} else {
					if got.err != nil || got.committed.Len() != 1 {
						t.Fatalf("request result: %v", got.err)
					}
					event, _ := got.committed.At(0)
					if event.UnreadCount() != uint32(i+1) || event.Message().MessageID().String() != fmt.Sprintf("message-%d", i) {
						t.Fatal("mailbox contained another request's result")
					}
				}
			}
			want := 2
			if rollback {
				want = 0
			}
			if queryCount(t, store.db, "SELECT COUNT(*) FROM messages") != want {
				t.Fatal("acknowledgement differs from database")
			}
		})
	}
}

func BenchmarkSQLiteCommittedResult(b *testing.B) {
	for _, size := range []int{1, 10, 50} {
		b.Run(fmt.Sprintf("Writes_%d", size), func(b *testing.B) {
			store, _ := openBenchmarkSQLite(b)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				var messages [model.MaxWriteBatchMessages]model.Message
				for i := 0; i < size; i++ {
					message, err := model.NewMessage(model.MessageInput{ChatID: "committed", MessageID: fmt.Sprintf("message-%d-%d", iteration, i), SentAt: time.Unix(int64(iteration*size+i+1), 0).UTC(), Text: "bounded synthetic body"})
					if err != nil {
						b.Fatal(err)
					}
					messages[i] = message
				}
				batch, err := model.NewWriteBatch(model.WriteRealtime, messages[:size])
				if err != nil {
					b.Fatal(err)
				}
				result, err := store.WriteRealtime(context.Background(), batch)
				if err != nil || result.Len() != size {
					b.Fatalf("committed=%d err=%v", result.Len(), err)
				}
			}
		})
	}
}
