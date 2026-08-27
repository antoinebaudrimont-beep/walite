package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkSQLitePruneOneChat1000(b *testing.B) {
	benchmarkSQLitePrune(b, 1, 1000)
}

func BenchmarkSQLitePruneMultiChat3000(b *testing.B) {
	benchmarkSQLitePrune(b, 5, 600)
}

func BenchmarkSQLiteCacheSize(b *testing.B) {
	store := openSQLiteBenchmarkStore(b)
	if err := store.PutMessage(context.Background(), maintenanceMessages(b, "benchmark-cache", 1, time.Unix(1, 0).UTC(), time.Second)[0]); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := store.CacheSizeBytes(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkSQLitePrune(b *testing.B, chats, messagesPerChat int) {
	store := openSQLiteBenchmarkStore(b)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	for chatIndex := range chats {
		messages := maintenanceMessages(
			b,
			fmt.Sprintf("benchmark-prune-%02d", chatIndex),
			messagesPerChat,
			now.Add(-365*24*time.Hour),
			time.Minute,
		)
		writeSQLitePageMessages(b, store, messages)
	}

	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		if _, err := store.db.ExecContext(context.Background(), `
UPDATE messages
SET body='benchmark body', body_bytes=14, retained_body=1`); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		result, err := store.Prune(context.Background(), now)
		if err != nil {
			b.Fatal(err)
		}
		if result.BodiesDropped != sqliteMaxPrunedBodies {
			b.Fatalf("bodies dropped=%d", result.BodiesDropped)
		}
	}
	b.ReportMetric(float64(sqliteMaxPrunedBodies), "bodies_pruned/op")
	b.ReportMetric(float64(chats*messagesPerChat), "rows/pass")
}

func openSQLiteBenchmarkStore(b *testing.B) *SQLiteStore {
	b.Helper()
	path := filepath.Join(b.TempDir(), "walite-cache.db")
	store, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := store.Close(); err != nil {
			b.Error(err)
		}
	})
	return store
}
