package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLiteContactUpsertInsertUpdateIdempotenceAndOrdering(t *testing.T) {
	store, _ := openTestSQLite(t)
	initial := sqliteTestContact(t, "contact-lifecycle", "Initial Name", 100, 1)
	for range 3 {
		if err := store.UpsertContact(context.Background(), initial); err != nil {
			t.Fatal(err)
		}
	}
	newerByTime := sqliteTestContact(t, "contact-lifecycle", "Time-Ordered Name", 150, 1)
	if err := store.UpsertContact(context.Background(), newerByTime); err != nil {
		t.Fatal(err)
	}
	newer := sqliteTestContact(t, "contact-lifecycle", "Current Name", 200, 2)
	if err := store.UpsertContact(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	stale := sqliteTestContact(t, "contact-lifecycle", "Stale Name", 300, 1)
	if err := store.UpsertContact(context.Background(), stale); err != nil {
		t.Fatal(err)
	}

	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM contacts"); got != 1 {
		t.Fatalf("contact count=%d want=1", got)
	}
	got, err := store.Contact(context.Background(), initial.ID())
	if err != nil {
		t.Fatal(err)
	}
	assertSQLiteContactsEqual(t, got, newer)
}

func TestSQLiteChatUpsertInsertUpdateIdempotenceAndOrdering(t *testing.T) {
	store, _ := openTestSQLite(t)
	contact := sqliteTestContact(t, "contact-chat", "Chat Contact", 50, 1)
	if err := store.UpsertContact(context.Background(), contact); err != nil {
		t.Fatal(err)
	}
	initial := sqliteTestChat(t, sqliteChatInput{
		ID: "chat-lifecycle", ContactID: contact.ID().String(), DisplayName: "Initial Chat",
		LastMessageMillis: 10, UnreadCount: 2, UpdatedMillis: 100, IngestSeq: 1,
	})
	for range 3 {
		if err := store.EnsureChat(context.Background(), initial); err != nil {
			t.Fatal(err)
		}
	}
	newerByTime := sqliteTestChat(t, sqliteChatInput{
		ID: "chat-lifecycle", ContactID: contact.ID().String(), DisplayName: "Time-Ordered Chat",
		LastMessageMillis: 15, UnreadCount: 3, UpdatedMillis: 150, IngestSeq: 1,
	})
	if err := store.EnsureChat(context.Background(), newerByTime); err != nil {
		t.Fatal(err)
	}
	newer := sqliteTestChat(t, sqliteChatInput{
		ID: "chat-lifecycle", ContactID: contact.ID().String(), DisplayName: "Current Chat",
		LastMessageMillis: 20, UnreadCount: 4, Muted: true, Archived: true,
		UpdatedMillis: 200, IngestSeq: 2,
	})
	if err := store.EnsureChat(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	stale := sqliteTestChat(t, sqliteChatInput{
		ID: "chat-lifecycle", ContactID: contact.ID().String(), DisplayName: "Stale Chat",
		IsGroup: true, LastMessageMillis: 30, UnreadCount: 9,
		UpdatedMillis: 300, IngestSeq: 1,
	})
	if err := store.EnsureChat(context.Background(), stale); err != nil {
		t.Fatal(err)
	}

	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM chats"); got != 1 {
		t.Fatalf("chat count=%d want=1", got)
	}
	got, err := store.Chat(context.Background(), initial.ID())
	if err != nil {
		t.Fatal(err)
	}
	assertSQLiteChatsEqual(t, got, newer)
}

