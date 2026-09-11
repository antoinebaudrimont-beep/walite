package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"time"
)

const (
	// DefaultSQLiteCacheBudgetBytes is the physical on-disk cache budget.
	DefaultSQLiteCacheBudgetBytes int64 = 250 * 1024 * 1024

	sqliteRetainedBodiesPerChat = 100
	sqliteBodyRetentionAge      = 90 * 24 * time.Hour
	sqliteMaxPrunedBodies       = 500
)

// CacheDegradation describes the store's current controlled degradation state.
type CacheDegradation uint8

const (
	CacheNormal CacheDegradation = iota + 1
	CachePressure
	CacheWriteUnavailable
)

// SQLitePruneResult reports content-free logical work performed by Prune.
type SQLitePruneResult struct {
	BodiesDropped     int
	LogicalBytesFreed int64
}

// SQLiteMaintenanceResult reports one bounded cache-pressure cycle.
type SQLiteMaintenanceResult struct {
	Prune        SQLitePruneResult
	SizeBefore   int64
	SizeAfter    int64
	BudgetBytes  int64
	Checkpointed bool
	Degradation  CacheDegradation
}

type sqliteCheckpointFunc func(context.Context, *sql.DB) error

type sqlitePruneHooks struct {
	beforeUpdate func() error
	beforeCommit func()
}

// CacheDegradation reports the store's current content-free degradation state.
func (store *SQLiteStore) CacheDegradation() CacheDegradation {
	if store == nil {
		return CacheWriteUnavailable
	}
	if store.permissionFailed.Load() {
		return CacheWriteUnavailable
	}
	state := CacheDegradation(store.degradation.Load())
	if state == 0 {
		return CacheNormal
	}
	return state
}

// CacheSizeBytes returns the physical size of the main database, WAL, and SHM.
func (store *SQLiteStore) CacheSizeBytes(ctx context.Context) (int64, error) {
	if err := store.checkOpen(ctx); err != nil {
		return 0, err
	}
	size, err := sqliteCacheSize(store.path, store.cacheStat)
	if err != nil {
		return 0, err
	}
	return size, nil
}

// Prune removes only eligible message bodies. Message identity and metadata
// remain intact, and at most sqliteMaxPrunedBodies are changed per transaction.
func (store *SQLiteStore) Prune(ctx context.Context, now time.Time) (SQLitePruneResult, error) {
	if err := store.checkOpen(ctx); err != nil {
		return SQLitePruneResult{}, err
	}
	if now.IsZero() {
		return SQLitePruneResult{}, newSQLiteError(ErrStoreRejected, nil)
	}
	if err := store.acquireMaintenance(ctx); err != nil {
		return SQLitePruneResult{}, err
	}
	defer store.releaseMaintenance()
	if err := store.checkOpen(ctx); err != nil {
		return SQLitePruneResult{}, err
	}
	result, err := store.pruneLocked(ctx, now)
	if err != nil {
		store.observeSQLiteFailure(err)
		return result, err
	}
	_ = store.checkWriteFiles()
	return result, nil
}

