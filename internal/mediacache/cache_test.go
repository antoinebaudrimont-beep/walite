package mediacache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func downloadableMedia(t *testing.T, kind model.MediaKind, name, mime string, size uint64) model.Media {
	t.Helper()
	media, err := model.NewDownloadableMedia(kind, name, mime, "/download/path", []byte("key"), []byte("hash"), []byte("encrypted-hash"), size)
	if err != nil {
		t.Fatal(err)
	}
	return media
}

func TestEnsureIsExplicitAtomicPrivateAndReusesCache(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private", "media")
	cache, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("decrypted image bytes")
	media := downloadableMedia(t, model.MediaImage, "holiday.jpg", "image/jpeg", uint64(len(payload)))
	calls := 0
	download := func(_ context.Context, _ model.Media, file File) error {
		calls++
		_, err := file.Write(payload)
		return err
	}
	path, hit, err := cache.Ensure(context.Background(), "chat", "message", media, MaxPreviewBytes, download)
	if err != nil || hit || calls != 1 {
		t.Fatalf("path=%q hit=%t calls=%d err=%v", path, hit, calls, err)
	}
	data, _ := os.ReadFile(path)
	fileInfo, _ := os.Stat(path)
	dirInfo, _ := os.Stat(root)
	if string(data) != string(payload) || fileInfo.Mode().Perm() != 0o600 || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("data=%q modes=%o/%o", data, fileInfo.Mode().Perm(), dirInfo.Mode().Perm())
	}
	if matches, _ := filepath.Glob(filepath.Join(root, ".partial-*")); len(matches) != 0 {
		t.Fatalf("partials=%v", matches)
	}
	_, hit, err = cache.Ensure(context.Background(), "chat", "message", media, MaxPreviewBytes, func(context.Context, model.Media, File) error {
		calls++
		return errors.New("must not download")
	})
	if err != nil || !hit || calls != 1 {
		t.Fatalf("hit=%t calls=%d err=%v", hit, calls, err)
	}
}

func TestEnsureRejectsDeclaredAndActualOversizeWithoutPublishingPartial(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	cache, _ := New(root)
	declared := downloadableMedia(t, model.MediaImage, "x.png", "image/png", 11)
	called := false
	if _, _, err := cache.Ensure(context.Background(), "chat", "declared", declared, 10, func(context.Context, model.Media, File) error { called = true; return nil }); !errors.Is(err, ErrTooLarge) || called {
		t.Fatalf("err=%v called=%t", err, called)
	}
	actual := downloadableMedia(t, model.MediaImage, "x.png", "image/png", 0)
	if _, _, err := cache.Ensure(context.Background(), "chat", "actual", actual, 10, func(_ context.Context, _ model.Media, file File) error {
		_, err := file.Write([]byte("01234567890"))
		return err
	}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v", err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatalf("partial escaped: %v", entries)
	}
}

func TestFailedDownloadLeavesNoFinishedFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	cache, _ := New(root)
	media := downloadableMedia(t, model.MediaDocument, "report.pdf", "application/pdf", 0)
	if _, _, err := cache.Ensure(context.Background(), "chat", "failed", media, MaxSaveBytes, func(_ context.Context, _ model.Media, file File) error {
		_, _ = file.Write([]byte("partial"))
		return errors.New("network failed")
	}); err == nil {
		t.Fatal("failure accepted")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatalf("partial escaped: %v", entries)
	}
}

func TestDeterministicOldestEvictionIsBounded(t *testing.T) {
	root := t.TempDir()
	cache, _ := New(root)
	base := time.Unix(1, 0)
	for index := 0; index < MaxCacheFiles+1; index++ {
		name := fmt.Sprintf("%064x.bin", index)
		if index == MaxCacheFiles {
			name = strings.Repeat("f", 64) + ".bin"
		}
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte{byte(index)}, 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := base.Add(time.Duration(index) * time.Second)
		_ = os.Chtimes(path, stamp, stamp)
	}
	keep := filepath.Join(root, strings.Repeat("f", 64)+".bin")
	if err := cache.prune(keep); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != MaxCacheFiles {
		t.Fatalf("files=%d", len(entries))
	}
}

func TestSaveSanitizesNameNeverOverwritesAndStaysOutsideCache(t *testing.T) {
	root, downloads := filepath.Join(t.TempDir(), "cache"), filepath.Join(t.TempDir(), "downloads")
	cache, _ := New(root)
	payload := []byte("document")
	media := downloadableMedia(t, model.MediaDocument, "../../résumé 👋.pdf", "application/pdf", uint64(len(payload)))
	cached, _, err := cache.Ensure(context.Background(), "chat", "doc", media, MaxSaveBytes, func(_ context.Context, _ model.Media, file File) error {
		_, err := file.Write(payload)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := cache.Save(cached, downloads, media)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.Save(cached, downloads, media)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != downloads || filepath.Dir(second) != downloads || first == second || filepath.Base(first) != "résumé 👋.pdf" || !strings.Contains(filepath.Base(second), "(1)") {
		t.Fatalf("first=%q second=%q", first, second)
	}
	data, _ := os.ReadFile(first)
	if string(data) != string(payload) {
		t.Fatalf("saved=%q", data)
	}
}
