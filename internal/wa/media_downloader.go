package wa

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
)

var (
	ErrMediaUnavailable = errors.New("WhatsApp media unavailable")
	ErrMediaRejected    = errors.New("WhatsApp media request rejected")
)

// MediaFile is the bounded worker-owned random-access destination required by
// the pinned whatsmeow streaming download API.
type MediaFile interface {
	io.Reader
	io.Writer
	io.Seeker
	io.ReaderAt
	io.WriterAt
	Truncate(int64) error
	Stat() (os.FileInfo, error)
}

// MediaDownloader is a capability of the existing connection-owned client.
// It never creates a second WhatsApp client or connection.
type MediaDownloader interface {
	DownloadMedia(context.Context, model.Media, MediaFile) error
}

type realMediaDownloader struct{ client *whatsmeow.Client }

func (connection *Connection) MediaDownloader() MediaDownloader {
	if connection == nil {
		return nil
	}
	client, ok := connection.client.(*whatsmeowConnectionClient)
	if !ok || client.client == nil {
		return nil
	}
	return &realMediaDownloader{client: client.client}
}

func (downloader *realMediaDownloader) DownloadMedia(ctx context.Context, media model.Media, file MediaFile) error {
	if ctx == nil || file == nil || downloader == nil || downloader.client == nil {
		return ErrMediaRejected
	}
	download, ok := media.Download()
	if !ok {
		return ErrMediaUnavailable
	}
	message := neutralDownloadable{
		directPath: download.DirectPath(), mediaKey: download.MediaKey(),
		fileSHA256: download.FileSHA256(), fileEncSHA256: download.FileEncSHA256(), mediaType: upstreamMediaType(media.Kind()),
	}
	if message.mediaType == "" {
		return ErrMediaRejected
	}
	if err := downloader.client.DownloadToFile(ctx, message, file); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return ErrMediaUnavailable
	}
	return nil
}

type neutralDownloadable struct {
	directPath                          string
	mediaKey, fileSHA256, fileEncSHA256 []byte
	mediaType                           whatsmeow.MediaType
}

func (message neutralDownloadable) GetDirectPath() string             { return message.directPath }
func (message neutralDownloadable) GetMediaKey() []byte               { return message.mediaKey }
func (message neutralDownloadable) GetFileSHA256() []byte             { return message.fileSHA256 }
func (message neutralDownloadable) GetFileEncSHA256() []byte          { return message.fileEncSHA256 }
func (message neutralDownloadable) GetMediaType() whatsmeow.MediaType { return message.mediaType }

func upstreamMediaType(kind model.MediaKind) whatsmeow.MediaType {
	switch kind {
	case model.MediaImage, model.MediaSticker:
		return whatsmeow.MediaImage
	case model.MediaVideo:
		return whatsmeow.MediaVideo
	case model.MediaAudio:
		return whatsmeow.MediaAudio
	case model.MediaDocument:
		return whatsmeow.MediaDocument
	default:
		return ""
	}
}