// EnforceCacheBudget performs at most one bounded prune transaction followed
// by one serialized TRUNCATE checkpoint when the physical cache exceeds budget.
func (store *SQLiteStore) EnforceCacheBudget(ctx context.Context, now time.Time) (SQLiteMaintenanceResult, error) {
	if err := store.checkOpen(ctx); err != nil {
		return SQLiteMaintenanceResult{}, err
	}
	if now.IsZero() {
		return SQLiteMaintenanceResult{}, newSQLiteError(ErrStoreRejected, nil)
	}
	before, err := store.CacheSizeBytes(ctx)
	if err != nil {
		return SQLiteMaintenanceResult{}, err
	}
	result := SQLiteMaintenanceResult{
		SizeBefore:  before,
		SizeAfter:   before,
		BudgetBytes: store.cacheBudget,
		Degradation: CacheNormal,
	}
	if before <= store.cacheBudget {
		result.Degradation = store.CacheDegradation()
		if result.Degradation != CacheWriteUnavailable {
			result.Degradation = CacheNormal
			store.degradation.Store(uint32(CacheNormal))
		}
		return result, nil
	}

	if err := store.acquireMaintenance(ctx); err != nil {
		return result, err
	}
	defer store.releaseMaintenance()
	if err := store.checkOpen(ctx); err != nil {
		return result, err
	}
	result.Prune, err = store.pruneLocked(ctx, now)
	if err == nil {
		err = store.checkpoint(ctx, store.db)
		result.Checkpointed = err == nil
	}
	after, sizeErr := sqliteCacheSize(store.path, store.cacheStat)
	if sizeErr == nil {
		result.SizeAfter = after
		if after > store.cacheBudget {
			result.Degradation = CachePressure
			store.degradation.Store(uint32(CachePressure))
		} else {
			result.Degradation = CacheNormal
			store.degradation.Store(uint32(CacheNormal))
		}
	}
	if err != nil {
		err = normalizeSQLiteMaintenanceError(err)
		store.observeSQLiteFailure(err)
		result.Degradation = store.CacheDegradation()
		return result, err
	}
	if sizeErr != nil {
		return result, sizeErr
	}
	_ = store.checkWriteFiles()
	if store.permissionFailed.Load() {
		result.Degradation = CacheWriteUnavailable
	}
	if result.Degradation == CachePressure {
		return result, newSQLiteError(ErrCachePressure, nil)
	}
	return result, nil
}

