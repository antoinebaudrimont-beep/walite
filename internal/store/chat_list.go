package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// ListChats returns at most the application-wide fixed summary bound, ordered
// by activity descending and stable chat identity ascending.
func (store *SQLiteStore) ListChats(ctx context.Context, limit int) ([]model.Chat, error) {
	if err := store.checkOpen(ctx); err != nil {
		return nil, err
	}
	limit = boundedChatListLimit(limit)
	rows, err := store.db.QueryContext(ctx, listChatsSQL, limit)
	if err != nil {
		return nil, fmt.Errorf("query chat list: %w", sqliteOperationError(err))
	}
	defer rows.Close()
	result := make([]model.Chat, 0, limit)
	for rows.Next() {
		chat, err := scanChatSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chat list: %w", err)
		}
		result = append(result, chat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query chat list: %w", sqliteOperationError(err))
	}
	return result, nil
}

// ListChats provides the same deterministic query for bounded test/offline stores.
func (memory *Memory) ListChats(ctx context.Context, limit int) ([]model.Chat, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	limit = boundedChatListLimit(limit)
	memory.mu.RLock()
	result := make([]model.Chat, 0, min(limit, len(memory.chats)))
	for _, value := range memory.chats {
		result = append(result, value.chat)
	}
	memory.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].LastMessageAt().Equal(result[j].LastMessageAt()) {
			return result[i].ID().String() < result[j].ID().String()
		}
		return result[i].LastMessageAt().After(result[j].LastMessageAt())
	})
	if len(result) > limit {
		result = result[:limit:limit]
	}
	return result, nil
}

func boundedChatListLimit(limit int) int {
	if limit <= 0 || limit > model.MaxChatSummaries {
		return model.MaxChatSummaries
	}
	return limit
}

func scanChatSummary(scanner sqliteScanner) (model.Chat, error) {
	var chatID string
	var contactID sql.NullString
	var displayName string
	var isGroup, muted, archived, placeholder int
	var lastMessageAt, unreadCount, updatedAt, ingestSeq int64
	if err := scanner.Scan(&chatID, &contactID, &displayName, &isGroup, &lastMessageAt, &unreadCount,
		&muted, &archived, &placeholder, &updatedAt, &ingestSeq); err != nil {
		return model.Chat{}, sqliteOperationError(err)
	}
	if !sqliteBoolean(isGroup) || !sqliteBoolean(muted) || !sqliteBoolean(archived) ||
		!sqliteBoolean(placeholder) || unreadCount < 0 || unreadCount > int64(maxSQLiteUnreadCount) ||
		ingestSeq < 0 || (contactID.Valid && contactID.String == "") {
		return model.Chat{}, newSQLiteError(ErrCorruptCache, nil)
	}
	chat, err := model.NewChat(model.ChatInput{
		ID: chatID, ContactID: contactID.String, DisplayName: displayName, IsGroup: isGroup == 1,
		LastMessageAt: sqliteOptionalTime(lastMessageAt), UnreadCount: uint32(unreadCount),
		Muted: muted == 1, Archived: archived == 1, Placeholder: placeholder == 1,
		UpdatedAt: sqliteOptionalTime(updatedAt), IngestSeq: uint64(ingestSeq),
	})
	if err != nil {
		return model.Chat{}, newSQLiteError(ErrCorruptCache, err)
	}
	return chat, nil
}

const listChatsSQL = `
SELECT chat_id, contact_id, display_name, is_group, last_message_at, unread_count,
       muted, archived, placeholder, updated_at, ingest_seq
FROM chats
ORDER BY last_message_at DESC, chat_id ASC
LIMIT ?`
