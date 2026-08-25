package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

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
