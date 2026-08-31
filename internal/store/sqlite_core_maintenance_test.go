package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLiteCoreMaintenanceParity(t *testing.T) {
	ctx := context.Background()
	disk, path := openTestSQLite(t)
	memory, _ := NewMemory(4)
	base := time.Unix(100, 0).UTC()
	first := testMessage(t, "retention", "first", "body 👋", base, false)
	second := testMessage(t, "retention", "second", "bodyless", base, true).WithoutBody()
	third := testMessage(t, "retention", "third", "newest", base.Add(time.Second), false)
	batch := sqliteLiveBatch(t, first, second, third)
	if _, err := disk.WriteRealtime(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.WriteRealtime(ctx, batch); err != nil {
		t.Fatal(err)
	}
	checkSnapshot := func() {
		t.Helper()
		got, err := disk.RetentionSnapshot(ctx, first.ChatID())
		if err != nil {
			t.Fatal(err)
		}
		want, err := memory.RetentionSnapshot(ctx, first.ChatID())
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("snapshot differs: %+v / %+v (%v)", got, want, err)
		}
	}
	checkSnapshot()
	usage, err := disk.Usage(ctx)
	size, sizeErr := disk.CacheSizeBytes(ctx)
	if err != nil || sizeErr != nil || usage.Validate() != nil || usage.Messages() != 3 || usage.Bodies() != 2 || usage.Chats() != 1 || usage.Anchors() != 0 || usage.EstimatedBytes() != size || size <= 0 {
		t.Fatalf("usage=%+v size=%d error=%v/%v", usage, size, err, sizeErr)
	}
	anchor, _ := model.NewMessageAnchor(first.ChatID(), first.MessageID(), first.SentAt(), first.FromMe())
	plan, _ := model.NewPrunePlan(first.ChatID(), []model.MessageID{first.MessageID(), first.MessageID(), second.MessageID()}, &anchor)
	for i := 0; i < 2; i++ {
		got, err := disk.ApplyPrune(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		want, err := memory.ApplyPrune(ctx, plan)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("prune mismatch %+v / %+v (%v)", got, want, err)
		}
		checkSnapshot()
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	disk, err = OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	checkSnapshot()
	usage, err = disk.Usage(ctx)
	if err != nil || usage.Messages() != 1 || usage.Bodies() != 1 || usage.Anchors() != 1 {
		t.Fatalf("after prune usage: %+v %v", usage, err)
	}
	chat, err := disk.Chat(ctx, first.ChatID())
	if err != nil || chat.UnreadCount() != 2 || !chat.LastMessageAt().Equal(third.SentAt()) {
		t.Fatal("prune changed unread/activity")
	}
}

func TestSQLiteCoreMaintenanceCancellationRollbackAndBounds(t *testing.T) {
	ctx := context.Background()
	disk, _ := openTestSQLite(t)
	message := testMessage(t, "prune", "one", "unchanged", time.Unix(1, 0).UTC(), false)
	if _, err := disk.WriteRealtime(ctx, sqliteLiveBatch(t, message)); err != nil {
		t.Fatal(err)
	}
	plan, _ := model.NewPrunePlan(message.ChatID(), []model.MessageID{message.MessageID()}, nil)
	canceled, cancel := context.WithCancel(ctx)
	disk.pruneHooks.beforeCommit = cancel
	result, err := disk.ApplyPrune(canceled, plan)
	if !errors.Is(err, context.Canceled) || result != (model.PruneResult{}) {
		t.Fatalf("canceled prune result=%+v err=%v", result, err)
	}
	if _, err := disk.Message(ctx, message.ChatID(), message.MessageID()); err != nil {
		t.Fatal("canceled prune committed")
	}
	disk.pruneHooks.beforeCommit = nil
	if _, err := disk.RetentionSnapshot(canceled, message.ChatID()); !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot cancellation: %v", err)
	}
	if _, err := disk.Usage(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("usage cancellation: %v", err)
	}
	if _, err := disk.ApplyPrune(ctx, model.PrunePlan{}); !errors.Is(err, ErrStoreRejected) {
		t.Fatalf("invalid plan: %v", err)
	}
	missing, _ := model.NewChatID("missing")
	anchor, _ := model.NewMessageAnchor(missing, message.MessageID(), message.SentAt(), false)
	missingPlan, _ := model.NewPrunePlan(missing, nil, &anchor)
	if _, err := disk.ApplyPrune(ctx, missingPlan); err != nil {
		t.Fatal(err)
	}
	if queryCount(t, disk.db, "SELECT COUNT(*) FROM chats") != 1 {
		t.Fatal("prune created missing chat")
	}
	writeSQLitePageMessages(t, disk, sqliteSequentialMessages(t, "oversize", model.MaxRetentionSnapshotSummaries+1))
	overID, _ := model.NewChatID("oversize")
	if _, err := disk.RetentionSnapshot(ctx, overID); !errors.Is(err, ErrStoreRejected) {
		t.Fatalf("oversized snapshot silently truncated: %v", err)
	}
}

func TestSQLiteCoreMaintenanceSharesWriterGate(t *testing.T) {
	timer := newManualSQLiteBatchTimer()
	signals := newSQLiteWriterTestSignals()
	disk, _ := openSQLiteWriterTestStore(t, timer, signals.hooks())
	ctx := context.Background()
	if err := disk.acquireMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	message := testMessage(t, "gate", "one", "bounded", time.Unix(1, 0).UTC(), false)
	batch := sqliteLiveBatch(t, message)
	done := asyncSQLiteCall(func() error { _, err := disk.WriteRealtime(ctx, batch); return err })
	receiveSQLiteTest(t, signals.enqueued)
	waitSQLiteBatchLogical(t, signals.batchAdds, 1)
	timer.waitReset(t)
	if !timer.Fire() {
		t.Fatal("timer inactive")
	}
	select {
	case <-done:
		t.Fatal("writer bypassed maintenance gate")
	default:
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := disk.Usage(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked maintenance cancellation: %v", err)
	}
	disk.releaseMaintenance()
	if err := receiveSQLiteTest(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteNativePruneDropsQuoteBytesWithBody(t *testing.T) {
	disk, _ := openTestSQLite(t)
	messages := sqliteSequentialMessages(t, "native-quote", 101)
	quote, _ := model.NewTextQuote("quoted", "Synthetic Café 👋", true)
	messages[0] = messages[0].WithQuote(quote)
	writeSQLitePageMessages(t, disk, messages)
	result, err := disk.Prune(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	wantBytes := len(messages[0].Text()) + len(quote.MessageID().String()) + len(quote.Text())
	if err != nil || result.BodiesDropped != 1 || result.LogicalBytesFreed != int64(wantBytes) {
		t.Fatalf("native prune result=%+v err=%v", result, err)
	}
	stored, err := disk.Message(context.Background(), messages[0].ChatID(), messages[0].MessageID())
	if err != nil || stored.BodyRetained() || stored.Quote() != (model.TextQuote{}) {
		t.Fatalf("native prune retained quote: %v", err)
	}
	if queryCount(t, disk.db, "SELECT COUNT(*) FROM messages WHERE retained_body = 0 AND (quote_id != '' OR quote_text != '')") != 0 {
		t.Fatal("quote bytes survived body pruning")
	}
}
