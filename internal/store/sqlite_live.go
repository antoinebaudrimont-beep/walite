package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// WriteRealtime returns only new inserts, in request order, after COMMIT.
// Each event carries unread/activity observed at that logical insert. Duplicates
// may enrich rows but never publish again. No read occurs after the transaction
// to infer which messages committed.
func (store *SQLiteStore) WriteRealtime(ctx context.Context, batch model.WriteBatch) (model.LiveEventBatch, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.LiveEventBatch{}, err
	}
	if batch.Origin() != model.WriteRealtime {
		return model.LiveEventBatch{}, newSQLiteError(ErrStoreRejected, nil)
	}
	var messages [model.MaxWriteBatchMessages]model.Message
	for i := 0; i < batch.Len(); i++ {
		messages[i], _ = batch.At(i)
	}
	owned, err := model.NewWriteBatch(model.WriteRealtime, messages[:batch.Len()])
	if err != nil {
		return model.LiveEventBatch{}, newSQLiteError(ErrStoreRejected, err)
	}
	if owned.Len() == 0 {
		return model.LiveEventBatch{}, nil
	}
	pending := newPendingMessages(owned)
	pending.emitLive = true
	result := store.writer.submitResult(ctx, pending)
	if result.err != nil {
		return model.LiveEventBatch{}, result.err
	}
	if result.committed == nil {
		return model.LiveEventBatch{}, nil
	}
	return *result.committed, nil
}

func writeSQLiteRequest(ctx context.Context, tx *sql.Tx, request *pendingWrite, now int64) error {
	// There are at most 50 logical inserts across the entire writer transaction.
	type insertion struct {
		message  model.Message
		unread   uint32
		activity time.Time
	}
	var inserts [model.MaxWriteBatchMessages]insertion
	count := 0
	for i := 0; i < request.messages.Len(); i++ {
		message, _ := request.messages.At(i)
		inserted := false
		if err := writeSQLiteMessage(ctx, tx, message, now, request.messages.Origin() == model.WriteRealtime, &inserted); err != nil {
			return err
		}
		if !inserted || !request.emitLive {
			continue
		}
		var unread, activity int64
		if err := tx.QueryRowContext(ctx, `SELECT unread_count, last_message_at FROM chats WHERE chat_id = ?`, message.ChatID().String()).Scan(&unread, &activity); err != nil {
			return sqliteOperationError(err)
		}
		if unread < 0 || unread > int64(maxSQLiteUnreadCount) {
			return newSQLiteError(ErrCorruptCache, nil)
		}
		inserts[count] = insertion{message, uint32(unread), time.UnixMilli(activity).UTC()}
		count++
	}
	if count == 0 {
		return nil
	}
	var events [model.MaxLiveEventsPerCommit]model.LiveEvent
	for i := 0; i < count; i++ {
		item := inserts[i]
		// This is still the writing transaction, and insertion identity was
		// recorded from INSERT's result. Read final same-request enrichment,
		// matching Memory when a later duplicate adds a quote/sender.
		message, err := readSQLiteMessage(ctx, tx, item.message.ChatID(), item.message.MessageID())
		if err != nil {
			return err
		}
		events[i], err = model.NewLiveMessageCommitted(message, item.unread, item.activity)
		if err != nil {
			return newSQLiteError(ErrStoreRejected, err)
		}
	}
	batch, err := model.NewLiveEventBatch(events[:count])
	if err != nil {
		return newSQLiteError(ErrStoreRejected, err)
	}
	request.result.committed = &batch
	return nil
}

func readSQLiteMessage(ctx context.Context, tx *sql.Tx, chat model.ChatID, id model.MessageID) (model.Message, error) {
	var v sqliteMessageValues
	err := tx.QueryRowContext(ctx, selectMessageSQL, chat.String(), id.String()).Scan(
		&v.sentAt, &v.fromMe, &v.body, &v.bodyTruncated, &v.retainedBody,
		&v.senderID, &v.isGroup, &v.quoteID, &v.quoteText, &v.quoteFromMe, &v.quoteParticipantID,
		&v.quoteMediaKind, &v.quoteMediaName, &v.quoteMediaMIME,
		&v.mediaKind, &v.mediaName, &v.mediaMIME, &v.mediaDeclaredBytes, &v.mediaDownloadRef)
	if err != nil {
		return model.Message{}, sqliteOperationError(err)
	}
	return sqliteMessageFromValues(chat.String(), id.String(), v)
}

// Called under the maintenance gate. A failed post-commit check is a warning
// exposed by CacheDegradation; the next write must pass this check first.
func (store *SQLiteStore) checkWriteFiles() error {
	var err error
	if store.writer != nil && store.writer.hooks.checkFiles != nil {
		err = store.writer.hooks.checkFiles()
	} else {
		err = tightenSQLiteFiles(store.path, operatingSystemFS)
	}
	store.permissionFailed.Store(err != nil)
	if err != nil {
		store.degradation.Store(uint32(CacheWriteUnavailable))
		return newSQLiteError(ErrUnsafeCachePath, err)
	}
	return nil
}
