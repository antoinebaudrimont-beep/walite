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
	message      model.Message
	payload      []byte
	mu           sync.Mutex
	downloads    int
	started      chan struct{}
	release      <-chan struct{}
	ignoreCancel bool
	downloadErr  error
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
		if application.ignoreCancel {
			<-application.release
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-application.release:
			}
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

type fakeExternalPreviewer struct {
	mu       sync.Mutex
	previews []externalPreview
	closed   int
	err      error
}

type fakeSystemOpener struct {
	mu      sync.Mutex
	targets []string
	err     error
}

func (opener *fakeSystemOpener) Open(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	opener.mu.Lock()
	defer opener.mu.Unlock()
	opener.targets = append(opener.targets, target)
	return opener.err
}

func (opener *fakeSystemOpener) opened() []string {
	opener.mu.Lock()
	defer opener.mu.Unlock()
	return append([]string(nil), opener.targets...)
}

func (previewer *fakeExternalPreviewer) Show(ctx context.Context, preview externalPreview) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	previewer.mu.Lock()
	defer previewer.mu.Unlock()
	previewer.previews = append(previewer.previews, preview)
	return previewer.err
}

func (previewer *fakeExternalPreviewer) Close() error {
	previewer.mu.Lock()
	defer previewer.mu.Unlock()
	previewer.closed++
	return nil
}

func (previewer *fakeExternalPreviewer) Active() bool { return false }
func (previewer *fakeExternalPreviewer) StopActive()  {}

func (previewer *fakeExternalPreviewer) shown() []externalPreview {
	previewer.mu.Lock()
	defer previewer.mu.Unlock()
	return append([]externalPreview(nil), previewer.previews...)
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

func TestMediaWorkerPreviewsOrdinaryGIFThroughImageBackend(t *testing.T) {
	payload := []byte("synthetic gif bytes")
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaImage, "animation.gif", "image/gif", payload), payload: payload}
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	overlay, external := &fakePreviewer{}, &fakeExternalPreviewer{}
	worker := newMediaWorkerWithExternalPreviewer(context.Background(), application, cache, t.TempDir(), overlay, external)
	defer worker.stop()
	request := tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "image", Width: 30, Height: 10}
	if !worker.admit(request) {
		t.Fatal("GIF preview rejected")
	}
	result := awaitMediaResult(t, worker)
	if !result.Previewing || application.count() != 1 || len(overlay.shown) != 1 || filepath.Ext(overlay.shown[0]) != ".gif" || len(external.shown()) != 0 {
		t.Fatalf("result=%+v downloads=%d overlay=%v external=%+v", result, application.count(), overlay.shown, external.shown())
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

func TestMediaWorkerFallsBackToSystemViewerWhenInlineCapabilityIsUnavailable(t *testing.T) {
	for _, kind := range []model.MediaKind{model.MediaImage, model.MediaSticker} {
		t.Run(kind.String(), func(t *testing.T) {
			payload := []byte("image bytes")
			application := &fakeMediaApplication{message: mediaWorkerMessage(t, kind, "holiday.webp", "image/webp", payload), payload: payload}
			cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
			opener := &fakeSystemOpener{}
			worker := newMediaWorkerWithPlatform(context.Background(), application, cache, t.TempDir(), unavailableInlinePreviewer{}, &fakeExternalPreviewer{}, opener)
			defer worker.stop()
			worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: kind.String(), Width: 30, Height: 10})
			result := awaitMediaResult(t, worker)
			opened := opener.opened()
			if result.Previewing || result.Status != "Opened image with system viewer" || len(opened) != 1 || !filepath.IsAbs(opened[0]) {
				t.Fatalf("result=%+v opened=%v", result, opened)
			}
		})
	}
}

