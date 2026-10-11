package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultCachePathUsesPlatformCacheDirectory(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", root)

	got, err := DefaultCachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "walite", "walite-cache.db")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Caches", "walite", "walite-cache.db")
	}
	if got != want {
		t.Fatalf("DefaultCachePath()=%q want=%q", got, want)
	}
}
