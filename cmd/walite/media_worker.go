package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/mediacache"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

const mediaOperationTimeout = 90 * time.Second

type mediaApplication interface {
	MediaMessage(context.Context, model.ChatID, model.MessageID) (model.Message, error)
	DownloadMedia(context.Context, model.Media, wa.MediaFile) error
}

type mediaPreviewer interface {
	Show(context.Context, string, tui.MediaRequest) error
	Close() error
}

type mediaWorker struct {
	ctx                   context.Context
	cancel                context.CancelFunc
	application           mediaApplication
	cache                 *mediacache.Cache
	savePath              string
	preview               mediaPreviewer
	external              externalMediaPreviewer
	requests              chan mediaWorkItem
	results               chan tui.MediaResult
	done                  chan struct{}
	stopOnce              sync.Once
	operationMu           sync.Mutex
	operationCancel       context.CancelFunc
	validityMu            sync.RWMutex
	generation            uint64
	externalStopRequested atomic.Bool
}

type mediaWorkItem struct {
	request    tui.MediaRequest
	generation uint64
}

func newDefaultMediaWorker(ctx context.Context, application applicationService) (*mediaWorker, error) {
	cachePath, err := config.DefaultMediaCachePath()
	if err != nil {
		return nil, err
	}
	savePath, err := config.DefaultMediaSavePath()
	if err != nil {
		return nil, err
	}
	cache, err := mediacache.New(cachePath)
	if err != nil {
		return nil, err
	}
	capability, _ := application.(mediaApplication)
	return newMediaWorker(ctx, capability, cache, savePath, &ueberzugPreviewer{binary: "ueberzugpp"}), nil
}

func newMediaWorker(ctx context.Context, application mediaApplication, cache *mediacache.Cache, savePath string, preview mediaPreviewer) *mediaWorker {
	return newMediaWorkerWithExternalPreviewer(ctx, application, cache, savePath, preview, newProcessExternalPreviewer())
}

func newMediaWorkerWithExternalPreviewer(ctx context.Context, application mediaApplication, cache *mediacache.Cache, savePath string, preview mediaPreviewer, external externalMediaPreviewer) *mediaWorker {
	workerCtx, cancel := context.WithCancel(ctx)
	worker := &mediaWorker{ctx: workerCtx, cancel: cancel, application: application, cache: cache, savePath: savePath, preview: preview, external: external, requests: make(chan mediaWorkItem, 1), results: make(chan tui.MediaResult, 1), done: make(chan struct{})}
	go worker.run()
	return worker
}

func (worker *mediaWorker) admit(request tui.MediaRequest) bool {
	if worker == nil || request.Action < tui.MediaPreview || request.Action > tui.MediaSave || request.ChatID == "" || request.MessageID == "" {
		return false
	}
	generation := worker.invalidate()
	worker.cancelOperation()
	select {
	case <-worker.requests:
	default:
	}
	select {
	case worker.requests <- mediaWorkItem{request: request, generation: generation}:
		return true
	case <-worker.ctx.Done():
		return false
	}
}

func (worker *mediaWorker) close() {
	worker.closeRequest()
}

func (worker *mediaWorker) closeExternal() bool {
	if worker == nil || !worker.external.Active() {
		return false
	}
	worker.externalStopRequested.Store(true)
	worker.closeRequest()
	return true
}

func (worker *mediaWorker) closeRequest() {
	if worker == nil {
		return
	}
	worker.invalidate()
	worker.cancelOperation()
	select {
	case <-worker.requests:
	default:
	}
	select {
	case worker.requests <- mediaWorkItem{}:
	case <-worker.ctx.Done():
	}
}

func (worker *mediaWorker) stop() {
	if worker == nil {
		return
	}
	worker.stopOnce.Do(func() { worker.invalidate(); worker.cancelOperation(); worker.cancel(); <-worker.done })
}

func (worker *mediaWorker) invalidate() uint64 {
	worker.validityMu.Lock()
	defer worker.validityMu.Unlock()
	worker.generation++
	return worker.generation
}

func (worker *mediaWorker) cancelOperation() {
	worker.operationMu.Lock()
	if worker.operationCancel != nil {
		worker.operationCancel()
	}
	worker.operationMu.Unlock()
}

