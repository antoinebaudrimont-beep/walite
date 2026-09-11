package mediacache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const (
	MaxCacheBytes     int64  = 256 << 20
	MaxCacheFiles            = 256
	MaxPreviewBytes   uint64 = 25 << 20
	MaxSaveBytes      uint64 = 100 << 20
	maxSavedNameBytes        = 180
)

var (
	ErrUnavailable = errors.New("media unavailable")
	ErrTooLarge    = errors.New("media exceeds size limit")
	ErrUnsafePath  = errors.New("unsafe media path")
)

type Cache struct{ root string }

func New(root string) (*Cache, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, ErrUnsafePath
	}
	return &Cache{root: filepath.Clean(root)}, nil
}

type File interface {
	io.Reader
	io.Writer
	io.Seeker
	io.ReaderAt
	io.WriterAt
	Truncate(int64) error
	Stat() (os.FileInfo, error)
}

type Download func(context.Context, model.Media, File) error

// Ensure returns a complete decrypted cache file. It performs network work
// only when called after an explicit user action and never publishes partials.
func (cache *Cache) Ensure(ctx context.Context, chatID, messageID string, media model.Media, maxBytes uint64, download Download) (string, bool, error) {
	if cache == nil || ctx == nil || chatID == "" || messageID == "" || maxBytes == 0 || maxBytes > uint64(MaxCacheBytes) {
		return "", false, ErrUnavailable
	}
	descriptor, ok := media.Download()
	if !ok {
		return "", false, ErrUnavailable
	}
	if descriptor.DeclaredBytes() > maxBytes {
		return "", false, ErrTooLarge
	}
	if err := ensurePrivateDir(cache.root); err != nil {
		return "", false, err
	}
	path := filepath.Join(cache.root, cacheName(chatID, messageID, media))
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() < 0 || uint64(info.Size()) > maxBytes {
			return "", false, ErrUnsafePath
		}
		if declared := descriptor.DeclaredBytes(); declared > 0 && uint64(info.Size()) != declared {
			if err := os.Remove(path); err != nil {
				return "", false, err
			}
		} else {
			_ = os.Chtimes(path, time.Now(), time.Now())
			if err := cache.prune(path); err != nil {
				return "", false, err
			}
			return path, true, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if download == nil {
		return "", false, ErrUnavailable
	}
	temporary, err := os.CreateTemp(cache.root, ".partial-")
	if err != nil {
		return "", false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	fail := func(err error) (string, bool, error) { _ = temporary.Close(); return "", false, err }
	if err := temporary.Chmod(0o600); err != nil {
		return fail(err)
	}
	if err := download(ctx, media, &boundedFile{File: temporary, max: int64(maxBytes)}); err != nil {
		return fail(err)
	}
	info, err := temporary.Stat()
	if err != nil {
		return fail(err)
	}
	if info.Size() < 0 || uint64(info.Size()) > maxBytes {
		return fail(ErrTooLarge)
	}
	if declared := descriptor.DeclaredBytes(); declared > 0 && uint64(info.Size()) != declared {
		return fail(ErrUnavailable)
	}
	if err := temporary.Sync(); err != nil {
		return fail(err)
	}
	if err := temporary.Close(); err != nil {
		return "", false, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", false, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", false, err
	}
	if err := syncDirectory(cache.root); err != nil {
		return "", false, err
	}
	if err := cache.prune(path); err != nil {
		return "", false, err
	}
	return path, false, nil
}

type boundedFile struct {
	*os.File
	max int64
}

func (file *boundedFile) Write(data []byte) (int, error) {
	position, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if int64(len(data)) > file.max-position {
		return 0, ErrTooLarge
	}
	return file.File.Write(data)
}
func (file *boundedFile) WriteAt(data []byte, offset int64) (int, error) {
	if offset < 0 || int64(len(data)) > file.max-offset {
		return 0, ErrTooLarge
	}
	return file.File.WriteAt(data, offset)
}
func (file *boundedFile) Truncate(size int64) error {
	if size < 0 || size > file.max {
		return ErrTooLarge
	}
	return file.File.Truncate(size)
}

type cacheEntry struct {
	path, name string
	size       int64
	modified   time.Time
}

func (cache *Cache) prune(keep string) error {
	return cache.pruneKeeping(keep)
}

func (cache *Cache) pruneKeeping(keep ...string) error {
	entries, err := os.ReadDir(cache.root)
	if err != nil {
		return err
	}
	files := make([]cacheEntry, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".partial-") {
			_ = os.Remove(filepath.Join(cache.root, entry.Name()))
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		item := cacheEntry{filepath.Join(cache.root, entry.Name()), entry.Name(), info.Size(), info.ModTime()}
		files = append(files, item)
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified.Equal(files[j].modified) {
			return files[i].name < files[j].name
		}
		return files[i].modified.Before(files[j].modified)
	})
	remaining := len(files)
	for _, item := range files {
		if remaining <= MaxCacheFiles && total <= MaxCacheBytes {
			break
		}
		if containsPath(keep, item.path) {
			continue
		}
		if err := os.Remove(item.path); err != nil {
			return err
		}
		total -= item.size
		remaining--
	}
	return nil
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}

func cacheName(chatID, messageID string, media model.Media) string {
	sum := sha256.Sum256([]byte(chatID + "\x00" + messageID))
	return hex.EncodeToString(sum[:]) + trustedExtension(media)
}

func trustedExtension(media model.Media) string {
	ext := strings.ToLower(filepath.Ext(media.Name()))
	if len(ext) <= 10 && ext != "" && strings.IndexFunc(ext[1:], func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) < 0 {
		return ext
	}
	switch strings.ToLower(media.MIMEType()) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "application/pdf":
		return ".pdf"
	default:
		return ".bin"
	}
}

