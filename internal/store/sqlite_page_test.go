package store

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLitePageEmptyChatDoesNotCreatePlaceholder(t *testing.T) {
	store, _ := openTestSQLite(t)
	chatID := sqlitePageChatID(t, "unknown-chat")

	messages, next, err := store.Page(context.Background(), chatID, model.NoCursor(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if messages == nil || len(messages) != 0 || !next.IsZero() {
		t.Fatalf("empty page=(%v,%v), want non-nil empty page and no cursor", messages, next)
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM chats"); got != 0 {
		t.Fatalf("chat count=%d after unknown-chat read", got)
	}
}

func TestSQLitePageBoundsLimitsAndLookahead(t *testing.T) {
	store, _ := openTestSQLite(t)
	chatID := "chat-bounds"
	writeSQLitePageMessages(t, store, sqliteSequentialMessages(t, chatID, 120))

	for _, test := range []struct {
		name  string
		limit int
		want  int
	}{
		{name: "zero defaults", limit: 0, want: defaultPageMessages},
		{name: "negative defaults", limit: -7, want: defaultPageMessages},
		{name: "one", limit: 1, want: 1},
		{name: "maximum", limit: maxPageMessages, want: maxPageMessages},
		{name: "over maximum clamps", limit: maxPageMessages + 1, want: maxPageMessages},
		{name: "far over maximum clamps", limit: 10_000, want: maxPageMessages},
	} {
		t.Run(test.name, func(t *testing.T) {
			messages, next, err := store.Page(context.Background(), sqlitePageChatID(t, chatID), model.NoCursor(), test.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != test.want || cap(messages) > test.want {
				t.Fatalf("page len/cap=(%d,%d), want len=%d cap<=%d", len(messages), cap(messages), test.want, test.want)
			}
			if next.IsZero() {
				t.Fatal("lookahead page did not return a continuation cursor")
			}
		})
	}

	for _, test := range []struct {
		limit int
		want  int
	}{
		{limit: -1, want: defaultPageMessages},
		{limit: 0, want: defaultPageMessages},
		{limit: 1, want: 1},
		{limit: 100, want: 100},
		{limit: 101, want: 100},
	} {
		if got := boundedSQLitePageLimit(test.limit); got != test.want {
			t.Fatalf("boundedSQLitePageLimit(%d)=%d want=%d", test.limit, got, test.want)
		}
		if got := boundedSQLitePageLimit(test.limit) + 1; got > maxSQLitePageRows {
			t.Fatalf("internal row limit=%d exceeds %d", got, maxSQLitePageRows)
		}
	}
}

func TestSQLitePageTraverses257MessagesExactly(t *testing.T) {
	store, _ := openTestSQLite(t)
	const chatID = "chat-traverse"
	writeSQLitePageMessages(t, store, sqliteSequentialMessages(t, chatID, 257))

	for _, pageSize := range []int{1, 10, 32, 50, 100} {
		t.Run(fmt.Sprintf("page_%d", pageSize), func(t *testing.T) {
			cursor := model.NoCursor()
			seen := make(map[string]bool, 257)
			var previous model.Message
			for {
				page, next, err := store.Page(context.Background(), sqlitePageChatID(t, chatID), cursor, pageSize)
				if err != nil {
					t.Fatal(err)
				}
				if len(page) == 0 {
					t.Fatal("received an empty page before traversal completed")
				}
				if len(page) > pageSize {
					t.Fatalf("page len=%d exceeds requested size=%d", len(page), pageSize)
				}
				for _, message := range page {
					messageID := message.MessageID().String()
					if seen[messageID] {
						t.Fatalf("duplicate message %q", messageID)
					}
					if len(seen) > 0 && !newer(previous, message) {
						t.Fatalf("messages out of descending tuple order: %q then %q", previous.MessageID().String(), messageID)
					}
					seen[messageID] = true
					previous = message
				}
				if next.IsZero() {
					break
				}
				last := page[len(page)-1]
				if !next.SentAt().Equal(last.SentAt()) || next.MessageID().String() != last.MessageID().String() {
					t.Fatalf("cursor does not identify last returned message: cursor=(%v,%q) last=(%v,%q)",
						next.SentAt(), next.MessageID().String(), last.SentAt(), last.MessageID().String())
				}
				cursor = next
			}
			if len(seen) != 257 {
				t.Fatalf("traversed %d messages, want 257", len(seen))
			}
		})
	}
}

func TestSQLitePageSameTimestampUsesMessageIDDescending(t *testing.T) {
	store, _ := openTestSQLite(t)
	const (
		chatID = "chat-ties"
		count  = 60
	)
	sentAt := time.UnixMilli(1_000).UTC()
	messages := make([]model.Message, count)
	for index := range count {
		value := (index * 17) % count
		messages[index] = testMessage(t, chatID, fmt.Sprintf("message-%03d", value), "tie", sentAt, false)
	}
	writeSQLitePageMessages(t, store, messages)

	got := collectSQLitePages(t, store, sqlitePageChatID(t, chatID), 7)
	if len(got) != count {
		t.Fatalf("message count=%d want=%d", len(got), count)
	}
	for index, message := range got {
		want := fmt.Sprintf("message-%03d", count-1-index)
		if message.MessageID().String() != want {
			t.Fatalf("message[%d]=%q want=%q", index, message.MessageID().String(), want)
		}
	}
}

func TestSQLitePagePreservesMessageFidelityAndUnicode(t *testing.T) {
	store, _ := openTestSQLite(t)
	const chatID = "chat-unicode"
	bodies := []string{"ASCII", "Café", "日本語", "❤️", "👍🏽", "👨‍👩‍👧‍👦", "🇦🇹"}
	want := make([]model.Message, 0, len(bodies)+1)
	for index, body := range bodies {
		want = append(want, testMessage(t, chatID, fmt.Sprintf("message-%02d", index), body, time.UnixMilli(int64(index+1)).UTC(), index%2 == 0))
	}
	want = append(want, testMessage(t, chatID, "message-bodyless", "discarded", time.UnixMilli(100).UTC(), true).WithoutBody())
	writeSQLitePageMessages(t, store, want)

	got := collectSQLitePages(t, store, sqlitePageChatID(t, chatID), 3)
	if len(got) != len(want) {
		t.Fatalf("message count=%d want=%d", len(got), len(want))
	}
	wantByID := make(map[string]model.Message, len(want))
	for _, message := range want {
		wantByID[message.MessageID().String()] = message
	}
	for _, message := range got {
		expected, ok := wantByID[message.MessageID().String()]
		if !ok {
			t.Fatalf("unexpected message %q", message.MessageID().String())
		}
		assertMessagesEqual(t, message, expected)
		if message.BodyTruncated() != expected.BodyTruncated() {
			t.Fatalf("message %q body_truncated=%t want=%t", message.MessageID().String(), message.BodyTruncated(), expected.BodyTruncated())
		}
		if message.ByteSize() != expected.ByteSize() {
			t.Fatalf("message %q byte size=%d want=%d", message.MessageID().String(), message.ByteSize(), expected.ByteSize())
		}
	}
}

func TestSQLitePageMutationBetweenPagesHasDocumentedKeysetSemantics(t *testing.T) {
	store, _ := openTestSQLite(t)
	const chatID = "chat-mutation"
	writeSQLitePageMessages(t, store, sqliteSequentialMessages(t, chatID, 10))

	first, cursor, err := store.Page(context.Background(), sqlitePageChatID(t, chatID), model.NoCursor(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 5 || cursor.IsZero() {
		t.Fatalf("first page len=%d cursor=%v", len(first), cursor)
	}
	newerMessage := testMessage(t, chatID, "message-newer", "newer", time.UnixMilli(1_000).UTC(), false)
	olderMessage := testMessage(t, chatID, "message-older-insert", "older", time.UnixMilli(5).UTC(), false)
	writeSQLitePageMessages(t, store, []model.Message{newerMessage, olderMessage})

	second, _, err := store.Page(context.Background(), sqlitePageChatID(t, chatID), cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := make(map[string]bool, len(first))
	for _, message := range first {
		firstIDs[message.MessageID().String()] = true
	}
	foundOlder := false
	for _, message := range second {
		messageID := message.MessageID().String()
		if messageID == newerMessage.MessageID().String() {
			t.Fatal("newer message inserted above the cursor leaked into page two")
		}
		if firstIDs[messageID] {
			t.Fatalf("message %q repeated across pages", messageID)
		}
		foundOlder = foundOlder || messageID == olderMessage.MessageID().String()
	}
	if !foundOlder {
		t.Fatal("older message inserted below the cursor was not visible on page two")
	}
}

func TestSQLitePageWorksAfterMultipleWALWriterBatches(t *testing.T) {
	store, _ := openTestSQLite(t)
	const chatID = "chat-wal"
	messages := sqliteSequentialMessages(t, chatID, 110)
	writeSQLitePageMessages(t, store, messages[:50])
	writeSQLitePageMessages(t, store, messages[50:100])
	writeSQLitePageMessages(t, store, messages[100:])

	got := collectSQLitePages(t, store, sqlitePageChatID(t, chatID), 32)
	if len(got) != len(messages) {
		t.Fatalf("message count=%d want=%d", len(got), len(messages))
	}
	if mode := strings.ToLower(pragmaText(t, store.db, "journal_mode")); mode != "wal" {
		t.Fatalf("journal_mode=%q", mode)
	}
	if max := store.db.Stats().MaxOpenConnections; max != 1 {
		t.Fatalf("MaxOpenConnections=%d", max)
	}
}

func TestSQLitePageQueryPlanUsesMessagesPageIndex(t *testing.T) {
	store, _ := openTestSQLite(t)
	for _, test := range []struct {
		name  string
		query string
		args  []any
	}{
		{name: "first", query: sqliteFirstMessagePageSQL, args: []any{"chat-plan", 51}},
		{name: "continuation", query: sqliteContinuationMessagePageSQL, args: []any{"chat-plan", int64(10), int64(10), "message-10", 51}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := store.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+test.query, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(details, "\n"), "messages_page_idx") {
				t.Fatalf("query plan does not use messages_page_idx: %v", details)
			}
		})
	}
}

func TestSQLitePageQueriesNeverUseOffset(t *testing.T) {
	for name, query := range map[string]string{
		"first":        sqliteFirstMessagePageSQL,
		"continuation": sqliteContinuationMessagePageSQL,
	} {
		if strings.Contains(strings.ToUpper(query), "OFFSET") {
			t.Fatalf("%s query contains OFFSET: %s", name, query)
		}
	}
}

func TestSQLitePageHonorsAlreadyCanceledContext(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := store.Page(ctx, sqlitePageChatID(t, "chat-canceled"), model.NoCursor(), 10)
	if err != context.Canceled {
		t.Fatalf("Page error=%v want=%v", err, context.Canceled)
	}
}

func TestSQLitePageCancellationWhileWaitingToQuery(t *testing.T) {
	store, _ := openTestSQLite(t)
	connection, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	waitsBefore := store.db.Stats().WaitCount
	chatID := sqlitePageChatID(t, "chat-wait-cancel")
	go func() {
		_, _, err := store.Page(ctx, chatID, model.NoCursor(), 10)
		result <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for store.db.Stats().WaitCount == waitsBefore {
		if time.Now().After(deadline) {
			t.Fatal("Page did not wait for the single database connection")
		}
		runtime.Gosched()
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Page error=%v want=%v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Page remained blocked after cancellation")
	}
}

func sqlitePageChatID(t testing.TB, value string) model.ChatID {
	t.Helper()
	chatID, err := model.NewChatID(value)
	if err != nil {
		t.Fatal(err)
	}
	return chatID
}

func sqliteSequentialMessages(t testing.TB, chatID string, count int) []model.Message {
	t.Helper()
	messages := make([]model.Message, count)
	for index := range count {
		message, err := model.NewMessage(model.MessageInput{
			ChatID:    chatID,
			MessageID: fmt.Sprintf("message-%06d", index+1),
			Text:      fmt.Sprintf("bounded message %d", index+1),
			SentAt:    time.UnixMilli(int64(index + 1)).UTC(),
			FromMe:    index%2 == 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		messages[index] = message
	}
	return messages
}

func writeSQLitePageMessages(t testing.TB, store *SQLiteStore, messages []model.Message) {
	t.Helper()
	for start := 0; start < len(messages); start += sqliteMaxBatchWrites {
		end := min(start+sqliteMaxBatchWrites, len(messages))
		batch, err := model.NewWriteBatch(model.WriteRealtime, messages[start:end])
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Write(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
}

func collectSQLitePages(t testing.TB, store *SQLiteStore, chatID model.ChatID, pageSize int) []model.Message {
	t.Helper()
	var all []model.Message
	cursor := model.NoCursor()
	for {
		page, next, err := store.Page(context.Background(), chatID, cursor, pageSize)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
		if next.IsZero() {
			return all
		}
		cursor = next
	}
}
