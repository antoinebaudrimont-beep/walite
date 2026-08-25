package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	_ "modernc.org/sqlite"
)

// All application-cache timestamp columns use signed Unix milliseconds.
const (
	currentSQLiteSchemaVersion = 1
	defaultSQLiteBusyTimeout   = time.Second
	defaultSQLiteCacheKiB      = 2048
	messageKindUnknown         = 0
)

var (
	ErrUnsafeCachePath   = errors.New("unsafe cache path")
	ErrCorruptCache      = errors.New("corrupt cache")
	ErrUnsupportedSchema = errors.New("unsupported cache schema")
	ErrCacheIO           = errors.New("cache I/O failed")
	ErrStoreClosed       = errors.New("store closed")
	ErrMessageNotFound   = errors.New("message not found")
	ErrStoreRejected     = errors.New("store operation rejected")
)

// SQLiteOptions controls the application-cache database connection.
type SQLiteOptions struct {
	Path        string
	BusyTimeout time.Duration
}

// SQLiteStore owns walite's application-cache database. It deliberately does
// not expose the underlying database/sql handle.
type SQLiteStore struct {
	db       *sql.DB
	path     string
	closed   atomic.Bool
	closeOne sync.Once
	closeErr error
}

type sqliteError struct {
	kind  error
	cause error
}

func (err *sqliteError) Error() string {
	if err == nil || err.kind == nil {
		return "cache operation failed"
	}
	return err.kind.Error()
}