// TypedView returns a private cache artifact whose extension is derived only
// from the trusted MIME type. It never changes the canonical cached download.
// A hard link reuses the existing bytes where supported; the atomic-copy
// fallback remains under the same file/count bounds and pruning policy.
func (cache *Cache) TypedView(cachedPath string, mediaValue model.Media) (string, error) {
	if cache == nil || !filepath.IsAbs(cachedPath) {
		return "", ErrUnsafePath
	}
	extension, ok := viewExtension(mediaValue.MIMEType())
	if !ok {
		return "", ErrUnavailable
	}
	resolved, err := filepath.EvalSymlinks(cachedPath)
	if err != nil || filepath.Dir(resolved) != cache.root {
		return "", ErrUnsafePath
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrUnsafePath
	}
	if strings.EqualFold(filepath.Ext(resolved), extension) {
		return resolved, nil
	}
	base := strings.TrimSuffix(filepath.Base(resolved), filepath.Ext(resolved))
	target := filepath.Join(cache.root, base+".view"+extension)
	if targetInfo, statErr := os.Lstat(target); statErr == nil {
		if !targetInfo.Mode().IsRegular() || targetInfo.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnsafePath
		}
		if err := os.Remove(target); err != nil {
			return "", err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	if err := os.Link(resolved, target); err != nil {
		if err := copyViewArtifact(resolved, target, cache.root); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", err
	}
	if err := syncDirectory(cache.root); err != nil {
		return "", err
	}
	if err := cache.pruneKeeping(resolved, target); err != nil {
		return "", err
	}
	return target, nil
}

func viewExtension(mimeType string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(mediaType) {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	case "audio/ogg":
		return ".ogg", true
	case "audio/mpeg":
		return ".mp3", true
	case "audio/mp4":
		return ".m4a", true
	case "audio/aac":
		return ".aac", true
	case "audio/wav", "audio/x-wav", "audio/vnd.wave":
		return ".wav", true
	case "video/mp4":
		return ".mp4", true
	case "video/quicktime":
		return ".mov", true
	case "application/pdf":
		return ".pdf", true
	default:
		return "", false
	}
}

func copyViewArtifact(sourcePath, targetPath, directory string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	temporary, err := os.CreateTemp(directory, ".view-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	_, copyErr := io.Copy(temporary, source)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(copyErr, syncErr, closeErr)
	}
	return os.Rename(temporaryPath, targetPath)
}

func (cache *Cache) Save(cachedPath, directory string, media model.Media) (string, error) {
	if cache == nil || !filepath.IsAbs(cachedPath) || !filepath.IsAbs(directory) {
		return "", ErrUnsafePath
	}
	resolved, err := filepath.EvalSymlinks(cachedPath)
	if err != nil || filepath.Dir(resolved) != cache.root {
		return "", ErrUnsafePath
	}
	if err := ensurePrivateDir(directory); err != nil {
		return "", err
	}
	source, err := os.Open(resolved)
	if err != nil {
		return "", err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrUnsafePath
	}
	base := safeSavedName(media)
	for index := 0; index < 10_000; index++ {
		name := base
		if index > 0 {
			ext := filepath.Ext(base)
			name = strings.TrimSuffix(base, ext) + fmt.Sprintf(" (%d)", index) + ext
		}
		target := filepath.Join(directory, name)
		temp, err := os.CreateTemp(directory, ".walite-save-")
		if err != nil {
			return "", err
		}
		tempPath := temp.Name()
		_ = temp.Chmod(0o600)
		_, copyErr := io.Copy(temp, source)
		syncErr := temp.Sync()
		closeErr := temp.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			_ = os.Remove(tempPath)
			return "", errors.Join(copyErr, syncErr, closeErr)
		}
		err = os.Link(tempPath, target)
		_ = os.Remove(tempPath)
		if errors.Is(err, os.ErrExist) {
			_, _ = source.Seek(0, io.SeekStart)
			continue
		}
		if err != nil {
			return "", err
		}
		if err := syncDirectory(directory); err != nil {
			return "", err
		}
		return target, nil
	}
	return "", ErrUnavailable
}

func safeSavedName(media model.Media) string {
	name := filepath.Base(strings.ReplaceAll(media.Name(), "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" || name == "." || name == ".." {
		name = media.Kind().String() + trustedExtension(media)
	}
	for len(name) > maxSavedNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if filepath.Ext(name) == "" {
		name += trustedExtension(media)
	}
	return name
}

func ensurePrivateDir(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return ErrUnsafePath
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafePath
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
