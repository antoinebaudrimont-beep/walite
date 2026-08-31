package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// RetentionSnapshot is body-free and bounded by the same hard limit as Memory.
// Oversized legacy caches are rejected, not silently presented as a complete
// snapshot. Core continues to enforce its configured (usually smaller) limit.
func (store *SQLiteStore) RetentionSnapshot(ctx context.Context, id model.ChatID) (model.RetentionSnapshot, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.RetentionSnapshot{}, err
	}
	id, err := model.NewChatID(id.String())
	if err != nil {
		return model.RetentionSnapshot{}, newSQLiteError(ErrStoreRejected, err)
	}
	if err := store.acquireMaintenance(ctx); err != nil {
		return model.RetentionSnapshot{}, err
	}
	defer store.releaseMaintenance()
	if err := store.checkOpen(ctx); err != nil {
		return model.RetentionSnapshot{}, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.RetentionSnapshot{}, sqliteOperationError(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT message_id, sent_at, from_me, retained_body, body_bytes
	FROM messages WHERE chat_id = ? ORDER BY sent_at DESC, message_id DESC LIMIT ?`, id.String(), model.MaxRetentionSnapshotSummaries+1)
	if err != nil {
		return model.RetentionSnapshot{}, sqliteOperationError(err)
	}
	defer rows.Close()
	summaries := make([]model.MessageSummary, 0, model.MaxRetentionSnapshotSummaries)
	for rows.Next() {
		if len(summaries) == model.MaxRetentionSnapshotSummaries {
			return model.RetentionSnapshot{}, newSQLiteError(ErrStoreRejected, nil)
		}
		var messageID string
		var sentAt int64
		var fromMe, retained, bytes int
		if err := rows.Scan(&messageID, &sentAt, &fromMe, &retained, &bytes); err != nil {
			return model.RetentionSnapshot{}, sqliteOperationError(err)
		}
		mid, err := model.NewMessageID(messageID)
		if err != nil || !sqliteBoolean(fromMe) || !sqliteBoolean(retained) {
			return model.RetentionSnapshot{}, newSQLiteError(ErrCorruptCache, err)
		}
		summary, err := model.NewMessageSummaryFromMetadata(model.MessageSummaryInput{
			ChatID: id, MessageID: mid, SentAt: time.UnixMilli(sentAt).UTC(), FromMe: fromMe == 1, BodyRetained: retained == 1, BodyBytes: bytes})
		if err != nil {
			return model.RetentionSnapshot{}, newSQLiteError(ErrCorruptCache, err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return model.RetentionSnapshot{}, sqliteOperationError(err)
	}
	if err := rows.Close(); err != nil {
		return model.RetentionSnapshot{}, sqliteOperationError(err)
	}
	anchor, err := readSQLiteAnchor(ctx, tx, id)
	if err != nil {
		return model.RetentionSnapshot{}, err
	}
	return model.NewRetentionSnapshot(id, summaries, anchor)
}

func readSQLiteAnchor(ctx context.Context, tx *sql.Tx, id model.ChatID) (*model.MessageAnchor, error) {
	var messageID string
	var sentAt int64
	var fromMe int
	err := tx.QueryRowContext(ctx, `SELECT anchor_message_id, anchor_sent_at, anchor_from_me FROM sync_checkpoints
	WHERE chat_id = ? AND anchor_message_id IS NOT NULL`, id.String()).Scan(&messageID, &sentAt, &fromMe)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, sqliteOperationError(err)
	}
	mid, err := model.NewMessageID(messageID)
	if err != nil || !sqliteBoolean(fromMe) {
		return nil, newSQLiteError(ErrCorruptCache, err)
	}
	anchor, err := model.NewMessageAnchor(id, mid, time.UnixMilli(sentAt).UTC(), fromMe == 1)
	if err != nil {
		return nil, newSQLiteError(ErrCorruptCache, err)
	}
	return &anchor, nil
}

// ApplyPrune executes Core's already-decided bounded row deletions and anchor
// replacement under the existing maintenance gate. It does not choose another
// retention policy or alter native Prune's 500-body maintenance cycle.
func (store *SQLiteStore) ApplyPrune(ctx context.Context, plan model.PrunePlan) (result model.PruneResult, resultErr error) {
	if err := store.checkOpen(ctx); err != nil {
		return result, err
	}
	var ids [model.MaxPrunePlanDeletes]model.MessageID
	for i := 0; i < plan.DeleteLen(); i++ {
		ids[i], _ = plan.DeleteAt(i)
	}
	var replacement *model.MessageAnchor
	if anchor, ok := plan.ReplacementAnchor(); ok {
		replacement = &anchor
	}
	owned, err := model.NewPrunePlan(plan.ChatID(), ids[:plan.DeleteLen()], replacement)
	if err != nil {
		return result, newSQLiteError(ErrStoreRejected, err)
	}
	if err := store.acquireMaintenance(ctx); err != nil {
		return result, err
	}
	defer store.releaseMaintenance()
	defer func() {
		if resultErr != nil {
			store.observeSQLiteFailure(resultErr)
		}
	}()
	if err := store.checkOpen(ctx); err != nil {
		return result, err
	}
	if err := store.checkWriteFiles(); err != nil {
		return result, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, sqliteOperationError(err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chats WHERE chat_id = ?)`, owned.ChatID().String()).Scan(&exists); err != nil {
		return result, sqliteOperationError(err)
	}
	if exists == 0 {
		return model.NewPruneResult(0, 0, false)
	}
	if hook := store.pruneHooks.beforeUpdate; hook != nil {
		if err := hook(); err != nil {
			return result, sqliteOperationError(err)
		}
	}
	deleted, bodies := 0, 0
	for i := 0; i < owned.DeleteLen(); i++ {
		id, _ := owned.DeleteAt(i)
		var retained int
		err := tx.QueryRowContext(ctx, `DELETE FROM messages WHERE chat_id = ? AND message_id = ? RETURNING retained_body`, owned.ChatID().String(), id.String()).Scan(&retained)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return result, sqliteOperationError(err)
		}
		if !sqliteBoolean(retained) {
			return result, newSQLiteError(ErrCorruptCache, nil)
		}
		deleted++
		bodies += retained
	}
	changed := false
	if replacement != nil {
		previous, err := readSQLiteAnchor(ctx, tx, owned.ChatID())
		if err != nil {
			return result, err
		}
		changed = previous == nil || !sameAnchor(*previous, *replacement)
		_, err = tx.ExecContext(ctx, `INSERT INTO sync_checkpoints(scope, chat_id, anchor_message_id, anchor_sent_at, anchor_from_me, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id) WHERE chat_id IS NOT NULL DO UPDATE SET
		anchor_message_id = excluded.anchor_message_id, anchor_sent_at = excluded.anchor_sent_at,
		anchor_from_me = excluded.anchor_from_me, updated_at = excluded.updated_at`,
			"retention:"+owned.ChatID().String(), owned.ChatID().String(), replacement.MessageID().String(), replacement.SentAt().UnixMilli(), sqliteBool(replacement.FromMe()), time.Now().UnixMilli())
		if err != nil {
			return result, sqliteOperationError(err)
		}
	}
	if hook := store.pruneHooks.beforeCommit; hook != nil {
		hook()
	}
	if err := sqliteContextError(ctx); err != nil {
		return result, err
	}
	if err := store.checkWriteFiles(); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, sqliteOperationError(err)
	}
	_ = store.checkWriteFiles() // committed pruning is not undone by maintenance
	return model.NewPruneResult(deleted, bodies, changed)
}

// Usage adapts physical DB+WAL+SHM accounting, not Memory's logical estimate.
// Counts are one SQL snapshot; nothing proportional to cache size is retained.
func (store *SQLiteStore) Usage(ctx context.Context) (model.CacheUsage, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.CacheUsage{}, err
	}
	if err := store.acquireMaintenance(ctx); err != nil {
		return model.CacheUsage{}, err
	}
	defer store.releaseMaintenance()
	var chats, messages, bodies, anchors int
	if err := store.checkOpen(ctx); err != nil {
		return model.CacheUsage{}, err
	}
	err := store.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM chats),
	(SELECT COUNT(*) FROM messages), (SELECT COUNT(*) FROM messages WHERE retained_body = 1),
	(SELECT COUNT(*) FROM sync_checkpoints WHERE chat_id IS NOT NULL AND anchor_message_id IS NOT NULL)`).Scan(&chats, &messages, &bodies, &anchors)
	if err != nil {
		return model.CacheUsage{}, sqliteOperationError(err)
	}
	bytes, err := sqliteCacheSize(store.path, store.cacheStat)
	if err != nil {
		return model.CacheUsage{}, err
	}
	return model.NewCacheUsage(bytes, chats, messages, bodies, anchors)
}
