package wa

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

var (
	ErrMediaSendRejected = errors.New("WhatsApp media send request rejected")
	// ErrMediaSendUncertain means SendMessage was invoked but did not return a
	// usable success. The remote side may have accepted it; never auto-retry.
	ErrMediaSendUncertain = errors.New("WhatsApp media delivery unknown; check recipient before sending again")
)

type mediaUploadClient interface {
	UploadReader(context.Context, io.Reader, io.ReadWriteSeeker, whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
}

// SendMedia uses the connection-owned client. It streams the selected local
// file through whatsmeow's bounded temporary-file upload path, then invokes
// SendMessage exactly once.
func (sender *realTextSender) SendMedia(ctx context.Context, chatID model.ChatID, path string, media model.Media, size uint64, sticker model.StickerSendMetadata) (model.Event, error) {
	if ctx == nil || path == "" || size == 0 || size > model.MaxOutgoingMediaBytes || media.Kind() == 0 ||
		media.Kind() == model.MediaSticker && !sticker.ValidFor(size) || media.Kind() != model.MediaSticker && !sticker.IsZero() {
		return model.Event{}, ErrMediaSendRejected
	}
	if err := ctx.Err(); err != nil {
		return model.Event{}, err
	}
	jid, err := textRecipient(chatID)
	if err != nil {
		return model.Event{}, err
	}
	if sender == nil || sender.client == nil || sender.ready != nil && !sender.ready.Load() {
		return model.Event{}, ErrMediaUnavailable
	}
	if !sender.client.IsConnected() || !sender.client.IsLoggedIn() {
		return model.Event{}, ErrMediaUnavailable
	}
	uploader, ok := sender.client.(mediaUploadClient)
	if !ok {
		return model.Event{}, ErrMediaUnavailable
	}
	alternate := sender.aliases.alternateFor(chatID)
	alternate, err = lookupAlternate(ctx, chatID, alternate, sender.lookup)
	if err != nil {
		return model.Event{}, err
	}
	canonical, err := sender.aliases.resolveForSend(chatID, alternate)
	if err != nil {
		return model.Event{}, err
	}
	if canonical != chatID {
		return model.Event{}, ErrMediaSendRejected
	}
	file, err := os.Open(path)
	if err != nil {
		return model.Event{}, ErrMediaSendRejected
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || uint64(info.Size()) != size {
		return model.Event{}, ErrMediaSendRejected
	}
	uploadType := upstreamMediaType(media.Kind())
	if uploadType == "" {
		return model.Event{}, ErrMediaSendRejected
	}
	// Bound the stream as well as the earlier stat. A concurrently growing file
	// can produce at most one extra byte and is then rejected by FileLength.
	upload, err := uploader.UploadReader(ctx, io.LimitReader(file, int64(size)+1), nil, uploadType)
	if err != nil {
		if ctx.Err() != nil {
			return model.Event{}, ctx.Err()
		}
		return model.Event{}, ErrMediaUnavailable
	}
	if upload.FileLength != size {
		return model.Event{}, ErrMediaSendRejected
	}
	committedMedia, err := model.NewDownloadableMedia(media.Kind(), media.Name(), media.MIMEType(), upload.DirectPath,
		upload.MediaKey, upload.FileSHA256, upload.FileEncSHA256, upload.FileLength)
	if err != nil {
		return model.Event{}, ErrMediaSendRejected
	}
	payload := outgoingMediaMessage(committedMedia, upload, sticker)
	if payload == nil {
		return model.Event{}, ErrMediaSendRejected
	}
	if err := ctx.Err(); err != nil {
		return model.Event{}, err
	}
	response, err := sender.client.SendMessage(ctx, jid, payload)
	if err != nil {
		return model.Event{}, ErrMediaSendUncertain
	}
	message, err := model.NewMessage(model.MessageInput{
		ChatID: chatID.String(), MessageID: string(response.ID), SentAt: response.Timestamp,
		FromMe: true, Media: committedMedia,
	})
	if err != nil {
		return model.Event{}, ErrMediaSendUncertain
	}
	event, err := model.NewEvent(message, response.Timestamp)
	if err != nil {
		return model.Event{}, ErrMediaSendUncertain
	}
	return event, nil
}

func outgoingMediaMessage(media model.Media, upload whatsmeow.UploadResponse, sticker model.StickerSendMetadata) *waE2E.Message {
	url, directPath, mimeType, size := upload.URL, upload.DirectPath, media.MIMEType(), upload.FileLength
	switch media.Kind() {
	case model.MediaImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			URL: &url, DirectPath: &directPath, Mimetype: &mimeType, FileLength: &size,
			MediaKey: upload.MediaKey, FileSHA256: upload.FileSHA256, FileEncSHA256: upload.FileEncSHA256,
		}}
	case model.MediaVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL: &url, DirectPath: &directPath, Mimetype: &mimeType, FileLength: &size,
			MediaKey: upload.MediaKey, FileSHA256: upload.FileSHA256, FileEncSHA256: upload.FileEncSHA256,
		}}
	case model.MediaAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL: &url, DirectPath: &directPath, Mimetype: &mimeType, FileLength: &size,
			MediaKey: upload.MediaKey, FileSHA256: upload.FileSHA256, FileEncSHA256: upload.FileEncSHA256,
		}}
	case model.MediaDocument:
		name := media.Name()
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			URL: &url, DirectPath: &directPath, Mimetype: &mimeType, FileName: &name, FileLength: &size,
			MediaKey: upload.MediaKey, FileSHA256: upload.FileSHA256, FileEncSHA256: upload.FileEncSHA256,
		}}
	case model.MediaSticker:
		width, height, animated := sticker.Width(), sticker.Height(), sticker.Animated()
		return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
			URL: &url, DirectPath: &directPath, Mimetype: &mimeType, FileLength: &size,
			MediaKey: upload.MediaKey, FileSHA256: upload.FileSHA256, FileEncSHA256: upload.FileEncSHA256,
			Width: &width, Height: &height, IsAnimated: &animated,
		}}
	default:
		return nil
	}
}
