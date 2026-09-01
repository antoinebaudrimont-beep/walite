package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/store"
)

func openConnectedTestCache(t *testing.T) *store.SQLiteStore {
	t.Helper()
	cache, err := store.OpenSQLite(context.Background(), store.SQLiteOptions{Path: filepath.Join(t.TempDir(), "cache.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return cache
}
