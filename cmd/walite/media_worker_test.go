package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/mediacache"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

type fakeMediaApplication struct {
	message     model.Message
	payload     []byte
	mu          sync.Mutex
	downloads   int
	started     chan struct{}
	release     <-chan struct{}
	downloadErr error
}

func (application *fakeMediaApplication) MediaMessage(context.Context, model.ChatID, model.MessageID) (model.Message, error) {
	return application.message, nil
}
func (application *fakeMediaApplication) DownloadMedia(ctx context.Context, _ model.Media, file wa.MediaFile) error {
	application.mu.Lock()
	application.downloads++
	started := application.started
	application.started = nil
	application.mu.Unlock()
	if started != nil {
		close(started)
	}
	if application.release != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-application.release:
		}
	}
	if application.downloadErr != nil {
		return application.downloadErr
	}
	_, err := file.Write(application.payload)
	return err
}
func (application *fakeMediaApplication) count() int {
	application.mu.Lock()
	defer application.mu.Unlock()
	return application.downloads
}

type fakePreviewer struct {
	mu     sync.Mutex
	shown  []string
	closed int
	err    error
}

func (preview *fakePreviewer) Show(_ context.Context, path string, _ tui.MediaRequest) error {
	preview.mu.Lock()
	defer preview.mu.Unlock()
	preview.shown = append(preview.shown, path)
	return preview.err
}
func (preview *fakePreviewer) Close() error {
	preview.mu.Lock()
	defer preview.mu.Unlock()
	preview.closed++
	return nil
}

func mediaWorkerMessage(t *testing.T, kind model.MediaKind, name, mime string, payload []byte) model.Message {
	t.Helper()
	media, err := model.NewDownloadableMedia(kind, name, mime, "/mms/file", []byte("key"), []byte("hash"), []byte("encrypted"), uint64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	message, err := model.NewMessage(model.MessageInput{ChatID: "chat", MessageID: "media", SentAt: time.Unix(1, 0).UTC(), Media: media})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func awaitMediaResult(t *testing.T, worker *mediaWorker) tui.MediaResult {
	t.Helper()
	select {
	case result := <-worker.results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("media result timeout")
		return tui.MediaResult{}
	}
}

func TestMediaWorkerDownloadsOncePreviewsThenSavesFromCache(t *testing.T) {
	payload := []byte("image bytes")
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaImage, "holiday.jpg", "image/jpeg", payload), payload: payload}
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	preview := &fakePreviewer{}
	worker := newMediaWorker(context.Background(), application, cache, filepath.Join(t.TempDir(), "Downloads", "walite"), preview)
	defer worker.stop()
	request := tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "image", X: 10, Y: 3, Width: 40, Height: 12}
	if !worker.admit(request) {
		t.Fatal("preview rejected")
	}
	result := awaitMediaResult(t, worker)
	if !result.Previewing || application.count() != 1 || len(preview.shown) != 1 {
		t.Fatalf("result=%+v downloads=%d shown=%v", result, application.count(), preview.shown)
	}
	request.Action = tui.MediaSave
	if !worker.admit(request) {
		t.Fatal("save rejected")
	}
	result = awaitMediaResult(t, worker)
	if result.Previewing || application.count() != 1 || result.Status != "Saved to ~/Downloads/walite/holiday.jpg" {
		t.Fatalf("result=%+v downloads=%d", result, application.count())
	}
}

func TestMediaWorkerPreviewsWebPStickerThroughImageBackend(t *testing.T) {
	payload := []byte("synthetic webp bytes")
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaSticker, "", "image/webp", payload), payload: payload}
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	preview := &fakePreviewer{}
	worker := newMediaWorker(context.Background(), application, cache, t.TempDir(), preview)
	defer worker.stop()
	request := tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "sticker", Width: 30, Height: 10}
	if !worker.admit(request) {
		t.Fatal("sticker preview rejected")
	}
	result := awaitMediaResult(t, worker)
	if !result.Previewing || application.count() != 1 || len(preview.shown) != 1 || filepath.Ext(preview.shown[0]) != ".webp" {
		t.Fatalf("result=%+v downloads=%d shown=%v", result, application.count(), preview.shown)
	}
}

