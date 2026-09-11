package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// A reaction may race ahead of its base message because the transport and
// committed-message consumers are independent. Retain only a small newest
// frontier per chat until those targets arrive; this also bounds reactions to
// already-pruned or otherwise unknown messages.
const maxOrphanReactionsPerChat = model.MaxReactionsPerMessage

// ApplyReaction serializes one latest-value mutation with all other cache
// writes and returns the complete bounded aggregate after commit.
func (store *SQLiteStore) ApplyReaction(ctx context.Context, reaction model.Reaction) (model.ReactionSummary, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.ReactionSummary{}, err
	}
	owned, err := model.NewReaction(model.ReactionInput{
		ChatID: reaction.ChatID().String(), TargetMessageID: reaction.TargetMessageID().String(),
		ReactorID: reaction.ReactorID().String(), Emoji: reaction.Emoji(), UpdatedAt: reaction.UpdatedAt(),
	})
	if err != nil {
		return model.ReactionSummary{}, newSQLiteError(ErrStoreRejected, err)
	}
	result := store.writer.submitResult(ctx, newPendingReaction(owned))
	return result.reaction, result.err
}

// ReactionSummary loads the bounded aggregate used by initial/reloaded pages.
func (store *SQLiteStore) ReactionSummary(ctx context.Context, chatID model.ChatID, target model.MessageID) (model.ReactionSummary, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.ReactionSummary{}, err
	}
	chat, err := model.NewChatID(chatID.String())
	if err != nil {
		return model.ReactionSummary{}, newSQLiteError(ErrStoreRejected, err)
	}
	message, err := model.NewMessageID(target.String())
	if err != nil {
		return model.ReactionSummary{}, newSQLiteError(ErrStoreRejected, err)
	}
	return readSQLiteReactionSummary(ctx, store.db, chat, message)
}

type reactionQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func writeSQLiteReaction(ctx context.Context, tx *sql.Tx, reaction model.Reaction, now int64) (model.ReactionSummary, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO chats(chat_id, placeholder, updated_at, ingest_seq)
		VALUES (?, 1, ?, 0) ON CONFLICT(chat_id) DO NOTHING`, reaction.ChatID().String(), now); err != nil {
		return model.ReactionSummary{}, fmt.Errorf("ensure reaction chat: %w", sqliteOperationError(err))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO reactions(chat_id, target_message_id, reactor_id, emoji, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, target_message_id, reactor_id) DO UPDATE SET
			emoji = excluded.emoji, updated_at = excluded.updated_at
		WHERE excluded.updated_at > reactions.updated_at OR
			(excluded.updated_at = reactions.updated_at AND excluded.emoji = '' AND reactions.emoji <> '') OR
			(excluded.updated_at = reactions.updated_at AND excluded.emoji <> '' AND reactions.emoji <> '' AND excluded.emoji > reactions.emoji)`,
		reaction.ChatID().String(), reaction.TargetMessageID().String(), reaction.ReactorID().String(), reaction.Emoji(), reaction.UpdatedAt().UnixMilli()); err != nil {
		return model.ReactionSummary{}, fmt.Errorf("upsert reaction: %w", sqliteOperationError(err))
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM reactions WHERE rowid IN (
		SELECT rowid FROM reactions WHERE chat_id = ? AND target_message_id = ?
		ORDER BY updated_at DESC, reactor_id ASC LIMIT -1 OFFSET ?
	)`, reaction.ChatID().String(), reaction.TargetMessageID().String(), model.MaxReactionsPerMessage); err != nil {
		return model.ReactionSummary{}, fmt.Errorf("bound reactions: %w", sqliteOperationError(err))
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM reactions WHERE rowid IN (
		SELECT reactions.rowid FROM reactions
		LEFT JOIN messages ON messages.chat_id = reactions.chat_id
			AND messages.message_id = reactions.target_message_id
		WHERE reactions.chat_id = ? AND messages.message_id IS NULL
		ORDER BY reactions.updated_at DESC, reactions.target_message_id ASC, reactions.reactor_id ASC
		LIMIT -1 OFFSET ?
	)`, reaction.ChatID().String(), maxOrphanReactionsPerChat); err != nil {
		return model.ReactionSummary{}, fmt.Errorf("bound orphan reactions: %w", sqliteOperationError(err))
	}
	return readSQLiteReactionSummary(ctx, tx, reaction.ChatID(), reaction.TargetMessageID())
}

func readSQLiteReactionSummary(ctx context.Context, queryer reactionQueryer, chatID model.ChatID, target model.MessageID) (model.ReactionSummary, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT emoji, COUNT(*), MAX(CASE WHEN reactor_id = ? THEN 1 ELSE 0 END)
		FROM reactions WHERE chat_id = ? AND target_message_id = ? AND emoji <> ''
		GROUP BY emoji ORDER BY COUNT(*) DESC, emoji ASC LIMIT ?`,
		model.SelfReactorID, chatID.String(), target.String(), model.MaxReactionGroupsPerMessage)
	if err != nil {
		return model.ReactionSummary{}, fmt.Errorf("query reactions: %w", sqliteOperationError(err))
	}
	defer rows.Close()
	groups := make([]model.ReactionGroup, 0, model.MaxReactionGroupsPerMessage)
	for rows.Next() {
		var emoji string
		var count, own int
		if err := rows.Scan(&emoji, &count, &own); err != nil || count < 1 || count > model.MaxReactionsPerMessage || !sqliteBoolean(own) {
			return model.ReactionSummary{}, fmt.Errorf("scan reactions: %w", newSQLiteError(ErrCorruptCache, err))
		}
		group, err := model.NewReactionGroup(emoji, uint16(count), own == 1)
		if err != nil {
			return model.ReactionSummary{}, fmt.Errorf("scan reactions: %w", newSQLiteError(ErrCorruptCache, err))
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return model.ReactionSummary{}, fmt.Errorf("query reactions: %w", sqliteOperationError(err))
	}
	summary, err := model.NewReactionSummary(chatID.String(), target.String(), groups)
	if err != nil {
		return model.ReactionSummary{}, newSQLiteError(ErrCorruptCache, err)
	}
	return summary, nil
}