func (err *sqliteError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func (err *sqliteError) Is(target error) bool {
	return err != nil && (target == err.kind || errors.Is(err.cause, target))
}

type sqliteFilesystem struct {
	lstat    func(string) (os.FileInfo, error)
	mkdirAll func(string, os.FileMode) error
	chmod    func(string, os.FileMode) error
	openFile func(string, int, os.FileMode) (*os.File, error)
}

var operatingSystemFS = sqliteFilesystem{
	lstat:    os.Lstat,
	mkdirAll: os.MkdirAll,
	chmod:    os.Chmod,
	openFile: os.OpenFile,
}

type migrationHook func(*sql.Tx) error

// OpenSQLite securely opens or creates a schema-v1 application cache.
func OpenSQLite(ctx context.Context, options SQLiteOptions) (*SQLiteStore, error) {
	return openSQLite(ctx, options, operatingSystemFS, nil)
}

func openSQLite(ctx context.Context, options SQLiteOptions, filesystem sqliteFilesystem, hook migrationHook) (*SQLiteStore, error) {
	if err := sqliteContextError(ctx); err != nil {
		return nil, err
	}
	busyTimeout, err := normalizeSQLiteBusyTimeout(options.BusyTimeout)
	if err != nil {
		return nil, newSQLiteError(ErrStoreRejected, err)
	}
	path, existed, nonempty, err := prepareSQLitePath(options.Path, filesystem)
	if err != nil {
		return nil, err
	}
	if err := sqliteContextError(ctx); err != nil {
		return nil, err
	}

	databaseURL := (&url.URL{Scheme: "file", Path: path}).String()
	database, err := sql.Open("sqlite", databaseURL)
	if err != nil {
		return nil, wrapSQLiteOpenError(existed && nonempty, err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	database.SetConnMaxLifetime(0)
	database.SetConnMaxIdleTime(0)
	fail := func(kind error, cause error) (*SQLiteStore, error) {
		_ = database.Close()
		return nil, newSQLiteError(kind, cause)
	}
	if err := database.PingContext(ctx); err != nil {
		return wrapSQLiteOpenDatabaseError(database, existed && nonempty, err)
	}
	if existed && nonempty {
		if err := sqliteQuickCheck(ctx, database); err != nil {
			return fail(ErrCorruptCache, err)
		}
	}

	version, err := sqliteUserVersion(ctx, database)
	if err != nil {
		if existed && nonempty {
			return fail(ErrCorruptCache, err)
		}
		return fail(ErrCacheIO, err)
	}
	if version > currentSQLiteSchemaVersion {
		return fail(ErrUnsupportedSchema, nil)
	}
	if err := configureSQLite(ctx, database, busyTimeout); err != nil {
		return fail(ErrCacheIO, err)
	}
	if err := migrateSQLite(ctx, database, version, hook); err != nil {
		return fail(ErrCacheIO, err)
	}
	if err := tightenSQLiteFiles(path, filesystem); err != nil {
		return fail(ErrUnsafeCachePath, err)
	}
	return &SQLiteStore{db: database, path: path}, nil
}

func wrapSQLiteOpenError(corruptCandidate bool, err error) error {
	if isContextError(err) {
		return err
	}
	if corruptCandidate {
		return newSQLiteError(ErrCorruptCache, err)
	}
	return newSQLiteError(ErrCacheIO, err)
}

func wrapSQLiteOpenDatabaseError(database *sql.DB, corruptCandidate bool, err error) (*SQLiteStore, error) {
	_ = database.Close()
	return nil, wrapSQLiteOpenError(corruptCandidate, err)
}

func normalizeSQLiteBusyTimeout(timeout time.Duration) (int64, error) {
	if timeout == 0 {
		timeout = defaultSQLiteBusyTimeout
	}
	if timeout < time.Millisecond || timeout > time.Minute {
		return 0, ErrStoreRejected
	}
	return timeout.Milliseconds(), nil
}

func prepareSQLitePath(path string, filesystem sqliteFilesystem) (string, bool, bool, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", false, false, newSQLiteError(ErrUnsafeCachePath, nil)
	}
	path = filepath.Clean(path)
	directory := filepath.Dir(path)
	if directory == path || filepath.Base(path) == "." {
		return "", false, false, newSQLiteError(ErrUnsafeCachePath, nil)
	}
	if err := rejectExistingSymlinks(directory, filesystem); err != nil {
		return "", false, false, newSQLiteError(ErrUnsafeCachePath, err)
	}
	if err := filesystem.mkdirAll(directory, 0o700); err != nil {
		return "", false, false, newSQLiteError(ErrCacheIO, err)
	}
	if err := rejectExistingSymlinks(directory, filesystem); err != nil {
		return "", false, false, newSQLiteError(ErrUnsafeCachePath, err)
	}
	info, err := filesystem.lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !sqliteFileOwnedByCurrentUser(info) {
		return "", false, false, newSQLiteError(ErrUnsafeCachePath, err)
	}
	if err := filesystem.chmod(directory, 0o700); err != nil {
		return "", false, false, newSQLiteError(ErrCacheIO, err)
	}

	info, err = filesystem.lstat(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, false, newSQLiteError(ErrCacheIO, err)
	}
	if existed {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !sqliteFileOwnedByCurrentUser(info) {
			return "", true, false, newSQLiteError(ErrUnsafeCachePath, nil)
		}
		if err := filesystem.chmod(path, 0o600); err != nil {
			return "", true, info.Size() > 0, newSQLiteError(ErrCacheIO, err)
		}
		if err := tightenSQLiteFiles(path, filesystem); err != nil {
			return "", true, info.Size() > 0, newSQLiteError(ErrUnsafeCachePath, err)
		}
		return path, true, info.Size() > 0, nil
	}
	file, err := filesystem.openFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return "", false, false, newSQLiteError(ErrCacheIO, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", false, false, newSQLiteError(ErrCacheIO, err)
	}
	if err := file.Close(); err != nil {
		return "", false, false, newSQLiteError(ErrCacheIO, err)
	}
	if err := tightenSQLiteFiles(path, filesystem); err != nil {
		return "", false, false, newSQLiteError(ErrUnsafeCachePath, err)
	}
	return path, false, false, nil
}

func rejectExistingSymlinks(path string, filesystem sqliteFilesystem) error {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	current := volume + string(os.PathSeparator)
	for _, part := range strings.Split(strings.TrimPrefix(remainder, string(os.PathSeparator)), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := filesystem.lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafeCachePath
		}
		if !info.IsDir() {
			return ErrUnsafeCachePath
		}
	}
	return nil
}

func sqliteQuickCheck(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := false
	for rows.Next() {
		seen = true
		var result string
		if err := rows.Scan(&result); err != nil {
			return err
		}
		if result != "ok" {
			return ErrCorruptCache
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !seen {
		return ErrCorruptCache
	}
	return nil
}

func sqliteUserVersion(ctx context.Context, database *sql.DB) (int, error) {
	var version int
	if err := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	if version < 0 {
		return 0, ErrCorruptCache
	}
	return version, nil
}

func configureSQLite(ctx context.Context, database *sql.DB, busyMilliseconds int64) error {
	statements := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyMilliseconds),
		fmt.Sprintf("PRAGMA cache_size = -%d", defaultSQLiteCacheKiB),
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateSQLite(ctx context.Context, database *sql.DB, version int, hook migrationHook) error {
	if version == currentSQLiteSchemaVersion {
		return nil
	}
	if version != 0 {
		return ErrUnsupportedSchema
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	for _, statement := range sqliteSchemaV1 {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if hook != nil {
		if err := hook(transaction); err != nil {
			return err
		}
	}
	if _, err := transaction.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func tightenSQLiteFiles(path string, filesystem sqliteFilesystem) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := filesystem.lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !sqliteFileOwnedByCurrentUser(info) {
			return ErrUnsafeCachePath
		}
		if err := filesystem.chmod(candidate, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func sqliteContextError(ctx context.Context) error {
	if ctx == nil {
		return newSQLiteError(ErrStoreRejected, nil)
	}
	return ctx.Err()
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func newSQLiteError(kind, cause error) error {
	if isContextError(cause) {
		return cause
	}
	return &sqliteError{kind: kind, cause: cause}
}

func (store *SQLiteStore) checkOpen(ctx context.Context) error {
	if err := sqliteContextError(ctx); err != nil {
		return err
	}
	if store == nil || store.db == nil || store.closed.Load() {
		return newSQLiteError(ErrStoreClosed, nil)
	}
	return nil
}

// Close releases the application-cache database. It is safe to call more than once.
func (store *SQLiteStore) Close() error {
	if store == nil {
		return nil
	}
	store.closeOne.Do(func() {
		store.closed.Store(true)
		if store.db != nil {
			_ = tightenSQLiteFiles(store.path, operatingSystemFS)
			store.closeErr = store.db.Close()
		}
	})
	if store.closeErr != nil {
		return newSQLiteError(ErrCacheIO, store.closeErr)
	}
	return nil
}

// EnsureChat inserts or improves bounded chat metadata.
func (store *SQLiteStore) EnsureChat(ctx context.Context, chat model.Chat) error {
	if err := store.checkOpen(ctx); err != nil {
		return err
	}
	validated, err := model.NewChat(model.ChatInput{
		ID:          chat.ID().String(),
		DisplayName: chat.DisplayName(),
		Placeholder: chat.Placeholder(),
	})
	if err != nil {
		return newSQLiteError(ErrStoreRejected, err)
	}
	placeholder := 0
	if validated.Placeholder() {
		placeholder = 1
	}
	now := time.Now().UnixMilli()
	_, err = store.db.ExecContext(ctx, ensureChatSQL,
		validated.ID().String(), validated.DisplayName(), placeholder, now,
	)
	if err != nil {
		return sqliteOperationError(err)
	}
	if err := tightenSQLiteFiles(store.path, operatingSystemFS); err != nil {
		return newSQLiteError(ErrUnsafeCachePath, err)
	}
	return nil
}

// PutMessage writes one message through the bounded service write shape.
func (store *SQLiteStore) PutMessage(ctx context.Context, message model.Message) error {
	batch, err := model.NewWriteBatch(model.WriteRealtime, []model.Message{message})
	if err != nil {
		return newSQLiteError(ErrStoreRejected, err)
	}
	return store.Write(ctx, batch)
}

// Write atomically upserts one bounded model batch and creates placeholder
// chats before inserting messages that reference unknown chats.
func (store *SQLiteStore) Write(ctx context.Context, batch model.WriteBatch) error {
	if err := store.checkOpen(ctx); err != nil {
		return err
	}
	messages := make([]model.Message, batch.Len())
	for index := range messages {
		message, ok := batch.At(index)
		if !ok {
			return newSQLiteError(ErrStoreRejected, nil)
		}
		messages[index] = message
	}
	validated, err := model.NewWriteBatch(batch.Origin(), messages)
	if err != nil || validated.Len() != batch.Len() {
		return newSQLiteError(ErrStoreRejected, err)
	}
	if validated.Len() == 0 {
		return nil
	}

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return sqliteOperationError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	now := time.Now().UnixMilli()
	for index := 0; index < validated.Len(); index++ {
		message, _ := validated.At(index)
		if _, err := transaction.ExecContext(ctx, ensurePlaceholderChatSQL,
			message.ChatID().String(), message.SentAt().UnixMilli(), now,
		); err != nil {
			return sqliteOperationError(err)
		}
		body := any(nil)
		bodyBytes := 0
		retainedBody := 0
		if message.BodyRetained() {
			body = message.Text()
			bodyBytes = len(message.Text())
			retainedBody = 1
		}
		fromMe := 0
		if message.FromMe() {
			fromMe = 1
		}
		bodyTruncated := 0
		if message.BodyTruncated() {
			bodyTruncated = 1
		}
		result, err := transaction.ExecContext(ctx, upsertMessageSQL,
			message.ChatID().String(), message.MessageID().String(),
			message.SentAt().UnixMilli(), fromMe, messageKindUnknown, body,
			bodyBytes, bodyTruncated, retainedBody,
		)
		if err != nil {
			return sqliteOperationError(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return sqliteOperationError(err)
		}
		if changed != 1 {
			return newSQLiteError(ErrStoreRejected, nil)
		}
	}
	if err := transaction.Commit(); err != nil {
		return sqliteOperationError(err)
	}
	committed = true
	if err := tightenSQLiteFiles(store.path, operatingSystemFS); err != nil {
		return newSQLiteError(ErrUnsafeCachePath, err)
	}
	return nil
}

// Message loads one message by its composite application identity.
func (store *SQLiteStore) Message(ctx context.Context, chatID model.ChatID, messageID model.MessageID) (model.Message, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.Message{}, err
	}
	validatedChatID, err := model.NewChatID(chatID.String())
	if err != nil {
		return model.Message{}, newSQLiteError(ErrStoreRejected, err)
	}
	validatedMessageID, err := model.NewMessageID(messageID.String())
	if err != nil {
		return model.Message{}, newSQLiteError(ErrStoreRejected, err)
	}
	var (
		sentAt        int64
		fromMe        int
		body          sql.NullString
		bodyTruncated int
		retainedBody  int
	)
	err = store.db.QueryRowContext(ctx, selectMessageSQL,
		validatedChatID.String(), validatedMessageID.String(),
	).Scan(&sentAt, &fromMe, &body, &bodyTruncated, &retainedBody)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Message{}, newSQLiteError(ErrMessageNotFound, nil)
	}
	if err != nil {
		return model.Message{}, sqliteOperationError(err)
	}
	if fromMe < 0 || fromMe > 1 || bodyTruncated < 0 || bodyTruncated > 1 || retainedBody < 0 || retainedBody > 1 || (retainedBody == 1 && !body.Valid) {
		return model.Message{}, newSQLiteError(ErrCorruptCache, nil)
	}
	message, err := model.NewMessage(model.MessageInput{
		ChatID:    validatedChatID.String(),
		MessageID: validatedMessageID.String(),
		SentAt:    time.UnixMilli(sentAt).UTC(),
		FromMe:    fromMe == 1,
		Text:      body.String,
	})
	if err != nil {
		return model.Message{}, newSQLiteError(ErrCorruptCache, err)
	}
	if retainedBody == 0 {
		message = message.WithoutBody()
	}
	return message, nil
}

func sqliteOperationError(err error) error {
	if isContextError(err) {
		return err
	}
	return newSQLiteError(ErrCacheIO, err)
}

const ensureChatSQL = `
INSERT INTO chats(chat_id, display_name, placeholder, updated_at, ingest_seq)
VALUES (?, ?, ?, ?, 0)
ON CONFLICT(chat_id) DO UPDATE SET
    display_name = CASE
        WHEN excluded.placeholder = 0 OR chats.placeholder = 1 THEN excluded.display_name
        ELSE chats.display_name
    END,
    placeholder = CASE WHEN excluded.placeholder = 0 THEN 0 ELSE chats.placeholder END,
    updated_at = excluded.updated_at`

const ensurePlaceholderChatSQL = `
INSERT INTO chats(chat_id, placeholder, last_message_at, updated_at, ingest_seq)
VALUES (?, 1, ?, ?, 0)
ON CONFLICT(chat_id) DO UPDATE SET
    last_message_at = MAX(chats.last_message_at, excluded.last_message_at),
    updated_at = excluded.updated_at`

const upsertMessageSQL = `
INSERT INTO messages(
    chat_id, message_id, sent_at, from_me, kind, body, body_bytes,
    body_truncated, local_revision, ingest_seq, retained_body
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?)
ON CONFLICT(chat_id, message_id) DO UPDATE SET
    body = CASE
        WHEN messages.retained_body = 1 AND excluded.retained_body = 0 THEN messages.body
        ELSE excluded.body
    END,
    body_bytes = CASE
        WHEN messages.retained_body = 1 AND excluded.retained_body = 0 THEN messages.body_bytes
        ELSE excluded.body_bytes
    END,
    body_truncated = CASE
        WHEN messages.retained_body = 1 AND excluded.retained_body = 0 THEN messages.body_truncated
        ELSE excluded.body_truncated
    END,
    retained_body = MAX(messages.retained_body, excluded.retained_body)
WHERE messages.sent_at = excluded.sent_at AND messages.from_me = excluded.from_me`

const selectMessageSQL = `
SELECT sent_at, from_me, body, body_truncated, retained_body
FROM messages
WHERE chat_id = ? AND message_id = ?`
