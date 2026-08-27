package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLitePruneRetainsNewestHundredPerChat(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-newest", 120, now.Add(-120*24*time.Hour), time.Minute))

	result, err := store.Prune(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BodiesDropped != 20 {
		t.Fatalf("bodies dropped=%d want=20", result.BodiesDropped)
	}
	assertSQLiteBodyCounts(t, store.db, 120, 100, 20)
	assertSQLiteBodyState(t, store.db, "chat-newest", "message-0019", false)
	assertSQLiteBodyState(t, store.db, "chat-newest", "message-0020", true)
}

func TestSQLitePruneRetainsRecentBodiesBeyondHundred(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-recent", 130, now.Add(-10*24*time.Hour), time.Minute))

	result, err := store.Prune(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result != (SQLitePruneResult{}) {
		t.Fatalf("result=%+v", result)
	}
	assertSQLiteBodyCounts(t, store.db, 130, 130, 0)
}

func TestSQLitePruneNinetyDayBoundary(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-sqliteBodyRetentionAge)
	messages := make([]model.Message, 0, 102)
	messages = append(messages,
		testMessage(t, "chat-boundary", "message-before", "before", cutoff.Add(-time.Millisecond), false),
		testMessage(t, "chat-boundary", "message-exact", "exact", cutoff, false),
	)
	for index := range 100 {
		messages = append(messages, testMessage(t, "chat-boundary", fmt.Sprintf("message-new-%03d", index), "new", cutoff.Add(time.Duration(index+1)*time.Minute), false))
	}
	writeSQLitePageMessages(t, store, messages)

	result, err := store.Prune(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BodiesDropped != 1 {
		t.Fatalf("bodies dropped=%d want=1", result.BodiesDropped)
	}
	assertSQLiteBodyState(t, store.db, "chat-boundary", "message-before", false)
	assertSQLiteBodyState(t, store.db, "chat-boundary", "message-exact", true)
}

func TestSQLitePruneMixedChatsAndLogicalByteAccounting(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	old := now.Add(-120 * 24 * time.Hour)
	texts := []string{"ASCII", "Café", "👨‍👩‍👧‍👦"}
	var wantBytes int64
	for chatIndex := range 3 {
		chatID := fmt.Sprintf("chat-mixed-%d", chatIndex)
		messages := maintenanceMessages(t, chatID, 103, old, time.Minute)
		for index := range texts {
			messages[index] = testMessage(t, chatID, fmt.Sprintf("message-%04d", index), texts[index], old.Add(time.Duration(index)*time.Minute), false)
			wantBytes += int64(len(texts[index]))
		}
		writeSQLitePageMessages(t, store, messages)
	}

	result, err := store.Prune(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BodiesDropped != 9 || result.LogicalBytesFreed != wantBytes {
		t.Fatalf("result=%+v want dropped=9 bytes=%d", result, wantBytes)
	}
	assertSQLiteBodyCounts(t, store.db, 309, 300, 9)
}

func TestSQLitePrunePreservesPaginationIdentityAndOrder(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	want := maintenanceMessages(t, "chat-page-prune", 125, now.Add(-120*24*time.Hour), time.Minute)
	writeSQLitePageMessages(t, store, want)
	chatID := sqlitePageChatID(t, "chat-page-prune")
	before := collectSQLitePages(t, store, chatID, 17)

	if _, err := store.Prune(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	after := collectSQLitePages(t, store, chatID, 17)
	if len(after) != len(before) {
		t.Fatalf("messages after=%d before=%d", len(after), len(before))
	}
	for index := range before {
		if before[index].MessageID() != after[index].MessageID() || before[index].SentAt() != after[index].SentAt() {
			t.Fatalf("message %d identity/order changed", index)
		}
	}
	if !after[0].BodyRetained() || after[len(after)-1].BodyRetained() || after[len(after)-1].Text() != "" {
		t.Fatalf("body state after prune: newest retained=%v oldest retained=%v oldest text=%q",
			after[0].BodyRetained(), after[len(after)-1].BodyRetained(), after[len(after)-1].Text())
	}
	assertSQLiteBodyCounts(t, store.db, 125, 100, 25)
}

func TestSQLitePrunePreservesChatAndContactMetadata(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	contact, err := model.NewContact(model.ContactInput{ID: "contact-prune", DisplayName: "Café", UpdatedAt: now, IngestSeq: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertContact(context.Background(), contact); err != nil {
		t.Fatal(err)
	}
	chat, err := model.NewChat(model.ChatInput{
		ID: "chat-metadata", ContactID: "contact-prune", DisplayName: "Project Room",
		LastMessageAt: now, UnreadCount: 12, Muted: true, UpdatedAt: now, IngestSeq: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-metadata", 110, now.Add(-120*24*time.Hour), time.Minute))
	wantContact, err := store.Contact(context.Background(), contact.ID())
	if err != nil {
		t.Fatal(err)
	}
	wantChat, err := store.Chat(context.Background(), chat.ID())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Prune(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	gotContact, err := store.Contact(context.Background(), contact.ID())
	if err != nil {
		t.Fatal(err)
	}
	gotChat, err := store.Chat(context.Background(), chat.ID())
	if err != nil {
		t.Fatal(err)
	}
	if gotContact.DisplayName() != wantContact.DisplayName() || gotContact.IngestSeq() != wantContact.IngestSeq() {
		t.Fatalf("contact changed: got=%q/%d", gotContact.DisplayName(), gotContact.IngestSeq())
	}
	if gotChat.DisplayName() != wantChat.DisplayName() || gotChat.UnreadCount() != wantChat.UnreadCount() || gotChat.Muted() != wantChat.Muted() {
		t.Fatalf("chat metadata changed: name=%q unread=%d muted=%v", gotChat.DisplayName(), gotChat.UnreadCount(), gotChat.Muted())
	}
}

func TestSQLitePruneIsBoundedToFiveHundredBodies(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-bounded-prune", 620, now.Add(-200*24*time.Hour), time.Minute))

	result, err := store.Prune(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BodiesDropped != sqliteMaxPrunedBodies {
		t.Fatalf("bodies dropped=%d want=%d", result.BodiesDropped, sqliteMaxPrunedBodies)
	}
	assertSQLiteBodyCounts(t, store.db, 620, 120, 500)
}

func TestSQLitePruneCancellationBeforeCommitRollsBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	options := SQLiteOptions{Path: testSQLitePath(t)}
	options.pruneHooks.beforeCommit = cancel
	store := openMaintenanceSQLite(t, options, sqliteWriterOptions{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-cancel-prune", 105, now.Add(-120*24*time.Hour), time.Minute))

	if _, err := store.Prune(ctx, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
	assertSQLiteBodyCounts(t, store.db, 105, 105, 0)
}

func TestSQLitePruneRejectsAlreadyCanceledContext(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Prune(ctx, time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
}

func TestSQLitePruneCorruptionIsControlled(t *testing.T) {
	options := SQLiteOptions{Path: testSQLitePath(t)}
	options.pruneHooks.beforeUpdate = func() error { return syntheticSQLiteCodeError(11) }
	store := openMaintenanceSQLite(t, options, sqliteWriterOptions{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-corrupt-prune", 105, now.Add(-120*24*time.Hour), time.Minute))
	if _, err := store.Prune(context.Background(), now); !errors.Is(err, ErrCorruptCache) {
		t.Fatalf("error=%v want ErrCorruptCache", err)
	}
	assertSQLiteBodyCounts(t, store.db, 105, 105, 0)
}

func TestSQLitePruneBusyIsControlledAndWriterRemainsUsable(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	options := SQLiteOptions{Path: testSQLitePath(t)}
	options.pruneHooks.beforeUpdate = func() error {
		if busy.Load() {
			return syntheticSQLiteCodeError(5)
		}
		return nil
	}
	store := openMaintenanceSQLite(t, options, sqliteWriterOptions{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-busy-prune", 105, now.Add(-120*24*time.Hour), time.Minute))

	if _, err := store.Prune(context.Background(), now); !errors.Is(err, ErrBusy) {
		t.Fatalf("error=%v want ErrBusy", err)
	}
	assertSQLiteBodyCounts(t, store.db, 105, 105, 0)
	busy.Store(false)
	if _, err := store.Prune(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMessage(context.Background(), testMessage(t, "chat-busy-prune", "message-after", "healthy", now, true)); err != nil {
		t.Fatalf("writer after maintenance: %v", err)
	}
}

func TestSQLitePruneSerializesWithBoundedWriter(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	enqueued := make(chan struct{}, 1)
	var observeEnqueue atomic.Bool
	options := SQLiteOptions{Path: testSQLitePath(t)}
	options.pruneHooks.beforeCommit = func() {
		close(entered)
		<-release
	}
	writerOptions := sqliteWriterOptions{hooks: sqliteWriterHooks{afterEnqueue: func() {
		if observeEnqueue.Load() {
			enqueued <- struct{}{}
		}
	}}}
	store := openMaintenanceSQLite(t, options, writerOptions)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-serialized", 105, now.Add(-120*24*time.Hour), time.Minute))

	pruned := make(chan error, 1)
	go func() {
		_, err := store.Prune(context.Background(), now)
		pruned <- err
	}()
	<-entered
	observeEnqueue.Store(true)
	written := make(chan error, 1)
	go func() {
		written <- store.PutMessage(context.Background(), testMessage(t, "chat-serialized", "message-after-prune", "after", now, true))
	}()
	<-enqueued
	select {
	case err := <-written:
		t.Fatalf("writer completed while prune owned maintenance: %v", err)
	default:
	}
	close(release)
	if err := <-pruned; err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	assertSQLiteBodyCounts(t, store.db, 106, 101, 5)
}

func TestSQLitePruneSQLFailureRollsBack(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-rollback-prune", 105, now.Add(-120*24*time.Hour), time.Minute))
	if _, err := store.db.ExecContext(context.Background(), `
CREATE TRIGGER synthetic_prune_failure
BEFORE UPDATE OF retained_body ON messages
WHEN NEW.retained_body = 0
BEGIN
    SELECT RAISE(ABORT, 'synthetic prune failure');
END`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Prune(context.Background(), now); !errors.Is(err, ErrCacheIO) {
		t.Fatalf("error=%v want ErrCacheIO", err)
	}
	assertSQLiteBodyCounts(t, store.db, 105, 105, 0)
}

func TestSQLiteCacheSizeAccountsForMainWALAndSHM(t *testing.T) {
	store, path := openTestSQLite(t)
	store.cacheStat = fakeSQLiteStat(map[string]int64{
		path:          100,
		path + "-wal": 23,
		path + "-shm": 7,
	})
	size, err := store.CacheSizeBytes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if size != 130 {
		t.Fatalf("size=%d want=130", size)
	}
}

func TestSQLiteCacheSizeAllowsMissingOptionalFiles(t *testing.T) {
	store, path := openTestSQLite(t)
	for _, test := range []struct {
		name  string
		sizes map[string]int64
		want  int64
	}{
		{name: "main only", sizes: map[string]int64{path: 100}, want: 100},
		{name: "main and WAL", sizes: map[string]int64{path: 100, path + "-wal": 20}, want: 120},
		{name: "main and SHM", sizes: map[string]int64{path: 100, path + "-shm": 7}, want: 107},
	} {
		t.Run(test.name, func(t *testing.T) {
			store.cacheStat = fakeSQLiteStat(test.sizes)
			size, err := store.CacheSizeBytes(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if size != test.want {
				t.Fatalf("size=%d want=%d", size, test.want)
			}
		})
	}
}

func TestSQLiteCacheSizeRejectsMissingMainFile(t *testing.T) {
	store, _ := openTestSQLite(t)
	store.cacheStat = fakeSQLiteStat(nil)
	if _, err := store.CacheSizeBytes(context.Background()); !errors.Is(err, ErrCacheIO) {
		t.Fatalf("error=%v want ErrCacheIO", err)
	}
}

func TestSQLiteCachePressureReturnsToNormalAfterRemeasure(t *testing.T) {
	path := testSQLitePath(t)
	var mainStats atomic.Int64
	stat := func(candidate string) (os.FileInfo, error) {
		if candidate != path {
			return nil, os.ErrNotExist
		}
		if mainStats.Add(1) == 1 {
			return fakeSQLiteFileInfo{name: candidate, size: 200}, nil
		}
		return fakeSQLiteFileInfo{name: candidate, size: 50}, nil
	}
	options := SQLiteOptions{Path: path, cacheBudgetBytes: 100, cacheStat: stat}
	store := openMaintenanceSQLite(t, options, sqliteWriterOptions{})
	result, err := store.EnforceCacheBudget(context.Background(), time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.SizeBefore != 200 || result.SizeAfter != 50 || !result.Checkpointed || result.Degradation != CacheNormal {
		t.Fatalf("result=%+v", result)
	}
}

func TestSQLiteCachePressurePrunesThenReportsPressure(t *testing.T) {
	options := SQLiteOptions{Path: testSQLitePath(t), cacheBudgetBytes: 1}
	store := openMaintenanceSQLite(t, options, sqliteWriterOptions{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-pressure", 110, now.Add(-120*24*time.Hour), time.Minute))

	result, err := store.EnforceCacheBudget(context.Background(), now)
	if !errors.Is(err, ErrCachePressure) {
		t.Fatalf("error=%v want ErrCachePressure", err)
	}
	if result.Prune.BodiesDropped != 10 || !result.Checkpointed || result.Degradation != CachePressure {
		t.Fatalf("result=%+v", result)
	}
	if state := store.CacheDegradation(); state != CachePressure {
		t.Fatalf("degradation=%v", state)
	}
	assertSQLiteBodyCounts(t, store.db, 110, 100, 10)
}

func TestSQLiteCachePressureNeverViolatesRetention(t *testing.T) {
	options := SQLiteOptions{Path: testSQLitePath(t), cacheBudgetBytes: 1}
	store := openMaintenanceSQLite(t, options, sqliteWriterOptions{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-pressure-protected", 140, now.Add(-10*24*time.Hour), time.Minute))

	result, err := store.EnforceCacheBudget(context.Background(), now)
	if !errors.Is(err, ErrCachePressure) {
		t.Fatalf("error=%v want ErrCachePressure", err)
	}
	if result.Prune != (SQLitePruneResult{}) {
		t.Fatalf("prune=%+v", result.Prune)
	}
	assertSQLiteBodyCounts(t, store.db, 140, 140, 0)
}

func TestSQLiteCheckpointFailureDoesNotUndoLogicalPrune(t *testing.T) {
	options := SQLiteOptions{Path: testSQLitePath(t), cacheBudgetBytes: 1}
	options.checkpoint = func(context.Context, *sql.DB) error {
		return newSQLiteError(ErrBusy, nil)
	}
	store := openMaintenanceSQLite(t, options, sqliteWriterOptions{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	writeSQLitePageMessages(t, store, maintenanceMessages(t, "chat-checkpoint", 105, now.Add(-120*24*time.Hour), time.Minute))

	result, err := store.EnforceCacheBudget(context.Background(), now)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("error=%v want ErrBusy", err)
	}
	if result.Prune.BodiesDropped != 5 || result.Checkpointed {
		t.Fatalf("result=%+v", result)
	}
	assertSQLiteBodyCounts(t, store.db, 105, 100, 5)
}

func TestSQLiteDiskFullIsContentFreeAndPriorDataSurvives(t *testing.T) {
	var full atomic.Bool
	hooks := sqliteWriterHooks{beforePendingWrite: func(pendingWriteKind) error {
		if full.Load() {
			return syntheticSQLiteCodeError(13)
		}
		return nil
	}}
	store := openMaintenanceSQLite(t, SQLiteOptions{Path: testSQLitePath(t)}, sqliteWriterOptions{hooks: hooks})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	first := testMessage(t, "chat-full", "message-first", "private first", now, false)
	if err := store.PutMessage(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	full.Store(true)
	err := store.PutMessage(context.Background(), testMessage(t, "chat-full", "message-rejected", "private rejected", now.Add(time.Second), false))
	if !errors.Is(err, ErrDiskFull) || err.Error() != ErrDiskFull.Error() {
		t.Fatalf("error=%q want content-free ErrDiskFull", err)
	}
	if state := store.CacheDegradation(); state != CacheWriteUnavailable {
		t.Fatalf("degradation=%v", state)
	}
	got, err := store.Message(context.Background(), first.ChatID(), first.MessageID())
	if err != nil || got.Text() != first.Text() {
		t.Fatalf("prior message got=%q err=%v", got.Text(), err)
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM messages WHERE message_id='message-rejected'"); got != 0 {
		t.Fatalf("rejected rows=%d", got)
	}
	full.Store(false)
	if err := store.PutMessage(context.Background(), testMessage(t, "chat-full", "message-recovered", "healthy", now.Add(2*time.Second), false)); err != nil {
		t.Fatalf("writer recovery: %v", err)
	}
	if state := store.CacheDegradation(); state != CacheNormal {
		t.Fatalf("degradation after healthy write=%v", state)
	}
}

func TestSQLitePruneStressMultiChatIdempotentAndOrdered(t *testing.T) {
	store, _ := openTestSQLite(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	for chatIndex := range 4 {
		chatID := fmt.Sprintf("chat-stress-%d", chatIndex)
		messages := maintenanceMessages(t, chatID, 140, now.Add(-140*24*time.Hour), 24*time.Hour)
		writeSQLitePageMessages(t, store, messages)
		writeSQLitePageMessages(t, store, messages)
	}

	result, err := store.Prune(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BodiesDropped != 160 {
		t.Fatalf("result=%+v want dropped=160", result)
	}
	if second, err := store.Prune(context.Background(), now); err != nil || second != (SQLitePruneResult{}) {
		t.Fatalf("second prune=%+v err=%v", second, err)
	}
	for chatIndex := range 4 {
		chatID := fmt.Sprintf("chat-stress-%d", chatIndex)
		messages := collectSQLitePages(t, store, sqlitePageChatID(t, chatID), 31)
		if len(messages) != 140 {
			t.Fatalf("chat=%s messages=%d", chatID, len(messages))
		}
		for index := 1; index < len(messages); index++ {
			if messages[index-1].SentAt().Before(messages[index].SentAt()) {
				t.Fatalf("chat=%s order changed at %d", chatID, index)
			}
		}
	}
	assertSQLiteBodyCounts(t, store.db, 560, 400, 160)
}

func maintenanceMessages(t testing.TB, chatID string, count int, first time.Time, step time.Duration) []model.Message {
	t.Helper()
	messages := make([]model.Message, count)
	for index := range messages {
		message, err := model.NewMessage(model.MessageInput{
			ChatID: chatID, MessageID: fmt.Sprintf("message-%04d", index),
			Text: fmt.Sprintf("body-%04d", index), SentAt: first.Add(time.Duration(index) * step),
			FromMe: index%3 == 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		messages[index] = message
	}
	return messages
}

func openMaintenanceSQLite(t *testing.T, options SQLiteOptions, writerOptions sqliteWriterOptions) *SQLiteStore {
	t.Helper()
	store, err := openSQLiteWithWriter(context.Background(), options, operatingSystemFS, nil, writerOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func assertSQLiteBodyCounts(t *testing.T, database *sql.DB, rows, retained, bodyless int) {
	t.Helper()
	if got := queryCount(t, database, "SELECT COUNT(*) FROM messages"); got != rows {
		t.Fatalf("message rows=%d want=%d", got, rows)
	}
	if got := queryCount(t, database, "SELECT COUNT(*) FROM messages WHERE retained_body=1"); got != retained {
		t.Fatalf("retained bodies=%d want=%d", got, retained)
	}
	if got := queryCount(t, database, "SELECT COUNT(*) FROM messages WHERE retained_body=0 AND body IS NULL AND body_bytes=0"); got != bodyless {
		t.Fatalf("bodyless rows=%d want=%d", got, bodyless)
	}
}

func assertSQLiteBodyState(t *testing.T, database *sql.DB, chatID, messageID string, retained bool) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(context.Background(),
		"SELECT retained_body FROM messages WHERE chat_id=? AND message_id=?", chatID, messageID,
	).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if (got == 1) != retained {
		t.Fatalf("message %s/%s retained=%d want=%v", chatID, messageID, got, retained)
	}
}

type syntheticSQLiteCodeError int

func (err syntheticSQLiteCodeError) Error() string { return "synthetic sqlite outcome" }
func (err syntheticSQLiteCodeError) Code() int     { return int(err) }

type fakeSQLiteFileInfo struct {
	name string
	size int64
}

func (info fakeSQLiteFileInfo) Name() string  { return info.name }
func (info fakeSQLiteFileInfo) Size() int64   { return info.size }
func (fakeSQLiteFileInfo) Mode() fs.FileMode  { return 0o600 }
func (fakeSQLiteFileInfo) ModTime() time.Time { return time.Unix(1, 0) }
func (fakeSQLiteFileInfo) IsDir() bool        { return false }
func (fakeSQLiteFileInfo) Sys() any           { return nil }

func fakeSQLiteStat(sizes map[string]int64) func(string) (os.FileInfo, error) {
	return func(path string) (os.FileInfo, error) {
		if size, ok := sizes[path]; ok {
			return fakeSQLiteFileInfo{name: path, size: size}, nil
		}
		return nil, os.ErrNotExist
	}
}
