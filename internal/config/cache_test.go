package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultCachePathUsesXDGCacheHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)

	got, err := DefaultCachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "walite", "walite-cache.db")
	if got != want {
		t.Fatalf("DefaultCachePath()=%q want=%q", got, want)
	}
}
