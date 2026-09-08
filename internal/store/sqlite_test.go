package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func testSQLitePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "walite", "walite-cache.db")
}

func openTestSQLite(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	path := testSQLitePath(t)
	store, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
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

func testMessage(t *testing.T, chatID, messageID, text string, sentAt time.Time, fromMe bool) model.Message {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{
		ChatID: chatID, MessageID: messageID, Text: text, SentAt: sentAt, FromMe: fromMe,
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func rawSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestOpenSQLiteCreatesCurrentSchema(t *testing.T) {
	store, path := openTestSQLite(t)

	if got := pragmaInt(t, store.db, "user_version"); got != currentSQLiteSchemaVersion {
		t.Fatalf("user_version=%d", got)
	}
	wantTables := []string{"app_meta", "attachments", "chats", "contacts", "display_metadata", "messages", "sync_checkpoints"}
	if got := schemaNames(t, store.db, "table"); !equalStrings(got, wantTables) {
		t.Fatalf("tables=%v want=%v", got, wantTables)
	}
	wantIndexes := []string{
		"attachments_local_idx", "chats_recent_idx", "messages_page_idx",
		"messages_prune_idx", "sync_checkpoint_chat_idx",
	}
	gotIndexes := schemaNames(t, store.db, "index")
	for _, name := range wantIndexes {
		if !containsString(gotIndexes, name) {
			t.Fatalf("indexes=%v missing=%q", gotIndexes, name)
		}
	}
	var partialIndexSQL string
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT sql FROM sqlite_schema WHERE type='index' AND name='attachments_local_idx'",
	).Scan(&partialIndexSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(partialIndexSQL), "WHERE LOCAL_PATH IS NOT NULL") {
		t.Fatalf("partial index SQL=%q", partialIndexSQL)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSQLiteConfiguresRequiredPragmasAndSingleConnection(t *testing.T) {
	store, _ := openTestSQLite(t)
	if got := pragmaInt(t, store.db, "foreign_keys"); got != 1 {
		t.Fatalf("foreign_keys=%d", got)
	}
	if got := pragmaText(t, store.db, "journal_mode"); strings.ToLower(got) != "wal" {
		t.Fatalf("journal_mode=%q", got)
	}
	if got := pragmaInt(t, store.db, "synchronous"); got != 1 {
		t.Fatalf("synchronous=%d want=1 (NORMAL)", got)
	}
	if got := pragmaInt(t, store.db, "busy_timeout"); got != 1000 {
		t.Fatalf("busy_timeout=%d", got)
	}
	if got := pragmaInt(t, store.db, "cache_size"); got != -defaultSQLiteCacheKiB {
		t.Fatalf("cache_size=%d", got)
	}
	if got := store.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections=%d", got)
	}
}

func TestSQLiteMessageRoundTripAndPlaceholderChat(t *testing.T) {
	store, _ := openTestSQLite(t)
	want := testMessage(t, "chat-round-trip", "message-1", "Café 🙂", time.Date(2026, 8, 25, 11, 12, 13, 456000000, time.UTC), true)
	if err := store.PutMessage(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Message(context.Background(), want.ChatID(), want.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	assertMessagesEqual(t, got, want)

	var placeholder int
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT placeholder FROM chats WHERE chat_id=?", want.ChatID().String(),
	).Scan(&placeholder); err != nil {
		t.Fatal(err)
	}
	if placeholder != 1 {
		t.Fatalf("placeholder=%d", placeholder)
	}
	var senderID string
	var kind int
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT sender_id, kind FROM messages WHERE chat_id=? AND message_id=?",
		want.ChatID().String(), want.MessageID().String(),
	).Scan(&senderID, &kind); err != nil {
		t.Fatal(err)
	}
	if senderID != "" || kind != messageKindUnknown {
		t.Fatalf("neutral message fields=(%q,%d)", senderID, kind)
	}
}

func TestSQLiteBodylessMessageRoundTrip(t *testing.T) {
	store, _ := openTestSQLite(t)
	want := testMessage(t, "chat-bodyless", "message-1", "discarded", time.UnixMilli(42).UTC(), false).WithoutBody()
	if err := store.PutMessage(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Message(context.Background(), want.ChatID(), want.MessageID())
	if err != nil {
		t.Fatal(err)
	}
	assertMessagesEqual(t, got, want)
}

func TestSQLiteEnsureChatImprovesPlaceholder(t *testing.T) {
	store, _ := openTestSQLite(t)
	message := testMessage(t, "chat-improved", "message-1", "hello", time.Now().UTC(), false)
	if err := store.PutMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	chat, err := model.NewChat(model.ChatInput{ID: "chat-improved", DisplayName: "Project Room"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	var displayName string
	var placeholder int
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT display_name, placeholder FROM chats WHERE chat_id=?", chat.ID().String(),
	).Scan(&displayName, &placeholder); err != nil {
		t.Fatal(err)
	}
	if displayName != "Project Room" || placeholder != 0 {
		t.Fatalf("chat=(%q,%d)", displayName, placeholder)
	}
}

func TestSQLiteRepeatedIdenticalUpsertDoesNotDuplicate(t *testing.T) {
	store, _ := openTestSQLite(t)
	message := testMessage(t, "chat-idempotent", "message-1", "same", time.Now().UTC(), false)
	for range 3 {
		if err := store.PutMessage(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); got != 1 {
		t.Fatalf("message count=%d", got)
	}
}

func TestSQLiteWriteRollsBackWholeBatch(t *testing.T) {
	store, _ := openTestSQLite(t)
	if _, err := store.db.ExecContext(context.Background(), `
CREATE TRIGGER synthetic_write_failure
BEFORE INSERT ON messages
WHEN NEW.message_id = 'message-fail'
BEGIN
    SELECT RAISE(ABORT, 'synthetic failure');
END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	messages := []model.Message{
		testMessage(t, "chat-rollback", "message-ok", "first", now, false),
		testMessage(t, "chat-rollback", "message-fail", "second", now.Add(time.Millisecond), false),
	}
	batch, err := model.NewWriteBatch(model.WriteRealtime, messages)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), batch); !errors.Is(err, ErrCacheIO) {
		t.Fatalf("Write error=%v", err)
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); got != 0 {
		t.Fatalf("message count=%d after rollback", got)
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM chats WHERE chat_id='chat-rollback'"); got != 0 {
		t.Fatalf("placeholder count=%d after rollback", got)
	}
}

func TestSQLiteMigrationRollbackLeavesVersionZero(t *testing.T) {
	path := testSQLitePath(t)
	synthetic := errors.New("synthetic migration failure")
	_, err := openSQLite(context.Background(), SQLiteOptions{Path: path}, operatingSystemFS, func(*sql.Tx) error {
		return synthetic
	})
	if !errors.Is(err, synthetic) || !errors.Is(err, ErrCacheIO) {
		t.Fatalf("Open error=%v", err)
	}
	database := rawSQLite(t, path)
	if got := pragmaInt(t, database, "user_version"); got != 0 {
		t.Fatalf("user_version=%d", got)
	}
	if got := queryCount(t, database, "SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='messages'"); got != 0 {
		t.Fatalf("messages table count=%d", got)
	}
}

func TestOpenSQLiteRejectsFutureSchemaWithoutChangingIt(t *testing.T) {
	path := testSQLitePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	database := rawSQLite(t, path)
	if _, err := database.ExecContext(context.Background(), fmt.Sprintf("PRAGMA user_version = %d", currentSQLiteSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path}); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Open error=%v", err)
	}
	verify := rawSQLite(t, path)
	if got := pragmaInt(t, verify, "user_version"); got != currentSQLiteSchemaVersion+1 {
		t.Fatalf("user_version=%d", got)
	}
}

func TestOpenSQLiteCorruptFileReturnsControlledErrorWithoutChangingFile(t *testing.T) {
	path := testSQLitePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("synthetic non-SQLite bytes")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path}); !errors.Is(err, ErrCorruptCache) {
		t.Fatalf("Open error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("corrupt cache was modified")
	}
}

func TestOpenSQLiteInitializesMissingAndZeroLengthFiles(t *testing.T) {
	for _, test := range []struct {
		name       string
		createZero bool
	}{
		{name: "missing"},
		{name: "zero length", createZero: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := testSQLitePath(t)
			if test.createZero {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			store, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if got := pragmaInt(t, store.db, "user_version"); got != currentSQLiteSchemaVersion {
				t.Fatalf("user_version=%d", got)
			}
		})
	}
}

func TestOpenSQLiteRejectsUnsafePathTypes(t *testing.T) {
	t.Run("relative path", func(t *testing.T) {
		if _, err := OpenSQLite(context.Background(), SQLiteOptions{Path: "cache/walite.db"}); !errors.Is(err, ErrUnsafeCachePath) {
			t.Fatalf("Open error=%v", err)
		}
	})
	t.Run("database is directory", func(t *testing.T) {
		path := testSQLitePath(t)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path}); !errors.Is(err, ErrUnsafeCachePath) {
			t.Fatalf("Open error=%v", err)
		}
	})
}

func TestOpenSQLiteRejectsSymlinkedDirectoryAndDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenSQLite(context.Background(), SQLiteOptions{Path: filepath.Join(link, "walite.db")}); !errors.Is(err, ErrUnsafeCachePath) {
			t.Fatalf("Open error=%v", err)
		}
	})
	t.Run("database", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target.db")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "walite.db")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenSQLite(context.Background(), SQLiteOptions{Path: link}); !errors.Is(err, ErrUnsafeCachePath) {
			t.Fatalf("Open error=%v", err)
		}
	})
	t.Run("WAL sidecar", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "walite.db")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "target-wal")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path+"-wal"); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path}); !errors.Is(err, ErrUnsafeCachePath) {
			t.Fatalf("Open error=%v", err)
		}
	})
}

func TestOpenSQLiteTightensPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not supported")
	}
	directory := filepath.Join(t.TempDir(), "walite")
	path := filepath.Join(directory, "walite-cache.db")
	if err := os.Mkdir(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertPermissions(t, directory, 0o700)
	assertPermissions(t, path, 0o600)
	message := testMessage(t, "chat-modes", "message-1", "private", time.Now().UTC(), false)
	if err := store.PutMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); errors.Is(err, os.ErrNotExist) {
			t.Logf("SQLite did not retain optional sidecar %s", filepath.Base(sidecar))
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		assertPermissions(t, sidecar, 0o600)
	}
}

func TestSQLiteCancellationAndCloseBehavior(t *testing.T) {
	store, _ := openTestSQLite(t)
	message := testMessage(t, "chat-context", "message-1", "cancel", time.Now().UTC(), false)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.PutMessage(canceled, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutMessage error=%v", err)
	}
	if got := queryCount(t, store.db, "SELECT COUNT(*) FROM messages"); got != 0 {
		t.Fatalf("message count=%d", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close error=%v", err)
	}
	if err := store.PutMessage(context.Background(), message); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed PutMessage error=%v", err)
	}
}

func TestOpenSQLiteRepeatedOpenClose(t *testing.T) {
	path := testSQLitePath(t)
	for range 3 {
		store, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteConcurrentAccessUsesBoundedPool(t *testing.T) {
	store, _ := openTestSQLite(t)
	const workers = 12
	var wait sync.WaitGroup
	errorsSeen := make(chan error, workers)
	for index := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			id := fmt.Sprintf("message-%d", index)
			message, err := model.NewMessage(model.MessageInput{
				ChatID: "chat-concurrent", MessageID: id, Text: id,
				SentAt: time.UnixMilli(int64(index + 1)).UTC(),
			})
			if err != nil {
				errorsSeen <- err
				return
			}
			if err := store.PutMessage(context.Background(), message); err != nil {
				errorsSeen <- err
				return
			}
			if _, err := store.Message(context.Background(), message.ChatID(), message.MessageID()); err != nil {
				errorsSeen <- err
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	if got := store.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections=%d", got)
	}
}

func TestSQLiteErrorTextDoesNotExposeContent(t *testing.T) {
	store, _ := openTestSQLite(t)
	secretID := "private-chat-identifier"
	chatID, err := model.NewChatID(secretID)
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := model.NewMessageID("missing-message")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Message(context.Background(), chatID, messageID)
	if !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("Message error=%v", err)
	}
	if strings.Contains(err.Error(), secretID) || strings.Contains(err.Error(), "SELECT") {
		t.Fatalf("error exposed content: %q", err)
	}
}

func pragmaInt(t *testing.T, database *sql.DB, name string) int {
	t.Helper()
	var value int
	if err := database.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func pragmaText(t *testing.T, database *sql.DB, name string) string {
	t.Helper()
	var value string
	if err := database.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func schemaNames(t *testing.T, database *sql.DB, kind string) []string {
	t.Helper()
	rows, err := database.QueryContext(context.Background(),
		"SELECT name FROM sqlite_schema WHERE type=? AND name NOT LIKE 'sqlite_%' ORDER BY name", kind,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return names
}

func queryCount(t *testing.T, database *sql.DB, query string) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertMessagesEqual(t *testing.T, got, want model.Message) {
	t.Helper()
	if got.ChatID().String() != want.ChatID().String() ||
		got.MessageID().String() != want.MessageID().String() ||
		!got.SentAt().Equal(want.SentAt()) || got.FromMe() != want.FromMe() ||
		got.Text() != want.Text() || got.Media() != want.Media() || got.BodyRetained() != want.BodyRetained() {
		t.Fatalf("message round trip got=(%q,%q,%v,%t,%q,%+v,%t) want=(%q,%q,%v,%t,%q,%+v,%t)",
			got.ChatID().String(), got.MessageID().String(), got.SentAt(), got.FromMe(), got.Text(), got.Media(), got.BodyRetained(),
			want.ChatID().String(), want.MessageID().String(), want.SentAt(), want.FromMe(), want.Text(), want.Media(), want.BodyRetained())
	}
}

func assertPermissions(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("permissions for %s=%#o want=%#o", filepath.Base(path), got, want)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