func TestDarwinMediaUsesSystemOpenerForEveryPreviewableKind(t *testing.T) {
	tests := []struct {
		name, file, mime, status string
		kind                     model.MediaKind
	}{
		{name: "image", file: "photo.jpg", mime: "image/jpeg", kind: model.MediaImage, status: "Opened image with system viewer"},
		{name: "GIF", file: "animation.gif", mime: "image/gif", kind: model.MediaImage, status: "Opened image with system viewer"},
		{name: "sticker", file: "sticker.webp", mime: "image/webp", kind: model.MediaSticker, status: "Opened sticker with system viewer"},
		{name: "video", file: "clip.mp4", mime: "video/mp4", kind: model.MediaVideo, status: "Opened video with system viewer"},
		{name: "audio", file: "voice.ogg", mime: "audio/ogg", kind: model.MediaAudio, status: "Opened audio with system viewer"},
		{name: "PDF", file: "report.pdf", mime: "application/pdf", kind: model.MediaDocument, status: "Opened pdf with system viewer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte(test.name)
			application := &fakeMediaApplication{message: mediaWorkerMessage(t, test.kind, test.file, test.mime, payload), payload: payload}
			cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
			opener := &fakeSystemOpener{}
			external := &fakeExternalPreviewer{err: errors.New("external viewer must not be called on darwin")}
			overlay := &fakePreviewer{err: errors.New("inline viewer must not be called on darwin")}
			worker := newMediaWorkerWithPlatform(context.Background(), application, cache, t.TempDir(), overlay, external, opener)
			worker.systemMedia = true
			defer worker.stop()
			worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: test.kind.String()})
			result := awaitMediaResult(t, worker)
			if result.Status != test.status || len(opener.opened()) != 1 || len(external.shown()) != 0 || len(overlay.shown) != 0 {
				t.Fatalf("result=%+v opened=%v external=%v overlay=%v", result, opener.opened(), external.shown(), overlay.shown)
			}
		})
	}
}

func TestMediaWorkerRoutesVideoAudioAndPDFToExternalViewers(t *testing.T) {
	tests := []struct {
		name, file, mime, status string
		kind                     model.MediaKind
		viewer                   externalViewerKind
	}{
		{"video and GifPlayback MP4", "clip.mp4", "video/mp4", "Opened video in mpv", model.MediaVideo, externalViewerMPVVideo},
		{"audio", "voice.ogg", "audio/ogg", "Opened audio in mpv", model.MediaAudio, externalViewerMPV},
		{"PDF", "report.pdf", "application/pdf; version=1.7", "Opened PDF in zathura", model.MediaDocument, externalViewerZathura},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte(test.name)
			application := &fakeMediaApplication{message: mediaWorkerMessage(t, test.kind, test.file, test.mime, payload), payload: payload}
			cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
			overlay, external := &fakePreviewer{}, &fakeExternalPreviewer{}
			worker := newMediaWorkerWithExternalPreviewer(context.Background(), application, cache, t.TempDir(), overlay, external)
			defer worker.stop()
			worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: test.kind.String(), Width: 10, Height: 10})
			result := awaitMediaResult(t, worker)
			shown := external.shown()
			if result.Previewing || result.Status != test.status || application.count() != 1 || len(overlay.shown) != 0 || len(shown) != 1 || shown[0].kind != test.viewer {
				t.Fatalf("result=%+v downloads=%d overlay=%v external=%+v", result, application.count(), overlay.shown, shown)
			}
		})
	}
}

func TestNonPDFDocumentPreviewIsControlledAndDoesNotDownload(t *testing.T) {
	payload := []byte("document")
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaDocument, "report.pdf", "text/plain", payload), payload: payload}
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	external := &fakeExternalPreviewer{}
	worker := newMediaWorkerWithExternalPreviewer(context.Background(), application, cache, t.TempDir(), &fakePreviewer{}, external)
	defer worker.stop()
	worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "document"})
	result := awaitMediaResult(t, worker)
	if result.Previewing || result.Status != "Preview not available for this document type" || application.count() != 0 || len(external.shown()) != 0 {
		t.Fatalf("result=%+v downloads=%d external=%+v", result, application.count(), external.shown())
	}
	worker.admit(tui.MediaRequest{Action: tui.MediaSave, ChatID: "chat", MessageID: "media", Kind: "document"})
	result = awaitMediaResult(t, worker)
	if result.Status == "" || application.count() != 1 {
		t.Fatalf("save result=%+v downloads=%d", result, application.count())
	}
}

func TestExternalPreviewUsesCacheWithoutRedownloading(t *testing.T) {
	payload := []byte("synthetic mp4")
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaVideo, "clip.mp4", "video/mp4", payload), payload: payload}
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	external := &fakeExternalPreviewer{}
	worker := newMediaWorkerWithExternalPreviewer(context.Background(), application, cache, t.TempDir(), &fakePreviewer{}, external)
	defer worker.stop()
	request := tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "video"}
	for index := 0; index < 2; index++ {
		if !worker.admit(request) {
			t.Fatal("preview rejected")
		}
		if result := awaitMediaResult(t, worker); result.Status != "Opened video in mpv" {
			t.Fatalf("result=%+v", result)
		}
	}
	if application.count() != 1 || len(external.shown()) != 2 {
		t.Fatalf("downloads=%d external=%+v", application.count(), external.shown())
	}
}