func (store *SQLiteStore) pruneLocked(ctx context.Context, now time.Time) (SQLitePruneResult, error) {
	if err := sqliteContextError(ctx); err != nil {
		return SQLitePruneResult{}, err
	}
	if err := store.checkWriteFiles(); err != nil {
		return SQLitePruneResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return SQLitePruneResult{}, sqliteOperationError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()

	cutoff := now.Add(-sqliteBodyRetentionAge).UnixMilli()
	var result SQLitePruneResult
	if err := transaction.QueryRowContext(
		ctx,
		sqlitePruneAccountingSQL,
		sqliteRetainedBodiesPerChat,
		cutoff,
		sqliteMaxPrunedBodies,
	).Scan(
		&result.BodiesDropped,
		&result.LogicalBytesFreed,
	); err != nil {
		return SQLitePruneResult{}, sqliteOperationError(err)
	}
	if result.BodiesDropped < 0 || result.BodiesDropped > sqliteMaxPrunedBodies || result.LogicalBytesFreed < 0 {
		return SQLitePruneResult{}, newSQLiteError(ErrCorruptCache, nil)
	}
	if result.BodiesDropped > 0 {
		if hook := store.pruneHooks.beforeUpdate; hook != nil {
			if err := hook(); err != nil {
				return SQLitePruneResult{}, sqliteOperationError(err)
			}
		}
		updated, err := transaction.ExecContext(
			ctx,
			sqlitePruneBodiesSQL,
			sqliteRetainedBodiesPerChat,
			cutoff,
			sqliteMaxPrunedBodies,
		)
		if err != nil {
			return SQLitePruneResult{}, sqliteOperationError(err)
		}
		changed, err := updated.RowsAffected()
		if err != nil {
			return SQLitePruneResult{}, sqliteOperationError(err)
		}
		if changed != int64(result.BodiesDropped) {
			return SQLitePruneResult{}, newSQLiteError(ErrCorruptCache, nil)
		}
	}
	if hook := store.pruneHooks.beforeCommit; hook != nil {
		hook()
	}
	if err := sqliteContextError(ctx); err != nil {
		return SQLitePruneResult{}, err
	}
	if err := store.checkWriteFiles(); err != nil {
		return SQLitePruneResult{}, err
	}
	if err := transaction.Commit(); err != nil {
		return SQLitePruneResult{}, sqliteOperationError(err)
	}
	committed = true
	return result, nil
}

func (store *SQLiteStore) acquireMaintenance(ctx context.Context) error {
	if store == nil || store.maintenance == nil {
		return newSQLiteError(ErrStoreClosed, nil)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-store.maintenance:
		return nil
	}
}

func (store *SQLiteStore) releaseMaintenance() {
	store.maintenance <- struct{}{}
}

func (store *SQLiteStore) observeSQLiteFailure(err error) {
	if errors.Is(err, ErrDiskFull) {
		store.degradation.Store(uint32(CacheWriteUnavailable))
	}
}

func (store *SQLiteStore) observeSQLiteWriteSuccess() {
	store.degradation.CompareAndSwap(uint32(CacheWriteUnavailable), uint32(CacheNormal))
}

func sqliteCacheSize(path string, stat func(string) (os.FileInfo, error)) (int64, error) {
	if path == "" || stat == nil {
		return 0, newSQLiteError(ErrCacheIO, nil)
	}
	var total int64
	for index, candidate := range [...]string{path, path + "-wal", path + "-shm"} {
		info, err := stat(candidate)
		if errors.Is(err, os.ErrNotExist) && index > 0 {
			continue
		}
		if err != nil {
			return 0, newSQLiteError(ErrCacheIO, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 {
			return 0, newSQLiteError(ErrUnsafeCachePath, nil)
		}
		if info.Size() > math.MaxInt64-total {
			return 0, newSQLiteError(ErrCacheIO, nil)
		}
		total += info.Size()
	}
	return total, nil
}

func sqliteTruncateCheckpoint(ctx context.Context, database *sql.DB) error {
	var busy, logFrames, checkpointedFrames int
	if err := database.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(
		&busy,
		&logFrames,
		&checkpointedFrames,
	); err != nil {
		return sqliteOperationError(err)
	}
	if busy != 0 {
		return newSQLiteError(ErrBusy, nil)
	}
	return nil
}

func normalizeSQLiteMaintenanceError(err error) error {
	if isContextError(err) || errors.Is(err, ErrBusy) || errors.Is(err, ErrDiskFull) ||
		errors.Is(err, ErrCacheIO) || errors.Is(err, ErrCorruptCache) ||
		errors.Is(err, ErrStoreClosed) || errors.Is(err, ErrStoreRejected) {
		return err
	}
	return sqliteOperationError(err)
}

const sqlitePruneAccountingSQL = `
WITH ranked AS (
    SELECT chat_id, message_id,
           ROW_NUMBER() OVER (
               PARTITION BY chat_id
               ORDER BY sent_at DESC, message_id DESC
           ) AS body_rank
    FROM messages
), eligible AS (
    SELECT message.body_bytes + length(CAST(message.quote_text AS BLOB)) + length(CAST(message.quote_id AS BLOB))
           + length(CAST(message.quote_participant_id AS BLOB))
           + length(CAST(message.quote_media_name AS BLOB)) + length(CAST(message.quote_media_mime AS BLOB)) AS body_bytes
    FROM messages AS message
    JOIN ranked
      ON ranked.chat_id = message.chat_id
     AND ranked.message_id = message.message_id
    WHERE message.retained_body = 1
      AND ranked.body_rank > ?
      AND message.sent_at < ?
    ORDER BY message.sent_at, message.chat_id, message.message_id
    LIMIT ?
)
SELECT COUNT(*), COALESCE(SUM(body_bytes), 0)
FROM eligible`

const sqlitePruneBodiesSQL = `
WITH ranked AS (
    SELECT chat_id, message_id,
           ROW_NUMBER() OVER (
               PARTITION BY chat_id
               ORDER BY sent_at DESC, message_id DESC
           ) AS body_rank
    FROM messages
), eligible AS (
    SELECT message.chat_id, message.message_id
    FROM messages AS message
    JOIN ranked
      ON ranked.chat_id = message.chat_id
     AND ranked.message_id = message.message_id
    WHERE message.retained_body = 1
      AND ranked.body_rank > ?
      AND message.sent_at < ?
    ORDER BY message.sent_at, message.chat_id, message.message_id
    LIMIT ?
)
UPDATE messages
SET body = NULL,
    body_bytes = 0,
    quote_id = '',
    quote_text = '',
    quote_from_me = 0,
	quote_participant_id = '',
	quote_media_kind = 0,
	quote_media_name = '',
	quote_media_mime = '',
    retained_body = 0
WHERE EXISTS (
    SELECT 1
    FROM eligible
    WHERE eligible.chat_id = messages.chat_id
      AND eligible.message_id = messages.message_id
)`
