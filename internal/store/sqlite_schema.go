package store

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
