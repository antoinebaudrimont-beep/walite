package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultWhatsAppSessionPathUsesXDGDataHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)

	got, err := DefaultWhatsAppSessionPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "walite", "whatsmeow-session.db")
	if got != want {
		t.Fatalf("DefaultWhatsAppSessionPath()=%q want=%q", got, want)
	}
}

func TestDefaultWhatsAppSessionPathUsesPrivateDataFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")

	got, err := DefaultWhatsAppSessionPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "share", "walite", "whatsmeow-session.db")
	if got != want {
		t.Fatalf("DefaultWhatsAppSessionPath()=%q want=%q", got, want)
	}
}

func TestDefaultWhatsAppSessionPathRejectsRelativeDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "relative")
	if _, err := DefaultWhatsAppSessionPath(); err == nil {
		t.Fatal("relative XDG_DATA_HOME accepted")
	}
}

func TestDefaultApplicationCachePathUsesSeparateXDGCacheHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	got, err := DefaultApplicationCachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "walite", "walite-cache.db")
	if got != want {
		t.Fatalf("DefaultApplicationCachePath()=%q want=%q", got, want)
	}
}

func TestDefaultApplicationCachePathUsesHomeFallbackAndRejectsRelative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	got, err := DefaultApplicationCachePath()
	if err != nil || got != filepath.Join(home, ".cache", "walite", "walite-cache.db") {
		t.Fatalf("fallback=%q err=%v", got, err)
	}
	t.Setenv("XDG_CACHE_HOME", "relative")
	if _, err := DefaultApplicationCachePath(); err == nil {
		t.Fatal("relative XDG_CACHE_HOME accepted")
	}
}

func TestDefaultMediaPathsUseSeparateCacheAndDownloadsRoots(t *testing.T) {
	cacheRoot, home := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("HOME", home)
	cachePath, err := DefaultMediaCachePath()
	if err != nil || cachePath != filepath.Join(cacheRoot, "walite", "media") {
		t.Fatalf("cache=%q err=%v", cachePath, err)
	}
	savePath, err := DefaultMediaSavePath()
	if err != nil || savePath != filepath.Join(home, "Downloads", "walite") {
		t.Fatalf("save=%q err=%v", savePath, err)
	}
}
