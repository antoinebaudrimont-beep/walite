package store

import (
	"context"
	"fmt"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const (
	defaultPageMessages = 50
	maxSQLitePageRows   = maxPageMessages + 1
)

// Page returns at most 100 messages in descending (sent_at, message_id)
// order. A non-zero cursor is the last message returned by the prior page.
// Non-positive limits use 50; limits above 100 are clamped to 100.
func (store *SQLiteStore) Page(ctx context.Context, id model.ChatID, cursor model.Cursor, limit int) ([]model.Message, model.Cursor, error) {
	if err := store.checkOpen(ctx); err != nil {
		return nil, model.NoCursor(), err
	}
	chatID, err := validateChatID(id)
	if err != nil {
		return nil, model.NoCursor(), newSQLiteError(ErrStoreRejected, err)
	}
	if err := validateCursor(cursor); err != nil {
		return nil, model.NoCursor(), newSQLiteError(ErrStoreRejected, err)
	}
	limit = boundedSQLitePageLimit(limit)
	queryLimit := limit + 1
	if queryLimit > maxSQLitePageRows {
		queryLimit = maxSQLitePageRows
	}

	var rows sqliteRows
	if cursor.IsZero() {
		rows, err = store.db.QueryContext(ctx, sqliteFirstMessagePageSQL, chatID.String(), queryLimit)
	} else {
		cursorMillis := cursor.SentAt().UnixMilli()
		rows, err = store.db.QueryContext(
			ctx,
			sqliteContinuationMessagePageSQL,
			chatID.String(),
			cursorMillis,
			cursorMillis,
			cursor.MessageID().String(),
			queryLimit,
		)
	}
	if err != nil {
		return nil, model.NoCursor(), fmt.Errorf("query message page: %w", sqliteOperationError(err))
	}
	defer rows.Close()

	messages := make([]model.Message, 0, queryLimit)
	for rows.Next() {
		message, err := scanSQLitePageMessage(rows)
		if err != nil {
			return nil, model.NoCursor(), fmt.Errorf("scan message page: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, model.NoCursor(), fmt.Errorf("query message page: %w", sqliteOperationError(err))
	}

	next := model.NoCursor()
	if len(messages) > limit {
		messages[limit] = model.Message{}
		messages = messages[:limit:limit]
		last := messages[len(messages)-1]
		next, err = model.NewCursor(last.SentAt(), last.MessageID())
		if err != nil {
			return nil, model.NoCursor(), newSQLiteError(ErrCorruptCache, err)
		}
	}
	return messages, next, nil
}

func boundedSQLitePageLimit(limit int) int {
	if limit <= 0 {
		return defaultPageMessages
	}
	if limit > maxPageMessages {
		return maxPageMessages
	}
	return limit
}

type sqliteRows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

func scanSQLitePageMessage(scanner sqliteScanner) (model.Message, error) {
	var chatID, messageID string
	var values sqliteMessageValues
	if err := scanner.Scan(
		&chatID,
		&messageID,
		&values.sentAt,
		&values.fromMe,
		&values.body,
		&values.bodyTruncated,
		&values.retainedBody,
		&values.senderID, &values.isGroup, &values.quoteID, &values.quoteText, &values.quoteFromMe,
	); err != nil {
		return model.Message{}, sqliteOperationError(err)
	}
	return sqliteMessageFromValues(chatID, messageID, values)
}

const sqliteMessageValueColumns = `sent_at, from_me, body, body_truncated, retained_body,
sender_id, is_group, quote_id, quote_text, quote_from_me`

const sqliteMessagePageColumns = `chat_id, message_id, ` + sqliteMessageValueColumns

const sqliteFirstMessagePageSQL = `
SELECT ` + sqliteMessagePageColumns + `
FROM messages
WHERE chat_id = ?
ORDER BY sent_at DESC, message_id DESC
LIMIT ?`

const sqliteContinuationMessagePageSQL = `
SELECT ` + sqliteMessagePageColumns + `
FROM messages
WHERE chat_id = ?
  AND (
      sent_at < ?
      OR (sent_at = ? AND message_id < ?)
  )
ORDER BY sent_at DESC, message_id DESC
LIMIT ?`
