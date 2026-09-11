package store

// V5 retains the bounded transport-neutral author identity needed by group
// quotes. It contains no WhatsApp protobuf or media bytes.
var sqliteSchemaV5 = []string{
	`ALTER TABLE messages ADD COLUMN quote_participant_id TEXT NOT NULL DEFAULT ''`,
}

// V4 stores only bounded, transport-neutral latest reaction state. The target
// is intentionally not a foreign key: WhatsApp may deliver a reaction before
// its base message, and the later message/history import will make it visible.
var sqliteSchemaV4 = []string{
	`CREATE TABLE reactions (
		chat_id TEXT NOT NULL REFERENCES chats(chat_id) ON DELETE CASCADE,
		target_message_id TEXT NOT NULL,
		reactor_id TEXT NOT NULL,
		emoji TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (chat_id, target_message_id, reactor_id)
	)`,
	`CREATE INDEX reactions_target_idx
		ON reactions(chat_id, target_message_id, emoji)`,
}

// V3 adds only the bounded presentation descriptor required to reconstruct a
// media quote after restart. Media bytes and download metadata remain absent.
var sqliteSchemaV3 = []string{
	`ALTER TABLE messages ADD COLUMN quote_media_kind INTEGER NOT NULL DEFAULT 0 CHECK (quote_media_kind BETWEEN 0 AND 5)`,
	`ALTER TABLE messages ADD COLUMN quote_media_name TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN quote_media_mime TEXT NOT NULL DEFAULT ''`,
}

// V2 adds only fields used by today's transport-neutral live messages/names.
// The advisory display cache has the same 128-slot FIFO bound as Memory.
var sqliteSchemaV2 = []string{
	`ALTER TABLE messages ADD COLUMN is_group INTEGER NOT NULL DEFAULT 0 CHECK (is_group IN (0,1))`,
	`ALTER TABLE messages ADD COLUMN quote_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN quote_text TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN quote_from_me INTEGER NOT NULL DEFAULT 0 CHECK (quote_from_me IN (0,1))`,
	`ALTER TABLE chats ADD COLUMN display_quality INTEGER NOT NULL DEFAULT 0 CHECK (display_quality BETWEEN 0 AND 5)`,
	`CREATE TABLE display_metadata (
		slot INTEGER PRIMARY KEY CHECK (slot >= 0 AND slot < 128),
		entity_id TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		quality INTEGER NOT NULL CHECK (quality BETWEEN 0 AND 5),
		is_group INTEGER NOT NULL CHECK (is_group IN (0,1))
	)`,
}

var sqliteSchemaV1 = []string{
	`CREATE TABLE app_meta (
        key TEXT PRIMARY KEY,
        value BLOB NOT NULL,
        updated_at INTEGER NOT NULL
    )`,
	`CREATE TABLE contacts (
        contact_id TEXT PRIMARY KEY,
        display_name TEXT NOT NULL DEFAULT '',
        updated_at INTEGER NOT NULL,
        ingest_seq INTEGER NOT NULL
    )`,
	`CREATE TABLE chats (
        chat_id TEXT PRIMARY KEY,
        contact_id TEXT REFERENCES contacts(contact_id) ON DELETE SET NULL,
        display_name TEXT NOT NULL DEFAULT '',
        is_group INTEGER NOT NULL DEFAULT 0 CHECK (is_group IN (0,1)),
        last_message_at INTEGER NOT NULL DEFAULT 0,
        unread_count INTEGER NOT NULL DEFAULT 0,
        muted INTEGER NOT NULL DEFAULT 0 CHECK (muted IN (0,1)),
        archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
        placeholder INTEGER NOT NULL DEFAULT 0 CHECK (placeholder IN (0,1)),
        updated_at INTEGER NOT NULL,
        ingest_seq INTEGER NOT NULL
    )`,
	`CREATE TABLE messages (
        chat_id TEXT NOT NULL REFERENCES chats(chat_id) ON DELETE CASCADE,
        message_id TEXT NOT NULL,
        sender_id TEXT NOT NULL DEFAULT '',
        sent_at INTEGER NOT NULL,
        from_me INTEGER NOT NULL CHECK (from_me IN (0,1)),
        kind INTEGER NOT NULL,
        body TEXT,
        body_bytes INTEGER NOT NULL DEFAULT 0,
        body_truncated INTEGER NOT NULL DEFAULT 0 CHECK (body_truncated IN (0,1)),
        deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0,1)),
        edited_at INTEGER NOT NULL DEFAULT 0,
        local_revision INTEGER NOT NULL DEFAULT 0,
        ingest_seq INTEGER NOT NULL,
        retained_body INTEGER NOT NULL DEFAULT 1 CHECK (retained_body IN (0,1)),
        PRIMARY KEY (chat_id, message_id)
    )`,
	`CREATE TABLE attachments (
        chat_id TEXT NOT NULL,
        message_id TEXT NOT NULL,
        attachment_id TEXT NOT NULL,
        kind INTEGER NOT NULL,
        display_name TEXT NOT NULL DEFAULT '',
        mime_type TEXT NOT NULL DEFAULT '',
        declared_bytes INTEGER NOT NULL DEFAULT 0,
        download_ref BLOB,
        local_path TEXT,
        local_bytes INTEGER NOT NULL DEFAULT 0,
        downloaded_at INTEGER NOT NULL DEFAULT 0,
        available INTEGER NOT NULL DEFAULT 1 CHECK (available IN (0,1)),
        PRIMARY KEY (chat_id, message_id, attachment_id),
        FOREIGN KEY (chat_id, message_id)
          REFERENCES messages(chat_id, message_id) ON DELETE CASCADE
    )`,
	`CREATE TABLE sync_checkpoints (
        scope TEXT PRIMARY KEY,
        chat_id TEXT,
        anchor_message_id TEXT,
        anchor_sent_at INTEGER NOT NULL DEFAULT 0,
        anchor_from_me INTEGER NOT NULL DEFAULT 0,
        category TEXT NOT NULL DEFAULT '',
        server_progress INTEGER NOT NULL DEFAULT 0,
        updated_at INTEGER NOT NULL,
        degraded INTEGER NOT NULL DEFAULT 0 CHECK (degraded IN (0,1))
    )`,
	`CREATE INDEX chats_recent_idx
        ON chats(archived, last_message_at DESC, chat_id)`,
	`CREATE INDEX messages_page_idx
        ON messages(chat_id, sent_at DESC, message_id DESC)`,
	`CREATE INDEX messages_prune_idx
        ON messages(retained_body, sent_at, chat_id, message_id)`,
	`CREATE INDEX attachments_local_idx
        ON attachments(downloaded_at, local_bytes)
        WHERE local_path IS NOT NULL`,
	`CREATE UNIQUE INDEX sync_checkpoint_chat_idx
        ON sync_checkpoints(chat_id)
        WHERE chat_id IS NOT NULL`,
}
