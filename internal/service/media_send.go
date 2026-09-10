package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const (
	MaxLocalMediaPathBytes = 4096
	mediaSniffBytes        = 512
)

var ErrLocalMediaRejected = errors.New("local media file rejected")

// SendMediaRequest is an immutable description of one inspected regular
// local file. File contents remain outside the request and are streamed by the
// transport only after Core reserves bounded realtime capacity.
type SendMediaRequest struct {
	chatID model.ChatID
	path   string
	media  model.Media
	size   uint64
}

// NewSendMediaRequest validates, stats and content-sniffs one local file.
func NewSendMediaRequest(chatID, path string) (SendMediaRequest, error) {
	ownedChatID, err := model.NewChatID(chatID)
	if err != nil || path == "" || len(path) > MaxLocalMediaPathBytes || !utf8.ValidString(path) || strings.IndexByte(path, 0) >= 0 {
		return SendMediaRequest{}, ErrLocalMediaRejected
	}
	file, err := os.Open(path)
	if err != nil {
		return SendMediaRequest{}, ErrLocalMediaRejected
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || uint64(info.Size()) > model.MaxOutgoingMediaBytes {
		return SendMediaRequest{}, ErrLocalMediaRejected
	}
	var header [mediaSniffBytes]byte
	read, readErr := io.ReadFull(file, header[:])
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return SendMediaRequest{}, ErrLocalMediaRejected
	}
	mimeType := http.DetectContentType(header[:read])
	kind := classifyOutgoingMedia(mimeType)
	media, err := model.NewMedia(kind, filepath.Base(path), mimeType)
	if err != nil {
		return SendMediaRequest{}, ErrLocalMediaRejected
	}
	return SendMediaRequest{chatID: ownedChatID, path: strings.Clone(path), media: media, size: uint64(info.Size())}, nil
}

func classifyOutgoingMedia(mimeType string) model.MediaKind {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return model.MediaImage
	case strings.HasPrefix(mimeType, "video/"):
		return model.MediaVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return model.MediaAudio
	default:
		return model.MediaDocument
	}
}

func (request SendMediaRequest) ChatID() model.ChatID { return request.chatID }
func (request SendMediaRequest) Path() string         { return request.path }
func (request SendMediaRequest) Media() model.Media   { return request.media }
func (request SendMediaRequest) Size() uint64         { return request.size }

// SendMedia shares text sending's one-in-flight gate and reserves the same
// bounded realtime path before upload or SendMessage can be invoked.
func (core *Core) SendMedia(ctx context.Context, request SendMediaRequest) error {
	if core == nil || ctx == nil || core.mediaSender == nil || !core.acceptingSends.Load() {
		return &CoreError{Kind: CoreClosed, Operation: CoreOperationSend}
	}
	owned, err := cloneSendMediaRequest(request)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return &CoreError{Kind: CoreCancelled, Operation: CoreOperationSend, Cause: err}
	}
	select {
	case core.sendSlot <- struct{}{}:
		defer func() { <-core.sendSlot }()
	default:
		return &CoreError{Kind: CoreBusy, Operation: CoreOperationSend}
	}
	reserved, err := core.realtimeQ.tryReserveCapacity(model.MaxNormalizedEventBytes)
	if err != nil {
		if errors.Is(err, errQueueFull) {
			return &CoreError{Kind: CoreBusy, Operation: CoreOperationSend}
		}
		if errors.Is(err, errQueueStopped) {
			return &CoreError{Kind: CoreClosed, Operation: CoreOperationSend}
		}
		return &CoreError{Kind: CoreInvariant, Operation: CoreOperationSend, Cause: err}
	}
	committed := false
	defer func() {
		if !committed {
			_ = reserved.release()
		}
	}()
	if err := ctx.Err(); err != nil {
		return &CoreError{Kind: CoreCancelled, Operation: CoreOperationSend, Cause: err}
	}
	event, err := core.mediaSender.SendMedia(ctx, owned.ChatID(), owned.Path(), owned.Media(), owned.Size())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			cause := ctx.Err()
			if cause == nil {
				cause = err
			}
			return &CoreError{Kind: CoreCancelled, Operation: CoreOperationSend, Cause: cause}
		}
		return &CoreError{Kind: CoreDegraded, Operation: CoreOperationSend, Cause: err}
	}
	normalized, err := model.NewEvent(event.Message(), event.ReceivedAt())
	media := normalized.Message().Media()
	download, downloadable := media.Download()
	if err != nil || normalized.Message().ChatID() != owned.ChatID() || !normalized.Message().FromMe() ||
		normalized.Message().Text() != "" || media.Kind() != owned.Media().Kind() || media.Name() != owned.Media().Name() ||
		media.MIMEType() != owned.Media().MIMEType() || !downloadable || download.DeclaredBytes() != owned.Size() {
		return &CoreError{Kind: CoreMalformed, Operation: CoreOperationSend}
	}
	if err := reserved.commit(normalized); err != nil {
		return &CoreError{Kind: CoreInvariant, Operation: CoreOperationSend, Cause: err}
	}
	committed = true
	return nil
}

func cloneSendMediaRequest(request SendMediaRequest) (SendMediaRequest, error) {
	chatID, err := model.NewChatID(request.ChatID().String())
	if err != nil || request.Path() == "" || len(request.Path()) > MaxLocalMediaPathBytes || !utf8.ValidString(request.Path()) ||
		request.Size() == 0 || request.Size() > model.MaxOutgoingMediaBytes {
		return SendMediaRequest{}, &CoreError{Kind: CoreMalformed, Operation: CoreOperationSend}
	}
	media, err := model.NewMedia(request.Media().Kind(), request.Media().Name(), request.Media().MIMEType())
	if err != nil || media.Kind() == model.MediaSticker {
		return SendMediaRequest{}, &CoreError{Kind: CoreMalformed, Operation: CoreOperationSend}
	}
	return SendMediaRequest{chatID: chatID, path: strings.Clone(request.Path()), media: media, size: request.Size()}, nil
}