func (worker *mediaWorker) run() {
	defer close(worker.done)
	defer close(worker.results)
	defer worker.preview.Close()
	defer worker.external.Close()
	for {
		select {
		case <-worker.ctx.Done():
			return
		case item := <-worker.requests:
			if worker.externalStopRequested.Swap(false) {
				worker.external.StopActive()
			}
			_ = worker.preview.Close()
			request := item.request
			if request.Action == 0 {
				worker.operationMu.Lock()
				worker.operationCancel = nil
				worker.operationMu.Unlock()
				continue
			}
			operationCtx, cancel := context.WithTimeout(worker.ctx, mediaOperationTimeout)
			worker.operationMu.Lock()
			worker.operationCancel = cancel
			worker.operationMu.Unlock()
			result := worker.handle(operationCtx, request, item.generation)
			operationErr := operationCtx.Err()
			cancel()
			if !result.Previewing || operationErr != nil {
				worker.operationMu.Lock()
				worker.operationCancel = nil
				worker.operationMu.Unlock()
			}
			if errors.Is(operationErr, context.Canceled) && worker.ctx.Err() == nil {
				continue
			}
			select {
			case worker.results <- result:
			default:
				select {
				case <-worker.results:
				default:
				}
				select {
				case worker.results <- result:
				default:
				}
			}
		}
	}
}

func (worker *mediaWorker) handle(ctx context.Context, request tui.MediaRequest, generation uint64) tui.MediaResult {
	result := tui.MediaResult{Action: request.Action, ChatID: request.ChatID, MessageID: request.MessageID}
	if worker.application == nil || worker.cache == nil {
		result.Status = "Media unavailable while offline"
		return result
	}
	chatID, chatErr := model.NewChatID(request.ChatID)
	messageID, messageErr := model.NewMessageID(request.MessageID)
	if chatErr != nil || messageErr != nil {
		result.Status = "Media request rejected"
		return result
	}
	message, err := worker.application.MediaMessage(ctx, chatID, messageID)
	if err != nil || message.Media().Kind().String() != request.Kind {
		result.Status = "Media is no longer available"
		return result
	}
	backend := previewBackendFor(message.Media())
	if request.Action == tui.MediaPreview && backend == previewBackendUnsupported {
		result.Status = "Preview not available for this document type"
		return result
	}
	limit := mediacache.MaxSaveBytes
	if request.Action == tui.MediaPreview {
		limit = mediacache.MaxPreviewBytes
	}
	path, cached, err := worker.cache.Ensure(ctx, request.ChatID, request.MessageID, message.Media(), limit,
		func(ctx context.Context, media model.Media, file mediacache.File) error {
			return worker.application.DownloadMedia(ctx, media, file)
		})
	if err != nil {
		switch {
		case errors.Is(err, mediacache.ErrTooLarge):
			result.Status = "Media exceeds the action size limit"
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			result.Status = "Media operation canceled"
		default:
			result.Status = "Media download unavailable"
		}
		return result
	}
	if err := worker.revalidate(ctx, generation, chatID, messageID, message); err != nil {
		result.Status = "Media operation canceled"
		return result
	}
	if request.Action == tui.MediaSave {
		saved, err := worker.cache.Save(path, worker.savePath, message.Media())
		if err != nil {
			result.Status = "Media save failed"
			return result
		}
		result.Status = "Saved to ~/Downloads/walite/" + filepath.Base(saved)
		return result
	}
	if backend == previewBackendImage {
		if request.Width < 1 || request.Height < 1 {
			result.Status = "Terminal too small for image preview"
			return result
		}
		_, err := worker.showImagePreview(ctx, generation, path, request)
		if err != nil {
			result.Status = "Image preview unavailable (install ueberzugpp with X11 support)"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				result.Status = "Media operation canceled"
			}
			return result
		}
		result.Previewing = true
		if cached {
			result.Status = "Previewing cached image — P or navigation closes"
		} else {
			result.Status = "Previewing image — P or navigation closes"
		}
		return result
	}

	externalKind := externalViewerMPVVideo
	label := "Video"
	if backend == previewBackendAudio {
		externalKind = externalViewerMPV
		label = "Audio"
	} else if backend == previewBackendPDF {
		externalKind = externalViewerZathura
		label = "PDF"
	}
	err = worker.showExternalPreview(ctx, generation, externalPreview{kind: externalKind, path: path, chatID: request.ChatID, messageID: request.MessageID})
	if err != nil {
		result.Status = externalPreviewFailureStatus(label, err)
		return result
	}
	if externalKind == externalViewerZathura {
		result.Status = "Opened PDF in zathura"
	} else {
		result.Status = "Opened " + strings.ToLower(label) + " in mpv"
	}
	return result
}

type previewBackend uint8

