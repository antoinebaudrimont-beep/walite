package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func BenchmarkSQLiteBatch(b *testing.B) {
	for _, size := range []int{1, 10, 50} {
		b.Run(fmt.Sprintf("Writes_%d", size), func(b *testing.B) {
			store, _ := openBenchmarkSQLite(b)
			messages := make([]model.Message, size)
			for index := range messages {
				message, err := model.NewMessage(model.MessageInput{
					ChatID:    "benchmark-batch-chat",
					MessageID: fmt.Sprintf("benchmark-message-%02d", index),
					SentAt:    time.UnixMilli(int64(index + 1)).UTC(),
					Text:      "bounded benchmark body",
				})
				if err != nil {
					b.Fatal(err)
				}
				messages[index] = message
			}
			batch, err := model.NewWriteBatch(model.WriteRealtime, messages)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := store.Write(context.Background(), batch); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if elapsed := b.Elapsed(); elapsed > 0 {
				b.ReportMetric(float64(size*b.N)/elapsed.Seconds(), "rows/s")
			}
		})
	}
}

func BenchmarkSQLiteOpenWarm(b *testing.B) {
	path := filepath.Join(b.TempDir(), "walite", "walite-cache.db")
	store, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
	if err != nil {
		b.Fatal(err)
	}
	if err := store.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		store, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
		if err != nil {
			b.Fatal(err)
		}
		if err := store.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSQLiteSingleMessageUpsert(b *testing.B) {
	store, _ := openBenchmarkSQLite(b)
	message, err := model.NewMessage(model.MessageInput{
		ChatID: "benchmark-chat", MessageID: "benchmark-message",
		SentAt: time.UnixMilli(1).UTC(), Text: "benchmark body",
	})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := store.PutMessage(context.Background(), message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSQLiteSingleMessageLookup(b *testing.B) {
	store, _ := openBenchmarkSQLite(b)
	message, err := model.NewMessage(model.MessageInput{
		ChatID: "benchmark-chat", MessageID: "benchmark-message",
		SentAt: time.UnixMilli(1).UTC(), Text: "benchmark body",
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := store.PutMessage(context.Background(), message); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := store.Message(context.Background(), message.ChatID(), message.MessageID()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSQLitePagination(b *testing.B) {
	const messageCount = 5_000
	store, _ := openBenchmarkSQLite(b)
	writeSQLitePageMessages(b, store, sqliteSequentialMessages(b, "benchmark-page-chat", messageCount))
	chatID := sqlitePageChatID(b, "benchmark-page-chat")

	benchmarks := []struct {
		name   string
		cursor model.Cursor
		limit  int
	}{
		{name: "First_50", cursor: model.NoCursor(), limit: 50},
		{name: "First_100", cursor: model.NoCursor(), limit: 100},
		{name: "Middle_100", cursor: sqliteBenchmarkCursor(b, 2_601), limit: 100},
		{name: "Old_100", cursor: sqliteBenchmarkCursor(b, 201), limit: 100},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				messages, _, err := store.Page(context.Background(), chatID, benchmark.cursor, benchmark.limit)
				if err != nil {
					b.Fatal(err)
				}
				if len(messages) != benchmark.limit {
					b.Fatalf("page len=%d want=%d", len(messages), benchmark.limit)
				}
			}
		})
	}
}

func BenchmarkSQLiteIngestion(b *testing.B) {
	b.Run("NewContactNewChatFirstMessage", func(b *testing.B) {
		store, _ := openBenchmarkSQLite(b)
		b.ReportAllocs()
		b.ResetTimer()
		for index := range b.N {
			sequence := index + 1
			contactID := fmt.Sprintf("benchmark-contact-%06d", sequence)
			chatID := fmt.Sprintf("benchmark-chat-%06d", sequence)
			updatedAt := time.UnixMilli(int64(sequence)).UTC()
			contact, err := model.NewContact(model.ContactInput{
				ID: contactID, DisplayName: "Synthetic Contact", UpdatedAt: updatedAt, IngestSeq: uint64(sequence),
			})
			if err != nil {
				b.Fatal(err)
			}
			chat, err := model.NewChat(model.ChatInput{
				ID: chatID, ContactID: contactID, DisplayName: "Synthetic Contact",
				UpdatedAt: updatedAt, IngestSeq: uint64(sequence),
			})
			if err != nil {
				b.Fatal(err)
			}
			message, err := model.NewMessage(model.MessageInput{
				ChatID: chatID, MessageID: "first-message", SentAt: updatedAt, Text: "synthetic first message",
			})
			if err != nil {
				b.Fatal(err)
			}
			if err := store.UpsertContact(context.Background(), contact); err != nil {
				b.Fatal(err)
			}
			if err := store.EnsureChat(context.Background(), chat); err != nil {
				b.Fatal(err)
			}
			if err := store.PutMessage(context.Background(), message); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ExistingChatIncomingMessage", func(b *testing.B) {
		store, _ := openBenchmarkSQLite(b)
		chat, err := model.NewChat(model.ChatInput{ID: "benchmark-existing-chat", DisplayName: "Existing Chat"})
		if err != nil {
			b.Fatal(err)
		}
		if err := store.EnsureChat(context.Background(), chat); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for index := range b.N {
			message, err := model.NewMessage(model.MessageInput{
				ChatID: chat.ID().String(), MessageID: fmt.Sprintf("incoming-%06d", index+1),
				SentAt: time.UnixMilli(int64(index + 1)).UTC(), Text: "synthetic incoming message",
			})
			if err != nil {
				b.Fatal(err)
			}
			if err := store.PutMessage(context.Background(), message); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func sqliteBenchmarkCursor(b *testing.B, sequence int) model.Cursor {
	b.Helper()
	messageID, err := model.NewMessageID(fmt.Sprintf("message-%06d", sequence))
	if err != nil {
		b.Fatal(err)
	}
	cursor, err := model.NewCursor(time.UnixMilli(int64(sequence)).UTC(), messageID)
	if err != nil {
		b.Fatal(err)
	}
	return cursor
}

func openBenchmarkSQLite(b *testing.B) (*SQLiteStore, string) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "walite", "walite-cache.db")
	store, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	return store, path
}
