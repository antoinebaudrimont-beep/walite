package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// ApplyDisplayMetadata uses the same bounded FIFO and quality merge as Memory.
// It is advisory: no chat/message insertion, unread increment or activity bump.
// Names and quality survive restart, including names received before a message.
func (store *SQLiteStore) ApplyDisplayMetadata(ctx context.Context, metadata model.DisplayMetadata) (model.DisplayMetadata, error) {
	if err := store.checkOpen(ctx); err != nil {
		return model.DisplayMetadata{}, err
	}
	owned, err := model.NewDisplayMetadata(metadata.ID().String(), metadata.Name(), metadata.Quality(), metadata.IsGroup())
	if err != nil {
		return model.DisplayMetadata{}, newSQLiteError(ErrStoreRejected, err)
	}
	result := store.writer.submitResult(ctx, pendingWrite{kind: pendingDisplayWrite, display: owned})
	return result.display, result.err
}

func sqliteDisplayValue(id, name string, quality, group int) (model.DisplayMetadata, error) {
	if !sqliteBoolean(group) || quality < 0 || quality > int(model.DisplayGroup) {
		return model.DisplayMetadata{}, newSQLiteError(ErrCorruptCache, nil)
	}
	value, err := model.NewDisplayMetadata(id, name, model.DisplayQuality(quality), group == 1)
	if err != nil {
		return model.DisplayMetadata{}, newSQLiteError(ErrCorruptCache, err)
	}
	return value, nil
}

func readSQLiteDisplay(ctx context.Context, tx *sql.Tx, id model.ChatID) (model.DisplayMetadata, int, error) {
	var slot, quality, group int
	var name string
	err := tx.QueryRowContext(ctx, `SELECT slot, name, quality, is_group FROM display_metadata WHERE entity_id = ?`, id.String()).Scan(&slot, &name, &quality, &group)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DisplayMetadata{}, -1, nil
	}
	if err != nil {
		return model.DisplayMetadata{}, 0, sqliteOperationError(err)
	}
	value, err := sqliteDisplayValue(id.String(), name, quality, group)
	return value, slot, err
}

func writeSQLiteDisplay(ctx context.Context, tx *sql.Tx, owned model.DisplayMetadata) (model.DisplayMetadata, error) {
	previous, slot, err := readSQLiteDisplay(ctx, tx, owned.ID())
	if err != nil {
		return model.DisplayMetadata{}, err
	}
	owned = previous.Merge(owned)
	owned, err = updateSQLiteChatDisplay(ctx, tx, owned)
	if err != nil {
		return model.DisplayMetadata{}, err
	}
	if slot < 0 {
		err = tx.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM app_meta WHERE key = 'display_next'`).Scan(&slot)
		if errors.Is(err, sql.ErrNoRows) {
			slot = 0
		} else if err != nil {
			return model.DisplayMetadata{}, sqliteOperationError(err)
		}
		if slot < 0 || slot >= model.DisplayMetadataCapacity {
			return model.DisplayMetadata{}, newSQLiteError(ErrCorruptCache, nil)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO app_meta(key, value, updated_at) VALUES ('display_next', ?, 0)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, (slot+1)%model.DisplayMetadataCapacity)
		if err != nil {
			return model.DisplayMetadata{}, sqliteOperationError(err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO display_metadata(slot, entity_id, name, quality, is_group) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(slot) DO UPDATE SET entity_id = excluded.entity_id, name = excluded.name, quality = excluded.quality, is_group = excluded.is_group`,
		slot, owned.ID().String(), owned.Name(), int(owned.Quality()), sqliteBool(owned.IsGroup()))
	if err != nil {
		return model.DisplayMetadata{}, sqliteOperationError(err)
	}
	return owned, nil
}

func updateSQLiteChatDisplay(ctx context.Context, tx *sql.Tx, metadata model.DisplayMetadata) (model.DisplayMetadata, error) {
	var name string
	var quality, group int
	err := tx.QueryRowContext(ctx, `SELECT display_name, display_quality, is_group FROM chats WHERE chat_id = ?`, metadata.ID().String()).Scan(&name, &quality, &group)
	if errors.Is(err, sql.ErrNoRows) {
		return metadata, nil
	}
	if err != nil {
		return model.DisplayMetadata{}, sqliteOperationError(err)
	}
	existing, err := sqliteDisplayValue(metadata.ID().String(), name, quality, group)
	if err != nil {
		return model.DisplayMetadata{}, err
	}
	metadata = existing.Merge(metadata)
	_, err = tx.ExecContext(ctx, `UPDATE chats SET display_name = ?, display_quality = ?, is_group = ?,
	placeholder = CASE WHEN ? != '' THEN 0 ELSE placeholder END WHERE chat_id = ?`,
		metadata.Name(), int(metadata.Quality()), sqliteBool(metadata.IsGroup()), metadata.Name(), metadata.ID().String())
	if err != nil {
		return model.DisplayMetadata{}, sqliteOperationError(err)
	}
	return metadata, nil
}

func applySQLiteCachedDisplay(ctx context.Context, tx *sql.Tx, id model.ChatID) error {
	metadata, slot, err := readSQLiteDisplay(ctx, tx, id)
	if err != nil || slot < 0 {
		return err
	}
	_, err = updateSQLiteChatDisplay(ctx, tx, metadata)
	return err
}