const (
	previewBackendUnsupported previewBackend = iota
	previewBackendImage
	previewBackendVideo
	previewBackendAudio
	previewBackendPDF
)

func previewBackendFor(mediaValue model.Media) previewBackend {
	switch mediaValue.Kind() {
	case model.MediaImage, model.MediaSticker:
		return previewBackendImage
	case model.MediaVideo:
		return previewBackendVideo
	case model.MediaAudio:
		return previewBackendAudio
	case model.MediaDocument:
		mediaType, _, err := mime.ParseMediaType(mediaValue.MIMEType())
		if err == nil && strings.EqualFold(mediaType, "application/pdf") {
			return previewBackendPDF
		}
	}
	return previewBackendUnsupported
}

func externalPreviewFailureStatus(label string, err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "Media operation canceled"
	case errors.Is(err, errExternalViewerDuplicate):
		return label + " preview is already open"
	case errors.Is(err, errExternalViewerBusy):
		return "Close the current external preview before opening another"
	case errors.Is(err, errMPVUnavailable):
		return "mpv is not installed"
	case errors.Is(err, errZathuraUnavailable):
		return "zathura is not installed"
	default:
		return label + " preview could not be opened"
	}
}

func (worker *mediaWorker) revalidate(ctx context.Context, generation uint64, chatID model.ChatID, messageID model.MessageID, original model.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := worker.application.MediaMessage(ctx, chatID, messageID)
	if err != nil || current.MessageID() != original.MessageID() || current.Media() != original.Media() {
		return errors.New("media identity changed")
	}
	worker.validityMu.RLock()
	defer worker.validityMu.RUnlock()
	if worker.generation != generation {
		return context.Canceled
	}
	return ctx.Err()
}

func (worker *mediaWorker) showImagePreview(operationCtx context.Context, generation uint64, path string, request tui.MediaRequest) (context.CancelFunc, error) {
	worker.validityMu.RLock()
	defer worker.validityMu.RUnlock()
	if worker.generation != generation {
		return nil, context.Canceled
	}
	previewCtx, cancel := context.WithCancel(worker.ctx)
	worker.operationMu.Lock()
	if err := operationCtx.Err(); err != nil {
		worker.operationMu.Unlock()
		cancel()
		return nil, err
	}
	worker.operationCancel = cancel
	worker.operationMu.Unlock()
	if err := worker.preview.Show(previewCtx, path, request); err != nil {
		cancel()
		return nil, err
	}
	return cancel, nil
}

func (worker *mediaWorker) showExternalPreview(ctx context.Context, generation uint64, preview externalPreview) error {
	worker.validityMu.RLock()
	defer worker.validityMu.RUnlock()
	if worker.generation != generation {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return worker.external.Show(ctx, preview)
}

type ueberzugPreviewer struct {
	binary  string
	command *exec.Cmd
	stdin   io.WriteCloser
}

func (preview *ueberzugPreviewer) Show(ctx context.Context, path string, request tui.MediaRequest) error {
	if preview == nil || ctx == nil || !filepath.IsAbs(path) {
		return errors.New("preview rejected")
	}
	if err := preview.Close(); err != nil {
		return err
	}
	binary, err := exec.LookPath(preview.binary)
	if err != nil {
		return err
	}
	args, data, err := ueberzugCommand(path, request)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, binary, args...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	_, err = stdin.Write(append(data, '\n'))
	if err != nil {
		_ = stdin.Close()
		_ = command.Wait()
		return err
	}
	preview.command, preview.stdin = command, stdin
	return nil
}

func ueberzugCommand(path string, request tui.MediaRequest) ([]string, []byte, error) {
	if !filepath.IsAbs(path) || request.X < 0 || request.Y < 0 || request.Width < 1 || request.Height < 1 {
		return nil, nil, errors.New("preview rejected")
	}
	payload := map[string]any{"action": "add", "identifier": "walite-media-preview", "path": path, "x": request.X, "y": request.Y, "max_width": request.Width, "max_height": request.Height, "scaler": "contain"}
	data, err := json.Marshal(payload)
	return []string{"layer", "--output", "x11"}, data, err
}

func (preview *ueberzugPreviewer) Close() error {
	if preview == nil || preview.command == nil {
		return nil
	}
	data, _ := json.Marshal(map[string]any{"action": "remove", "identifier": "walite-media-preview"})
	_, _ = preview.stdin.Write(append(data, '\n'))
	_ = preview.stdin.Close()
	_ = preview.command.Process.Kill()
	_ = preview.command.Wait()
	preview.command, preview.stdin = nil, nil
	return nil
}
