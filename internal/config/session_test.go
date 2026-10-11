package config

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultWhatsAppSessionPathUsesPlatformDataDirectory(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", root)

	got, err := DefaultWhatsAppSessionPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "walite", "whatsmeow-session.db")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Application Support", "walite", "whatsmeow-session.db")
	}
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
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Application Support", "walite", "whatsmeow-session.db")
	}
	if got != want {
		t.Fatalf("DefaultWhatsAppSessionPath()=%q want=%q", got, want)
	}
}

func TestDefaultWhatsAppSessionPathValidatesPlatformDataRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "relative")
	if runtime.GOOS != "darwin" {
		if _, err := DefaultWhatsAppSessionPath(); err == nil {
			t.Fatal("relative XDG_DATA_HOME accepted")
		}
		return
	}

	// macOS ignores XDG overrides and validates HOME as its active root.
	want := filepath.Join(home, "Library", "Application Support", "walite", "whatsmeow-session.db")
	if got, err := DefaultWhatsAppSessionPath(); err != nil || got != want {
		t.Fatalf("native data=%q want=%q err=%v", got, want, err)
	}
	for _, invalidHome := range []string{"", "relative"} {
		t.Setenv("HOME", invalidHome)
		if _, err := DefaultWhatsAppSessionPath(); err == nil {
			t.Fatalf("invalid HOME %q accepted", invalidHome)
		}
	}
}

func TestDefaultApplicationCachePathUsesPlatformCacheDirectory(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", root)
	got, err := DefaultApplicationCachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "walite", "walite-cache.db")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Caches", "walite", "walite-cache.db")
	}
	if got != want {
		t.Fatalf("DefaultApplicationCachePath()=%q want=%q", got, want)
	}
}

func TestDefaultApplicationCachePathUsesHomeFallbackAndValidatesPlatformRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	want := filepath.Join(home, ".cache", "walite", "walite-cache.db")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Caches", "walite", "walite-cache.db")
	}
	got, err := DefaultApplicationCachePath()
	if err != nil || got != want {
		t.Fatalf("fallback=%q want=%q err=%v", got, want, err)
	}
	t.Setenv("XDG_CACHE_HOME", "relative")
	if runtime.GOOS != "darwin" {
		if _, err := DefaultApplicationCachePath(); err == nil {
			t.Fatal("relative XDG_CACHE_HOME accepted")
		}
		return
	}

	if got, err := DefaultApplicationCachePath(); err != nil || got != want {
		t.Fatalf("native cache=%q want=%q err=%v", got, want, err)
	}
	for _, invalidHome := range []string{"", "relative"} {
		t.Setenv("HOME", invalidHome)
		if _, err := DefaultApplicationCachePath(); err == nil {
			t.Fatalf("invalid HOME %q accepted", invalidHome)
		}
	}
}

func TestDefaultMediaPathsUseSeparateCacheAndDownloadsRoots(t *testing.T) {
	cacheRoot, home := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("HOME", home)
	wantCache := filepath.Join(cacheRoot, "walite", "media")
	if runtime.GOOS == "darwin" {
		wantCache = filepath.Join(home, "Library", "Caches", "walite", "media")
	}
	cachePath, err := DefaultMediaCachePath()
	if err != nil || cachePath != wantCache {
		t.Fatalf("cache=%q want=%q err=%v", cachePath, wantCache, err)
	}
	savePath, err := DefaultMediaSavePath()
	if err != nil || savePath != filepath.Join(home, "Downloads", "walite") {
		t.Fatalf("save=%q err=%v", savePath, err)
	}
}

func TestDarwinUsesNativeApplicationSupportCacheAndDownloadsDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Native macOS roots must win even when valid XDG overrides are present.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	directories := systemUserDirectories{goos: "darwin"}

	configDirectory, err := directories.ConfigDirectory()
	if err != nil || configDirectory != filepath.Join(home, "Library", "Application Support") {
		t.Fatalf("config=%q err=%v", configDirectory, err)
	}
	dataDirectory, err := directories.DataDirectory()
	if err != nil || dataDirectory != configDirectory {
		t.Fatalf("data=%q config=%q err=%v", dataDirectory, configDirectory, err)
	}
	cacheDirectory, err := directories.CacheDirectory()
	if err != nil || cacheDirectory != filepath.Join(home, "Library", "Caches") {
		t.Fatalf("cache=%q err=%v", cacheDirectory, err)
	}
	downloads, err := directories.DownloadsDirectory()
	if err != nil || downloads != filepath.Join(home, "Downloads") {
		t.Fatalf("downloads=%q err=%v", downloads, err)
	}
}

func TestUnknownPlatformDataAndDownloadsRemainUnsupported(t *testing.T) {
	directories := systemUserDirectories{goos: "plan9"}
	if _, err := directories.DataDirectory(); !errors.Is(err, ErrUnsupportedUserDirectories) {
		t.Fatalf("data error=%v", err)
	}
	if _, err := directories.DownloadsDirectory(); !errors.Is(err, ErrUnsupportedUserDirectories) {
		t.Fatalf("downloads error=%v", err)
	}
}

func TestDarwinUserDirectoriesRejectInvalidHome(t *testing.T) {
	directories := systemUserDirectories{goos: "darwin"}
	for _, home := range []string{"", "relative"} {
		t.Run("home="+home, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			for name, directory := range map[string]func() (string, error){
				"config":    directories.ConfigDirectory,
				"cache":     directories.CacheDirectory,
				"data":      directories.DataDirectory,
				"downloads": directories.DownloadsDirectory,
			} {
				if path, err := directory(); err == nil || path != "" {
					t.Fatalf("%s accepted invalid HOME %q: path=%q err=%v", name, home, path, err)
				}
			}
		})
	}
}

func TestLinuxDataDirectoryPolicy(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	directories := systemUserDirectories{goos: "linux"}
	for _, test := range []struct {
		name, home, data, want string
		wantError              bool
	}{
		{"override", home, data, data, false},
		{"fallback", home, "", filepath.Join(home, ".local", "share"), false},
		{"relative override", home, "relative", "", true},
		{"missing home", "", "", "", true},
		{"relative home", "relative", "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", test.home)
			t.Setenv("XDG_DATA_HOME", test.data)
			got, err := directories.DataDirectory()
			if test.wantError {
				if err == nil || got != "" {
					t.Fatalf("invalid root accepted: path=%q err=%v", got, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("data=%q want=%q err=%v", got, test.want, err)
			}
		})
	}
}
