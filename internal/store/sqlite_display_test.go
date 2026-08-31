package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLiteDisplayQualitySurvivesRestartAndFIFOReplacement(t *testing.T) {
	ctx := context.Background()
	disk, path := openTestSQLite(t)
	memory, _ := NewMemory(4)
	apply := func(value model.DisplayMetadata) {
		t.Helper()
		want, err := memory.ApplyDisplayMetadata(ctx, value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := disk.ApplyDisplayMetadata(ctx, value)
		if err != nil || got != want {
			t.Fatalf("display parity: %+v != %+v (%v)", got, want, err)
		}
	}
	metadata, _ := model.NewDisplayMetadata("chat", "Synthetic Café 👋", model.DisplaySaved, true)
	apply(metadata)
	if count := queryCount(t, disk.db, "SELECT COUNT(*) FROM chats"); count != 0 {
		t.Fatal("advisory metadata inserted chat")
	}
	message := testMessage(t, "chat", "one", "body", time.Unix(1, 0).UTC(), false)
	batch := sqliteLiveBatch(t, message)
	if _, err := disk.WriteRealtime(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.WriteRealtime(ctx, batch); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < model.DisplayMetadataCapacity; i++ {
		other, _ := model.NewDisplayMetadata(fmt.Sprintf("person-%d", i), "Synthetic Person", model.DisplayPush, false)
		apply(other)
	}
	if got := queryCount(t, disk.db, "SELECT COUNT(*) FROM display_metadata"); got != model.DisplayMetadataCapacity {
		t.Fatalf("FIFO count=%d", got)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	disk, err = OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	// This person has no chat row: its persisted FIFO entry must also retain
	// source quality, independently of the chat's protected display quality.
	cachedPerson, _ := model.NewDisplayMetadata(fmt.Sprintf("person-%d", model.DisplayMetadataCapacity-1), "lower phone", model.DisplayPhone, false)
	apply(cachedPerson)
	low, _ := model.NewDisplayMetadata("chat", "lower push", model.DisplayPush, false)
	apply(low)
	chat, err := disk.Chat(ctx, message.ChatID())
	if err != nil || chat.DisplayName() != metadata.Name() || !chat.IsGroup() || chat.Placeholder() || chat.UnreadCount() != 1 || !chat.LastMessageAt().Equal(message.SentAt()) {
		t.Fatalf("restart changed chat: %+v %v", chat, err)
	}
	rename, _ := model.NewDisplayMetadata("chat", "Renamed 👨‍👩‍👧‍👦", model.DisplaySaved, true)
	apply(rename)
	empty, _ := model.NewDisplayMetadata("chat", "", model.DisplayOpaque, false)
	apply(empty)
}

func TestSQLiteV1UpgradePreservesCacheAndRollsBackOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback_%t", fail), func(t *testing.T) {
			ctx := context.Background()
			path := testSQLitePath(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			db := rawSQLite(t, path)
			for _, statement := range sqliteSchemaV1 {
				if _, err := db.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			for _, statement := range []string{
				`PRAGMA user_version = 1`,
				`INSERT INTO contacts(contact_id, display_name, updated_at, ingest_seq) VALUES ('person', 'Saved V1', 1000, 1)`,
				`INSERT INTO chats(chat_id, contact_id, display_name, unread_count, last_message_at, updated_at, ingest_seq) VALUES ('chat', 'person', 'V1 Chat', 7, 1000, 1000, 1)`,
				`INSERT INTO messages(chat_id, message_id, sender_id, sent_at, from_me, kind, body, body_bytes, ingest_seq) VALUES ('chat', 'one', 'person', 1000, 0, 0, 'old 👋', 8, 1)`,
			} {
				if _, err := db.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			var hook migrationHook
			if fail {
				hook = func(*sql.Tx) error { return errors.New("synthetic migration failure") }
			}
			store, err := openSQLite(ctx, SQLiteOptions{Path: path}, operatingSystemFS, hook)
			if fail {
				if err == nil {
					store.Close()
					t.Fatal("migration unexpectedly succeeded")
				}
				verify := rawSQLite(t, path)
				if pragmaInt(t, verify, "user_version") != 1 || queryCount(t, verify, "SELECT COUNT(*) FROM messages") != 1 {
					t.Fatal("rollback lost original cache")
				}
				if queryCount(t, verify, "SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name = 'quote_id'") != 0 {
					t.Fatal("partial migration survived")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			id, _ := model.NewChatID("chat")
			mid, _ := model.NewMessageID("one")
			message, err := store.Message(ctx, id, mid)
			if err != nil || message.Text() != "old 👋" || message.SenderID().String() != "person" || message.Quote() != (model.TextQuote{}) {
				t.Fatalf("upgrade message: %+v %v", message, err)
			}
			chat, err := store.Chat(ctx, id)
			if err != nil || chat.DisplayName() != "V1 Chat" || chat.UnreadCount() != 7 {
				t.Fatalf("upgrade chat: %+v %v", chat, err)
			}
			quote, _ := model.NewTextQuote("prior", "quote", false)
			if got, err := store.WriteRealtime(ctx, sqliteLiveBatch(t, message.WithQuote(quote))); err != nil || got.Len() != 0 {
				t.Fatalf("upgraded duplicate: %v", err)
			}
		})
	}
}
