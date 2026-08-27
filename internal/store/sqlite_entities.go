package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// Contact loads one contact by its opaque application identity.
func (store *SQLiteStore) Contact(ctx context.Context, id model.ContactID) (model.Contact, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.Contact{}, err
	}
	contactID, err := model.NewContactID(id.String())
	if err != nil {
		return model.Contact{}, newSQLiteError(ErrStoreRejected, err)
	}
	var displayName string
	var updatedAt, ingestSeq int64
	err = store.db.QueryRowContext(ctx, selectContactSQL, contactID.String()).Scan(
		&displayName,
		&updatedAt,
		&ingestSeq,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Contact{}, newSQLiteError(ErrContactNotFound, nil)
	}
	if err != nil {
		return model.Contact{}, fmt.Errorf("query contact: %w", sqliteOperationError(err))
	}
	if ingestSeq < 0 {
		return model.Contact{}, fmt.Errorf("scan contact: %w", newSQLiteError(ErrCorruptCache, nil))
	}
	contact, err := model.NewContact(model.ContactInput{
		ID:          contactID.String(),
		DisplayName: displayName,
		UpdatedAt:   sqliteOptionalTime(updatedAt),
		IngestSeq:   uint64(ingestSeq),
	})
	if err != nil {
		return model.Contact{}, fmt.Errorf("scan contact: %w", newSQLiteError(ErrCorruptCache, err))
	}
	return contact, nil
}

// Chat loads one chat by its application identity.
func (store *SQLiteStore) Chat(ctx context.Context, id model.ChatID) (model.Chat, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.Chat{}, err
	}
	chatID, err := model.NewChatID(id.String())
	if err != nil {
		return model.Chat{}, newSQLiteError(ErrStoreRejected, err)
	}
	var contactID sql.NullString
	var displayName string
	var isGroup, muted, archived, placeholder int
	var lastMessageAt, unreadCount, updatedAt, ingestSeq int64
	err = store.db.QueryRowContext(ctx, selectChatSQL, chatID.String()).Scan(
		&contactID,
		&displayName,
		&isGroup,
		&lastMessageAt,
		&unreadCount,
		&muted,
		&archived,
		&placeholder,
		&updatedAt,
		&ingestSeq,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Chat{}, newSQLiteError(ErrChatNotFound, nil)
	}
	if err != nil {
		return model.Chat{}, fmt.Errorf("query chat: %w", sqliteOperationError(err))
	}
	if !sqliteBoolean(isGroup) || !sqliteBoolean(muted) || !sqliteBoolean(archived) ||
		!sqliteBoolean(placeholder) || unreadCount < 0 || unreadCount > int64(maxSQLiteUnreadCount) ||
		ingestSeq < 0 || (contactID.Valid && contactID.String == "") {
		return model.Chat{}, fmt.Errorf("scan chat: %w", newSQLiteError(ErrCorruptCache, nil))
	}
	chat, err := model.NewChat(model.ChatInput{
		ID:            chatID.String(),
		ContactID:     contactID.String,
		DisplayName:   displayName,
		IsGroup:       isGroup == 1,
		LastMessageAt: sqliteOptionalTime(lastMessageAt),
		UnreadCount:   uint32(unreadCount),
		Muted:         muted == 1,
		Archived:      archived == 1,
		Placeholder:   placeholder == 1,
		UpdatedAt:     sqliteOptionalTime(updatedAt),
		IngestSeq:     uint64(ingestSeq),
	})
	if err != nil {
		return model.Chat{}, fmt.Errorf("scan chat: %w", newSQLiteError(ErrCorruptCache, err))
	}
	return chat, nil
}

func sqliteBoolean(value int) bool {
	return value == 0 || value == 1
}

func sqliteOptionalTime(milliseconds int64) time.Time {
	if milliseconds == 0 {
		return time.Time{}
	}
	return time.UnixMilli(milliseconds).UTC()
}

const selectContactSQL = `
SELECT display_name, updated_at, ingest_seq
FROM contacts
WHERE contact_id = ?`

const selectChatSQL = `
SELECT
    contact_id, display_name, is_group, last_message_at, unread_count,
    muted, archived, placeholder, updated_at, ingest_seq
FROM chats
WHERE chat_id = ?`