func TestSQLiteNewContactStartsChatMetadataFirst(t *testing.T) {
	store, _ := openTestSQLite(t)
	contact := sqliteTestContact(t, "contact-new-person", "New Person", 100, 1)
	chat := sqliteTestChat(t, sqliteChatInput{
		ID: "chat-new-person", ContactID: contact.ID().String(), DisplayName: "New Person",
		UpdatedMillis: 110, IngestSeq: 2,
	})
	message := testMessage(t, chat.ID().String(), "message-first", "Hello from a new contact", time.UnixMilli(120).UTC(), false)

	if err := store.UpsertContact(context.Background(), contact); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}

	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM contacts"); got != 1 {
		t.Fatalf("contact count=%d want=1", got)
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM chats"); got != 1 {
		t.Fatalf("chat count=%d want=1", got)
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); got != 1 {
		t.Fatalf("message count=%d want=1", got)
	}
	gotChat, err := store.Chat(context.Background(), chat.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !gotChat.HasContact() || gotChat.ContactID().String() != contact.ID().String() ||
		gotChat.DisplayName() != chat.DisplayName() || gotChat.Placeholder() ||
		gotChat.UnreadCount() != 1 || !gotChat.LastMessageAt().Equal(message.SentAt()) {
		t.Fatalf("stored chat=(contact=%q,name=%q,placeholder=%t,unread=%d,last=%v)",
			gotChat.ContactID().String(), gotChat.DisplayName(), gotChat.Placeholder(), gotChat.UnreadCount(), gotChat.LastMessageAt())
	}
	gotMessage, err := store.Message(context.Background(), message.ChatID(), message.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	assertMessagesEqual(t, gotMessage, message)
	page, next, err := store.Page(context.Background(), chat.ID(), model.NoCursor(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || !next.IsZero() || page[0].MessageID().String() != message.MessageID().String() {
		t.Fatalf("pagination=(len=%d,next=%v)", len(page), next)
	}
	assertSQLiteForeignKeysValid(t, store)
}

func TestSQLiteMessageBeforeMetadataUpgradesPlaceholder(t *testing.T) {
	store, _ := openTestSQLite(t)
	message := testMessage(t, "chat-message-first", "message-before-metadata", "metadata follows", time.UnixMilli(500).UTC(), false)
	if err := store.PutMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	placeholder, err := store.Chat(context.Background(), message.ChatID())
	if err != nil {
		t.Fatal(err)
	}
	if !placeholder.Placeholder() || placeholder.UnreadCount() != 1 {
		t.Fatalf("placeholder=(%t, unread=%d)", placeholder.Placeholder(), placeholder.UnreadCount())
	}

	contact := sqliteTestContact(t, "contact-message-first", "Late Contact", 400, 1)
	chat := sqliteTestChat(t, sqliteChatInput{
		ID: message.ChatID().String(), ContactID: contact.ID().String(), DisplayName: "Late Contact",
		UpdatedMillis: 450, IngestSeq: 2,
	})
	if err := store.UpsertContact(context.Background(), contact); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}

	gotChat, err := store.Chat(context.Background(), chat.ID())
	if err != nil {
		t.Fatal(err)
	}
	if gotChat.Placeholder() || !gotChat.HasContact() || gotChat.ContactID().String() != contact.ID().String() ||
		gotChat.DisplayName() != "Late Contact" || gotChat.UnreadCount() != 1 ||
		!gotChat.LastMessageAt().Equal(message.SentAt()) {
		t.Fatalf("upgraded chat=(placeholder=%t,contact=%q,name=%q,unread=%d,last=%v)",
			gotChat.Placeholder(), gotChat.ContactID().String(), gotChat.DisplayName(), gotChat.UnreadCount(), gotChat.LastMessageAt())
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM chats"); got != 1 {
		t.Fatalf("chat count=%d want=1", got)
	}
	gotMessage, err := store.Message(context.Background(), message.ChatID(), message.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	assertMessagesEqual(t, gotMessage, message)
	assertSQLiteForeignKeysValid(t, store)
}

func TestSQLiteGroupChatNeedsNoContact(t *testing.T) {
	store, _ := openTestSQLite(t)
	chat := sqliteTestChat(t, sqliteChatInput{
		ID: "group-no-contact", DisplayName: "Synthetic Group", IsGroup: true,
		UpdatedMillis: 100, IngestSeq: 1,
	})
	if err := store.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	message := testMessage(t, chat.ID().String(), "group-message", "Group hello", time.UnixMilli(200).UTC(), false)
	if err := store.PutMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}

	got, err := store.Chat(context.Background(), chat.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsGroup() || got.HasContact() || got.ContactID().String() != "" ||
		got.DisplayName() != chat.DisplayName() || got.Placeholder() {
		t.Fatalf("group=(is_group=%t,has_contact=%t,contact=%q,name=%q,placeholder=%t)",
			got.IsGroup(), got.HasContact(), got.ContactID().String(), got.DisplayName(), got.Placeholder())
	}
	if gotCount := queryCount(t, store.db, "SELECT COUNT(*) FROM contacts"); gotCount != 0 {
		t.Fatalf("contact count=%d want=0", gotCount)
	}
	assertSQLiteForeignKeysValid(t, store)
}

func TestSQLiteEntityIngestionEndToEndIdempotence(t *testing.T) {
	store, _ := openTestSQLite(t)
	contact := sqliteTestContact(t, "contact-idempotent-entity", "Idempotent Contact", 100, 1)
	chat := sqliteTestChat(t, sqliteChatInput{
		ID: "chat-idempotent-entity", ContactID: contact.ID().String(), DisplayName: "Idempotent Contact",
		UpdatedMillis: 110, IngestSeq: 2,
	})
	message := testMessage(t, chat.ID().String(), "message-idempotent-entity", "one logical message", time.UnixMilli(120).UTC(), false)
	for range 3 {
		if err := store.UpsertContact(context.Background(), contact); err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureChat(context.Background(), chat); err != nil {
			t.Fatal(err)
		}
		if err := store.PutMessage(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if contacts := queryCount(t, store.db, "SELECT COUNT(*) FROM contacts"); contacts != 1 {
		t.Fatalf("contact count=%d want=1", contacts)
	}
	if chats := queryCount(t, store.db, "SELECT COUNT(*) FROM chats"); chats != 1 {
		t.Fatalf("chat count=%d want=1", chats)
	}
	if messages := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); messages != 1 {
		t.Fatalf("message count=%d want=1", messages)
	}
	got, err := store.Chat(context.Background(), chat.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.UnreadCount() != 1 {
		t.Fatalf("unread=%d want=1", got.UnreadCount())
	}
}

func TestSQLiteMessageActivityUnreadAndDuplicateSemantics(t *testing.T) {
	store, _ := openTestSQLite(t)
	chat := sqliteTestChat(t, sqliteChatInput{ID: "chat-activity", DisplayName: "Activity", UpdatedMillis: 10, IngestSeq: 1})
	if err := store.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	incoming := testMessage(t, chat.ID().String(), "incoming-new", "incoming", time.UnixMilli(100).UTC(), false)
	if err := store.PutMessage(context.Background(), incoming); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMessage(context.Background(), incoming); err != nil {
		t.Fatal(err)
	}
	assertSQLiteChatActivity(t, store, chat.ID(), 1, incoming.SentAt())
	fromMe := testMessage(t, chat.ID().String(), "outgoing-new", "outgoing", time.UnixMilli(200).UTC(), true)
	if err := store.PutMessage(context.Background(), fromMe); err != nil {
		t.Fatal(err)
	}
	assertSQLiteChatActivity(t, store, chat.ID(), 1, fromMe.SentAt())
	olderIncoming := testMessage(t, chat.ID().String(), "incoming-delayed", "delayed", time.UnixMilli(50).UTC(), false)
	if err := store.PutMessage(context.Background(), olderIncoming); err != nil {
		t.Fatal(err)
	}
	assertSQLiteChatActivity(t, store, chat.ID(), 2, fromMe.SentAt())
	historyIncoming := testMessage(t, chat.ID().String(), "history-message", "history", time.UnixMilli(300).UTC(), false)
	historyBatch, err := model.NewWriteBatch(model.WriteHistory, []model.Message{historyIncoming})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), historyBatch); err != nil {
		t.Fatal(err)
	}

	assertSQLiteChatActivity(t, store, chat.ID(), 2, historyIncoming.SentAt())
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 4 {
		t.Fatalf("message count=%d want=4", count)
	}
}

func TestSQLiteMessageIngestionRollbackPreservesChatState(t *testing.T) {
	store, _ := openTestSQLite(t)
	chat := sqliteTestChat(t, sqliteChatInput{
		ID: "chat-ingest-rollback", DisplayName: "Rollback", LastMessageMillis: 50,
		UnreadCount: 1, UpdatedMillis: 60, IngestSeq: 1,
	})
	if err := store.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(context.Background(), `
CREATE TRIGGER synthetic_ingest_failure
BEFORE INSERT ON messages
WHEN NEW.message_id = 'message-ingest-fail'
BEGIN
    SELECT RAISE(ABORT, 'synthetic failure');
END`); err != nil {
		t.Fatal(err)
	}
	failing := testMessage(t, chat.ID().String(), "message-ingest-fail", "private synthetic body", time.UnixMilli(100).UTC(), false)
	err := store.PutMessage(context.Background(), failing)
	if !errors.Is(err, ErrCacheIO) {
		t.Fatalf("PutMessage error=%v", err)
	}
	if strings.Contains(err.Error(), failing.Text()) || strings.Contains(err.Error(), failing.MessageID().String()) {
		t.Fatalf("ingestion error disclosed private values: %q", err)
	}

	got, err := store.Chat(context.Background(), chat.ID())
	if err != nil {
		t.Fatal(err)
	}
	assertSQLiteChatsEqual(t, got, chat)
	if count := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); count != 0 {
		t.Fatalf("message count=%d after rollback", count)
	}
}

func TestSQLiteEntityWritesPreserveWriterBounds(t *testing.T) {
	contact := sqliteTestContact(t, "contact-bounds", "Bounds", 1, 1)
	chat := sqliteTestChat(t, sqliteChatInput{ID: "chat-bounds-entity", UpdatedMillis: 1, IngestSeq: 1})
	if newPendingContact(contact).logicalWrites() != 1 || newPendingChat(chat).logicalWrites() != 1 {
		t.Fatal("contact/chat metadata requests must each consume one logical writer slot")
	}
	if sqliteWriteQueueCapacity != 64 || sqliteMaxBatchWrites != 50 || sqliteMaxBatchAge != 25*time.Millisecond {
		t.Fatalf("writer bounds queue=%d batch=%d age=%v", sqliteWriteQueueCapacity, sqliteMaxBatchWrites, sqliteMaxBatchAge)
	}
}

type sqliteChatInput struct {
	ID, ContactID, DisplayName            string
	IsGroup, Muted, Archived, Placeholder bool
	LastMessageMillis, UpdatedMillis      int64
	UnreadCount                           uint32
	IngestSeq                             uint64
}

func sqliteTestContact(t testing.TB, id, name string, updatedMillis int64, ingestSeq uint64) model.Contact {
	t.Helper()
	contact, err := model.NewContact(model.ContactInput{
		ID: id, DisplayName: name, UpdatedAt: time.UnixMilli(updatedMillis).UTC(), IngestSeq: ingestSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	return contact
}

func sqliteTestChat(t testing.TB, input sqliteChatInput) model.Chat {
	t.Helper()
	lastMessageAt := time.Time{}
	if input.LastMessageMillis != 0 {
		lastMessageAt = time.UnixMilli(input.LastMessageMillis).UTC()
	}
	updatedAt := time.Time{}
	if input.UpdatedMillis != 0 {
		updatedAt = time.UnixMilli(input.UpdatedMillis).UTC()
	}
	chat, err := model.NewChat(model.ChatInput{
		ID: input.ID, ContactID: input.ContactID, DisplayName: input.DisplayName,
		IsGroup: input.IsGroup, LastMessageAt: lastMessageAt, UnreadCount: input.UnreadCount,
		Muted: input.Muted, Archived: input.Archived, Placeholder: input.Placeholder,
		UpdatedAt: updatedAt, IngestSeq: input.IngestSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	return chat
}

func assertSQLiteContactsEqual(t testing.TB, got, want model.Contact) {
	t.Helper()
	if got.ID().String() != want.ID().String() || got.DisplayName() != want.DisplayName() ||
		got.NameTruncated() != want.NameTruncated() || !got.UpdatedAt().Equal(want.UpdatedAt()) ||
		got.IngestSeq() != want.IngestSeq() {
		t.Fatalf("contact got=(%q,%q,%t,%v,%d) want=(%q,%q,%t,%v,%d)",
			got.ID().String(), got.DisplayName(), got.NameTruncated(), got.UpdatedAt(), got.IngestSeq(),
			want.ID().String(), want.DisplayName(), want.NameTruncated(), want.UpdatedAt(), want.IngestSeq())
	}
}

func assertSQLiteChatsEqual(t testing.TB, got, want model.Chat) {
	t.Helper()
	if got.ID().String() != want.ID().String() || got.HasContact() != want.HasContact() ||
		got.ContactID().String() != want.ContactID().String() || got.DisplayName() != want.DisplayName() ||
		got.IsGroup() != want.IsGroup() || !got.LastMessageAt().Equal(want.LastMessageAt()) ||
		got.UnreadCount() != want.UnreadCount() || got.Muted() != want.Muted() ||
		got.Archived() != want.Archived() || got.Placeholder() != want.Placeholder() ||
		!got.UpdatedAt().Equal(want.UpdatedAt()) || got.IngestSeq() != want.IngestSeq() {
		t.Fatalf("chat got=(%q,%q,%q,%t,%v,%d,%t,%t,%t,%v,%d) want=(%q,%q,%q,%t,%v,%d,%t,%t,%t,%v,%d)",
			got.ID().String(), got.ContactID().String(), got.DisplayName(), got.IsGroup(), got.LastMessageAt(), got.UnreadCount(), got.Muted(), got.Archived(), got.Placeholder(), got.UpdatedAt(), got.IngestSeq(),
			want.ID().String(), want.ContactID().String(), want.DisplayName(), want.IsGroup(), want.LastMessageAt(), want.UnreadCount(), want.Muted(), want.Archived(), want.Placeholder(), want.UpdatedAt(), want.IngestSeq())
	}
}

func assertSQLiteForeignKeysValid(t testing.TB, store *SQLiteStore) {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check returned a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertSQLiteChatActivity(t testing.TB, store *SQLiteStore, chatID model.ChatID, unread uint32, lastMessageAt time.Time) {
	t.Helper()
	chat, err := store.Chat(context.Background(), chatID)
	if err != nil {
		t.Fatal(err)
	}
	if chat.UnreadCount() != unread || !chat.LastMessageAt().Equal(lastMessageAt) {
		t.Fatalf("chat activity=(unread=%d,last=%v) want=(unread=%d,last=%v)",
			chat.UnreadCount(), chat.LastMessageAt(), unread, lastMessageAt)
	}
}
