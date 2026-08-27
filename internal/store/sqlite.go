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
	ErrContactNotFound   = errors.New("contact not found")
	ErrChatNotFound      = errors.New("chat not found")
	ErrMessageNotFound   = errors.New("message not found")
	ErrStoreRejected     = errors.New("store operation rejected")
	ErrBusy              = errors.New("cache busy")
	ErrDiskFull          = errors.New("cache storage unavailable")
	ErrCachePressure     = errors.New("cache remains over budget")
)

const maxSQLiteUnreadCount = uint32(^uint32(0))

// SQLiteOptions controls the application-cache database connection.
type SQLiteOptions struct {
	Path        string
	BusyTimeout time.Duration

	cacheBudgetBytes int64
	cacheStat        func(string) (os.FileInfo, error)
	checkpoint       sqliteCheckpointFunc
	pruneHooks       sqlitePruneHooks
}

// SQLiteStore owns walite's application-cache database. It deliberately does
// not expose the underlying database/sql handle.
type SQLiteStore struct {
	db       *sql.DB
	path     string
	closed   atomic.Bool
	closeOne sync.Once
	closeErr error
	writer   *sqliteBatchWriter

	maintenance chan struct{}
	cacheBudget int64
	cacheStat   func(string) (os.FileInfo, error)
	checkpoint  sqliteCheckpointFunc
	pruneHooks  sqlitePruneHooks
	degradation atomic.Uint32
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
	return openSQLiteWithWriter(ctx, options, filesystem, hook, sqliteWriterOptions{})
}

func openSQLiteWithWriter(ctx context.Context, options SQLiteOptions, filesystem sqliteFilesystem, hook migrationHook, writerOptions sqliteWriterOptions) (*SQLiteStore, error) {
	if err := sqliteContextError(ctx); err != nil {
		return nil, err
	}
	cacheBudget := options.cacheBudgetBytes
	if cacheBudget == 0 {
		cacheBudget = DefaultSQLiteCacheBudgetBytes
	}
	if cacheBudget < 0 {
		return nil, newSQLiteError(ErrStoreRejected, nil)
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
	cacheStat := options.cacheStat
	if cacheStat == nil {
		cacheStat = os.Lstat
	}
	checkpoint := options.checkpoint
	if checkpoint == nil {
		checkpoint = sqliteTruncateCheckpoint
	}
	store := &SQLiteStore{
		db:          database,
		path:        path,
		maintenance: make(chan struct{}, 1),
		cacheBudget: cacheBudget,
		cacheStat:   cacheStat,
		checkpoint:  checkpoint,
		pruneHooks:  options.pruneHooks,
	}
	store.maintenance <- struct{}{}
	store.degradation.Store(uint32(CacheNormal))
	store.writer = newSQLiteBatchWriter(store, writerOptions)
	go store.writer.run()
	return store, nil
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
		if store.writer != nil {
			store.writer.shutdown()
		}
		if store.db != nil {
			_ = store.acquireMaintenance(context.Background())
			defer store.releaseMaintenance()
			_ = tightenSQLiteFiles(store.path, operatingSystemFS)
			store.closeErr = store.db.Close()
		}
	})
	if store.closeErr != nil {
		return newSQLiteError(ErrCacheIO, store.closeErr)
	}
	return nil
}

// UpsertContact inserts or updates bounded contact metadata through the single
// SQLite writer.
func (store *SQLiteStore) UpsertContact(ctx context.Context, contact model.Contact) error {
	if err := store.checkOpen(ctx); err != nil {
		return err
	}
	validated, err := normalizeContact(contact)
	if err != nil {
		return newSQLiteError(ErrStoreRejected, err)
	}
	return store.writer.submit(ctx, newPendingContact(validated))
}

// EnsureChat inserts or improves bounded chat metadata through the single
// SQLite writer.
func (store *SQLiteStore) EnsureChat(ctx context.Context, chat model.Chat) error {
	if err := store.checkOpen(ctx); err != nil {
		return err
	}
	validated, err := normalizeChat(chat)
	if err != nil {
		return newSQLiteError(ErrStoreRejected, err)
	}
	return store.writer.submit(ctx, newPendingChat(validated))
}

// PutMessage submits one message to the bounded writer and waits for commit.
func (store *SQLiteStore) PutMessage(ctx context.Context, message model.Message) error {
	return store.SubmitMessage(ctx, message)
}