func TestMediaWorkerCancellationPreventsStalePreview(t *testing.T) {
	release := make(chan struct{})
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaImage, "x.jpg", "image/jpeg", []byte("x")), payload: []byte("x"), started: make(chan struct{}), release: release}
	started := application.started
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	preview := &fakePreviewer{}
	worker := newMediaWorker(context.Background(), application, cache, t.TempDir(), preview)
	worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "image", Width: 10, Height: 10})
	<-started
	worker.close()
	close(release)
	worker.stop()
	if len(preview.shown) != 0 {
		t.Fatalf("stale overlay=%v", preview.shown)
	}
}

func TestMediaWorkerControlledPreviewFailureAndTypeGate(t *testing.T) {
	payload := []byte("bytes")
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaImage, "x.jpg", "image/jpeg", payload), payload: payload}
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	worker := newMediaWorker(context.Background(), application, cache, t.TempDir(), &fakePreviewer{err: errors.New("missing")})
	defer worker.stop()
	worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "image", Width: 10, Height: 10})
	if result := awaitMediaResult(t, worker); result.Previewing || result.Status == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestVideoAndGifPlaybackClassRemainOutsideImagePreview(t *testing.T) {
	payload := []byte("synthetic mp4")
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaVideo, "clip.mp4", "video/mp4", payload), payload: payload}
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	preview := &fakePreviewer{}
	worker := newMediaWorker(context.Background(), application, cache, t.TempDir(), preview)
	defer worker.stop()
	worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "video", Width: 10, Height: 10})
	result := awaitMediaResult(t, worker)
	if result.Previewing || result.Status != "Preview is available for images and stickers only" || application.count() != 0 || len(preview.shown) != 0 {
		t.Fatalf("result=%+v downloads=%d shown=%v", result, application.count(), preview.shown)
	}
}

func TestMediaWorkerDisconnectedUncachedIsControlled(t *testing.T) {
	payload := []byte("bytes")
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaImage, "x.jpg", "image/jpeg", payload), payload: payload, downloadErr: wa.ErrMediaUnavailable}
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	worker := newMediaWorker(context.Background(), application, cache, t.TempDir(), &fakePreviewer{})
	defer worker.stop()
	worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "image", Width: 10, Height: 10})
	result := awaitMediaResult(t, worker)
	if result.Previewing || result.Status != "Media download unavailable" || application.count() != 1 {
		t.Fatalf("result=%+v calls=%d", result, application.count())
	}
}

func TestMediaWorkerSaveAcceptsEverySupportedMediaKind(t *testing.T) {
	tests := []struct {
		kind              model.MediaKind
		name, mime, label string
	}{
		{model.MediaImage, "image.jpg", "image/jpeg", "image"},
		{model.MediaVideo, "video.mp4", "video/mp4", "video"},
		{model.MediaDocument, "document.pdf", "application/pdf", "document"},
		{model.MediaAudio, "audio.ogg", "audio/ogg", "audio"},
		{model.MediaSticker, "sticker.webp", "image/webp", "sticker"},
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			payload := []byte(test.label)
			application := &fakeMediaApplication{message: mediaWorkerMessage(t, test.kind, test.name, test.mime, payload), payload: payload}
			cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
			worker := newMediaWorker(context.Background(), application, cache, filepath.Join(t.TempDir(), "downloads"), &fakePreviewer{})
			defer worker.stop()
			worker.admit(tui.MediaRequest{Action: tui.MediaSave, ChatID: "chat", MessageID: "media", Kind: test.label})
			result := awaitMediaResult(t, worker)
			if result.Status == "" || application.count() != 1 {
				t.Fatalf("result=%+v calls=%d", result, application.count())
			}
		})
	}
}

func TestUeberzugArgumentsAndJSONAreDeterministicAndShellFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Café image.jpg")
	request := tui.MediaRequest{X: 7, Y: 3, Width: 42, Height: 12}
	args, data, err := ueberzugCommand(path, request)
	if err != nil || len(args) != 3 || args[0] != "layer" || args[1] != "--output" || args[2] != "x11" {
		t.Fatalf("args=%v err=%v", args, err)
	}
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil || payload["path"] != path || payload["identifier"] != "walite-media-preview" || payload["scaler"] != "contain" {
		t.Fatalf("payload=%s", data)
	}
	if payload["x"] != float64(7) || payload["y"] != float64(3) || payload["max_width"] != float64(42) || payload["max_height"] != float64(12) {
		t.Fatalf("geometry=%v", payload)
	}
}