func TestCanceledDownloadCannotLaunchStaleExternalViewer(t *testing.T) {
	release := make(chan struct{})
	application := &fakeMediaApplication{message: mediaWorkerMessage(t, model.MediaVideo, "clip.mp4", "video/mp4", []byte("mp4")), payload: []byte("mp4"), started: make(chan struct{}), release: release, ignoreCancel: true}
	started := application.started
	cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
	external := &fakeExternalPreviewer{}
	worker := newMediaWorkerWithExternalPreviewer(context.Background(), application, cache, t.TempDir(), &fakePreviewer{}, external)
	worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: "video"})
	<-started
	worker.close()
	close(release)
	worker.stop()
	if len(external.shown()) != 0 {
		t.Fatalf("stale external preview=%+v", external.shown())
	}
}

func TestExternalViewerFailuresProduceControlledStatus(t *testing.T) {
	tests := []struct {
		name, status string
		kind         model.MediaKind
		file, mime   string
		err          error
	}{
		{"missing mpv", "mpv is not installed", model.MediaVideo, "x.mp4", "video/mp4", errMPVUnavailable},
		{"missing zathura", "zathura is not installed", model.MediaDocument, "x.pdf", "application/pdf", errZathuraUnavailable},
		{"duplicate", "Video preview is already open", model.MediaVideo, "x.mp4", "video/mp4", errExternalViewerDuplicate},
		{"busy", "Close the current external preview before opening another", model.MediaAudio, "x.ogg", "audio/ogg", errExternalViewerBusy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte("payload")
			application := &fakeMediaApplication{message: mediaWorkerMessage(t, test.kind, test.file, test.mime, payload), payload: payload}
			cache, _ := mediacache.New(filepath.Join(t.TempDir(), "cache"))
			worker := newMediaWorkerWithExternalPreviewer(context.Background(), application, cache, t.TempDir(), &fakePreviewer{}, &fakeExternalPreviewer{err: test.err})
			defer worker.stop()
			worker.admit(tui.MediaRequest{Action: tui.MediaPreview, ChatID: "chat", MessageID: "media", Kind: test.kind.String()})
			if result := awaitMediaResult(t, worker); result.Status != test.status {
				t.Fatalf("result=%+v", result)
			}
		})
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

func TestExternalViewerArgumentsAreDeterministicAndShellFree(t *testing.T) {
	hostile := filepath.Join(t.TempDir(), "$(touch pwned); --really-quiet.pdf")
	binary, args, err := externalViewerCommand(externalViewerMPV, hostile)
	if err != nil || binary != "mpv" || len(args) != 2 || args[0] != "--" || args[1] != hostile {
		t.Fatalf("mpv binary=%q args=%q err=%v", binary, args, err)
	}
	binary, args, err = externalViewerCommand(externalViewerZathura, hostile)
	if err != nil || binary != "zathura" || len(args) != 1 || args[0] != hostile {
		t.Fatalf("zathura binary=%q args=%q err=%v", binary, args, err)
	}
}

type blockingExternalStop struct {
	fakeExternalPreviewer
	started chan struct{}
	release chan struct{}
}

func (previewer *blockingExternalStop) Active() bool { return true }
func (previewer *blockingExternalStop) StopActive() {
	close(previewer.started)
	<-previewer.release
}

func TestExternalCloseAdmissionDoesNotWaitForProcessExit(t *testing.T) {
	external := &blockingExternalStop{started: make(chan struct{}), release: make(chan struct{})}
	worker := newMediaWorkerWithExternalPreviewer(context.Background(), nil, nil, t.TempDir(), &fakePreviewer{}, external)
	defer worker.stop()
	defer close(external.release)
	// This must return while StopActive is still blocked on process completion.
	if !worker.closeExternal() {
		t.Fatal("active external viewer did not consume Escape")
	}
	select {
	case <-external.started:
	case <-time.After(5 * time.Second):
		t.Fatal("media worker did not process external close")
	}
}