// SubmitMessage submits one message to the bounded writer and waits for its
// transaction to commit. Once enqueued, caller cancellation stops only the
// acknowledgement wait; the accepted write remains owned by the writer.
func (store *SQLiteStore) SubmitMessage(ctx context.Context, message model.Message) error {
	if err := store.checkOpen(ctx); err != nil {
		return err
	}
	batch, err := model.NewWriteBatch(model.WriteRealtime, []model.Message{message})
	if err != nil {
		return newSQLiteError(ErrStoreRejected, err)
	}
	return store.writer.submit(ctx, newPendingMessages(batch))
}

// Write submits one already-bounded service batch and waits for commit.
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
	return store.writer.submit(ctx, newPendingMessages(validated))
}

func (store *SQLiteStore) commitPendingWrites(requests *[sqliteMaxBatchWrites]pendingWrite, count int) (duration time.Duration, resultErr error) {
	started := time.Now()
	ctx := context.Background()
	defer func() {
		if resultErr != nil {
			store.observeSQLiteFailure(resultErr)
		} else {
			store.observeSQLiteWriteSuccess()
		}
	}()
	if err := store.acquireMaintenance(ctx); err != nil {
		return time.Since(started), err
	}
	defer store.releaseMaintenance()
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Since(started), sqliteOperationError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	now := time.Now().UnixMilli()
	for index := 0; index < count; index++ {
		request := &requests[index]
		if hook := store.writer.hooks.beforePendingWrite; hook != nil {
			if err := hook(request.kind); err != nil {
				return time.Since(started), sqliteOperationError(err)
			}
		}
		switch request.kind {
		case pendingContactWrite:
			if err := writeSQLiteContact(ctx, transaction, request.contact); err != nil {
				return time.Since(started), err
			}
			continue
		case pendingChatWrite:
			if err := writeSQLiteChat(ctx, transaction, request.chat); err != nil {
				return time.Since(started), err
			}
			continue
		case pendingMessageWrite:
		default:
			return time.Since(started), newSQLiteError(ErrStoreRejected, nil)
		}
		for messageIndex := 0; messageIndex < request.messages.Len(); messageIndex++ {
			message, _ := request.messages.At(messageIndex)
			countUnread := request.messages.Origin() == model.WriteRealtime
			if err := writeSQLiteMessage(ctx, transaction, message, now, countUnread); err != nil {
				return time.Since(started), err
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		return time.Since(started), sqliteOperationError(err)
	}
	committed = true
	duration = time.Since(started)
	if err := tightenSQLiteFiles(store.path, operatingSystemFS); err != nil {
		return duration, newSQLiteError(ErrUnsafeCachePath, err)
	}
	return duration, nil
}

func normalizeContact(contact model.Contact) (model.Contact, error) {
	return model.NewContact(model.ContactInput{
		ID:          contact.ID().String(),
		DisplayName: contact.DisplayName(),
		UpdatedAt:   contact.UpdatedAt(),
		IngestSeq:   contact.IngestSeq(),
	})
}

func writeSQLiteContact(ctx context.Context, transaction *sql.Tx, contact model.Contact) error {
	updatedAt := int64(0)
	if !contact.UpdatedAt().IsZero() {
		updatedAt = contact.UpdatedAt().UnixMilli()
	}
	if _, err := transaction.ExecContext(ctx, upsertContactSQL,
		contact.ID().String(), contact.DisplayName(), updatedAt, int64(contact.IngestSeq()),
	); err != nil {
		return fmt.Errorf("upsert contact: %w", sqliteOperationError(err))
	}
	return nil
}

func writeSQLiteChat(ctx context.Context, transaction *sql.Tx, chat model.Chat) error {
	contactID := any(nil)
	if chat.HasContact() {
		contactID = chat.ContactID().String()
	}
	isGroup := sqliteBool(chat.IsGroup())
	lastMessageAt := int64(0)
	if !chat.LastMessageAt().IsZero() {
		lastMessageAt = chat.LastMessageAt().UnixMilli()
	}
	muted := sqliteBool(chat.Muted())
	archived := sqliteBool(chat.Archived())
	placeholder := 0
	if chat.Placeholder() {
		placeholder = 1
	}
	updatedAt := int64(0)
	if !chat.UpdatedAt().IsZero() {
		updatedAt = chat.UpdatedAt().UnixMilli()
	}
	if _, err := transaction.ExecContext(ctx, ensureChatSQL,
		chat.ID().String(), contactID, chat.DisplayName(), isGroup,
		lastMessageAt, int64(chat.UnreadCount()), muted, archived,
		placeholder, updatedAt, int64(chat.IngestSeq()),
	); err != nil {
		return fmt.Errorf("upsert chat: %w", sqliteOperationError(err))
	}
	return nil
}

func writeSQLiteMessage(ctx context.Context, transaction *sql.Tx, message model.Message, now int64, countUnread bool) error {
	if _, err := transaction.ExecContext(ctx, ensurePlaceholderChatSQL,
		message.ChatID().String(), message.SentAt().UnixMilli(), now,
	); err != nil {
		return fmt.Errorf("ingest message: %w", sqliteOperationError(err))
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
	result, err := transaction.ExecContext(ctx, insertMessageSQL,
		message.ChatID().String(), message.MessageID().String(),
		message.SentAt().UnixMilli(), fromMe, messageKindUnknown, body,
		bodyBytes, bodyTruncated, retainedBody,
	)
	if err != nil {
		return fmt.Errorf("ingest message: %w", sqliteOperationError(err))
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ingest message: %w", sqliteOperationError(err))
	}
	if inserted == 0 {
		result, err = transaction.ExecContext(ctx, updateMessageSQL,
			retainedBody, body,
			retainedBody, bodyBytes,
			retainedBody, bodyTruncated,
			retainedBody,
			message.ChatID().String(), message.MessageID().String(),
			message.SentAt().UnixMilli(), fromMe,
		)
		if err != nil {
			return fmt.Errorf("ingest message: %w", sqliteOperationError(err))
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("ingest message: %w", sqliteOperationError(err))
		}
		if changed != 1 {
			return fmt.Errorf("ingest message: %w", newSQLiteError(ErrStoreRejected, nil))
		}
	} else if inserted != 1 {
		return fmt.Errorf("ingest message: %w", newSQLiteError(ErrStoreRejected, nil))
	}
	unreadIncrement := 0
	if inserted == 1 && countUnread && !message.FromMe() {
		unreadIncrement = 1
	}
	if _, err := transaction.ExecContext(ctx, updateChatActivitySQL,
		message.SentAt().UnixMilli(), unreadIncrement, int64(maxSQLiteUnreadCount),
		now, message.ChatID().String(),
	); err != nil {
		return fmt.Errorf("ingest message: %w", sqliteOperationError(err))
	}
	return nil
}

func sqliteBool(value bool) int {
	if value {
		return 1
	}
	return 0
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
	var values sqliteMessageValues
	err = store.db.QueryRowContext(ctx, selectMessageSQL,
		validatedChatID.String(), validatedMessageID.String(),
	).Scan(
		&values.sentAt,
		&values.fromMe,
		&values.body,
		&values.bodyTruncated,
		&values.retainedBody,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Message{}, newSQLiteError(ErrMessageNotFound, nil)
	}
	if err != nil {
		return model.Message{}, sqliteOperationError(err)
	}
	return sqliteMessageFromValues(validatedChatID.String(), validatedMessageID.String(), values)
}

type sqliteScanner interface {
	Scan(...any) error
}

type sqliteMessageValues struct {
	sentAt        int64
	fromMe        int
	body          sql.NullString
	bodyTruncated int
	retainedBody  int
}

func sqliteMessageFromValues(chatID, messageID string, values sqliteMessageValues) (model.Message, error) {
	if values.fromMe < 0 || values.fromMe > 1 ||
		values.bodyTruncated < 0 || values.bodyTruncated > 1 ||
		values.retainedBody < 0 || values.retainedBody > 1 ||
		(values.retainedBody == 1 && !values.body.Valid) {
		return model.Message{}, newSQLiteError(ErrCorruptCache, nil)
	}
	message, err := model.NewMessage(model.MessageInput{
		ChatID:    chatID,
		MessageID: messageID,
		SentAt:    time.UnixMilli(values.sentAt).UTC(),
		FromMe:    values.fromMe == 1,
		Text:      values.body.String,
	})
	if err != nil {
		return model.Message{}, newSQLiteError(ErrCorruptCache, err)
	}
	if values.retainedBody == 0 {
		message = message.WithoutBody()
	}
	return message, nil
}

func sqliteOperationError(err error) error {
	if isContextError(err) {
		return err
	}
	var codedError interface{ Code() int }
	if errors.As(err, &codedError) {
		switch codedError.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY and SQLITE_LOCKED, including extended codes.
			return newSQLiteError(ErrBusy, err)
		case 11, 26: // SQLITE_CORRUPT and SQLITE_NOTADB.
			return newSQLiteError(ErrCorruptCache, err)
		case 13: // SQLITE_FULL, including extended codes.
			return newSQLiteError(ErrDiskFull, err)
		}
	}
	return newSQLiteError(ErrCacheIO, err)
}

const upsertContactSQL = `
INSERT INTO contacts(contact_id, display_name, updated_at, ingest_seq)
VALUES (?, ?, ?, ?)
ON CONFLICT(contact_id) DO UPDATE SET
    display_name = CASE
        WHEN excluded.display_name = '' THEN contacts.display_name
        ELSE excluded.display_name
    END,
    updated_at = excluded.updated_at,
    ingest_seq = excluded.ingest_seq
WHERE excluded.ingest_seq > contacts.ingest_seq
   OR (
       excluded.ingest_seq = contacts.ingest_seq
       AND excluded.updated_at >= contacts.updated_at
   )`

const ensureChatSQL = `
INSERT INTO chats(
    chat_id, contact_id, display_name, is_group, last_message_at,
    unread_count, muted, archived, placeholder, updated_at, ingest_seq
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id) DO UPDATE SET
	contact_id = CASE
		WHEN excluded.contact_id IS NULL THEN chats.contact_id
		ELSE excluded.contact_id
	END,
    display_name = CASE
		WHEN excluded.display_name = '' THEN chats.display_name
		ELSE excluded.display_name
    END,
	is_group = excluded.is_group,
	last_message_at = MAX(chats.last_message_at, excluded.last_message_at),
	unread_count = MAX(chats.unread_count, excluded.unread_count),
	muted = excluded.muted,
	archived = excluded.archived,
	placeholder = 0,
	updated_at = excluded.updated_at,
	ingest_seq = excluded.ingest_seq
WHERE excluded.placeholder = 0
  AND (
	  chats.placeholder = 1
	  OR excluded.ingest_seq > chats.ingest_seq
	  OR (
	      excluded.ingest_seq = chats.ingest_seq
	      AND excluded.updated_at >= chats.updated_at
	  )
  )`

const ensurePlaceholderChatSQL = `
INSERT INTO chats(chat_id, placeholder, last_message_at, updated_at, ingest_seq)
VALUES (?, 1, ?, ?, 0)
ON CONFLICT(chat_id) DO UPDATE SET
    last_message_at = MAX(chats.last_message_at, excluded.last_message_at),
    updated_at = excluded.updated_at`

const insertMessageSQL = `
INSERT INTO messages(
    chat_id, message_id, sent_at, from_me, kind, body, body_bytes,
    body_truncated, local_revision, ingest_seq, retained_body
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?)
ON CONFLICT(chat_id, message_id) DO NOTHING`

const updateMessageSQL = `
UPDATE messages SET
    body = CASE
		WHEN retained_body = 1 AND ? = 0 THEN body
		ELSE ?
    END,
    body_bytes = CASE
		WHEN retained_body = 1 AND ? = 0 THEN body_bytes
		ELSE ?
    END,
    body_truncated = CASE
		WHEN retained_body = 1 AND ? = 0 THEN body_truncated
		ELSE ?
    END,
	retained_body = MAX(retained_body, ?)
WHERE chat_id = ? AND message_id = ? AND sent_at = ? AND from_me = ?`

const updateChatActivitySQL = `
UPDATE chats SET
    last_message_at = MAX(last_message_at, ?),
    unread_count = CASE
        WHEN ? = 1 AND unread_count < ? THEN unread_count + 1
        ELSE unread_count
    END,
    updated_at = ?
WHERE chat_id = ?`

const selectMessageSQL = `
SELECT sent_at, from_me, body, body_truncated, retained_body
FROM messages
WHERE chat_id = ? AND message_id = ?`
