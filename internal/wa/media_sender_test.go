package wa

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

type fakeMediaSendClient struct {
	connected, loggedIn bool
	uploadCalls         atomic.Int32
	sendCalls           atomic.Int32
	upload              func(context.Context, []byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	send                func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error)
}

func (client *fakeMediaSendClient) IsConnected() bool { return client.connected }
func (client *fakeMediaSendClient) IsLoggedIn() bool  { return client.loggedIn }
func (client *fakeMediaSendClient) UploadReader(ctx context.Context, source io.Reader, temporary io.ReadWriteSeeker, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	client.uploadCalls.Add(1)
	if temporary != nil {
		return whatsmeow.UploadResponse{}, errors.New("caller supplied unexpected temporary file")
	}
	data, err := io.ReadAll(io.LimitReader(source, model.MaxOutgoingMediaBytes+1))
	if err != nil {
		return whatsmeow.UploadResponse{}, err
	}
	return client.upload(ctx, data, kind)
}
func (client *fakeMediaSendClient) SendMessage(ctx context.Context, jid types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	client.sendCalls.Add(1)
	if len(extra) != 0 {
		return whatsmeow.SendResponse{}, errors.New("unexpected send extras")
	}
	return client.send(ctx, jid, message)
}

func TestRealMediaSenderUploadsAndBuildsEachNeutralKind(t *testing.T) {
	tests := []struct {
		kind       model.MediaKind
		mime       string
		upstream   whatsmeow.MediaType
		assertWire func(*testing.T, *waE2E.Message, string, string)
	}{
		{model.MediaImage, "image/png", whatsmeow.MediaImage, func(t *testing.T, message *waE2E.Message, name, mime string) {
			if image := message.GetImageMessage(); image == nil || image.GetMimetype() != mime || image.GetFileLength() == 0 {
				t.Fatalf("image=%+v", image)
			}
		}},
		{model.MediaVideo, "video/mp4", whatsmeow.MediaVideo, func(t *testing.T, message *waE2E.Message, name, mime string) {
			if video := message.GetVideoMessage(); video == nil || video.GetMimetype() != mime || video.GetFileLength() == 0 || video.GetGifPlayback() {
				t.Fatalf("video=%+v", video)
			}
		}},
		{model.MediaAudio, "audio/mpeg", whatsmeow.MediaAudio, func(t *testing.T, message *waE2E.Message, name, mime string) {
			if audio := message.GetAudioMessage(); audio == nil || audio.GetMimetype() != mime || audio.GetFileLength() == 0 || audio.GetPTT() {
				t.Fatalf("audio=%+v", audio)
			}
		}},
		{model.MediaDocument, "application/pdf", whatsmeow.MediaDocument, func(t *testing.T, message *waE2E.Message, name, mime string) {
			if document := message.GetDocumentMessage(); document == nil || document.GetMimetype() != mime || document.GetFileName() != name || document.GetFileLength() == 0 {
				t.Fatalf("document=%+v", document)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.kind.String(), func(t *testing.T) {
			name := "Café $(no-shell) file.bin"
			path := filepath.Join(t.TempDir(), name)
			data := []byte("bounded synthetic media")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			media, _ := model.NewMedia(test.kind, name, test.mime)
			at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
			client := &fakeMediaSendClient{connected: true, loggedIn: true}
			client.upload = func(_ context.Context, got []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
				if !reflect.DeepEqual(got, data) || kind != test.upstream {
					t.Fatalf("upload kind=%q bytes=%q", kind, got)
				}
				return successfulUpload(uint64(len(got))), nil
			}
			client.send = func(_ context.Context, jid types.JID, message *waE2E.Message) (whatsmeow.SendResponse, error) {
				if jid.String() != "123@lid" {
					t.Fatalf("jid=%s", jid)
				}
				test.assertWire(t, message, name, test.mime)
				return whatsmeow.SendResponse{ID: "authoritative-media-id", Timestamp: at}, nil
			}
			sender := &realTextSender{client: client}
			chatID, _ := model.NewChatID("123@lid")
			event, err := sender.SendMedia(context.Background(), chatID, path, media, uint64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			download, ok := event.Message().Media().Download()
			if client.uploadCalls.Load() != 1 || client.sendCalls.Load() != 1 || !event.Message().FromMe() ||
				event.Message().MessageID().String() != "authoritative-media-id" || event.Message().Text() != "" ||
				event.Message().Media().Kind() != test.kind || event.Message().Media().Name() != name || !ok || download.DeclaredBytes() != uint64(len(data)) {
				t.Fatalf("event=%+v upload=%d send=%d", event, client.uploadCalls.Load(), client.sendCalls.Load())
			}
		})
	}
}

func TestRealMediaSenderFailuresDoNotSendOrInventCommittedEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.png")
	data := []byte("synthetic")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	media, _ := model.NewMedia(model.MediaImage, "file.png", "image/png")
	chatID, _ := model.NewChatID("123@lid")
	uploadFailure := errors.New("private upload failure")
	client := &fakeMediaSendClient{connected: true, loggedIn: true}
	client.upload = func(context.Context, []byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		return whatsmeow.UploadResponse{}, uploadFailure
	}
	client.send = func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		return whatsmeow.SendResponse{}, errors.New("must not send")
	}
	event, err := (&realTextSender{client: client}).SendMedia(context.Background(), chatID, path, media, uint64(len(data)))
	if !errors.Is(err, ErrMediaUnavailable) || event.Message().MessageID().String() != "" || client.uploadCalls.Load() != 1 || client.sendCalls.Load() != 0 {
		t.Fatalf("upload failure event=%+v err=%v upload=%d send=%d", event, err, client.uploadCalls.Load(), client.sendCalls.Load())
	}

	client.upload = func(_ context.Context, got []byte, _ whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		return successfulUpload(uint64(len(got))), nil
	}
	client.send = func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		return whatsmeow.SendResponse{}, errors.New("private send failure")
	}
	event, err = (&realTextSender{client: client}).SendMedia(context.Background(), chatID, path, media, uint64(len(data)))
	if !errors.Is(err, ErrMediaSendUncertain) || event.Message().MessageID().String() != "" || client.uploadCalls.Load() != 2 || client.sendCalls.Load() != 1 {
		t.Fatalf("send failure event=%+v err=%v upload=%d send=%d", event, err, client.uploadCalls.Load(), client.sendCalls.Load())
	}
}

func TestRealMediaSenderRejectsStickerSizeMismatchAndCancellationBeforeUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media.webp")
	if err := os.WriteFile(path, []byte("webp"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeMediaSendClient{connected: true, loggedIn: true, upload: func(context.Context, []byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		return successfulUpload(4), nil
	}, send: func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		return whatsmeow.SendResponse{}, nil
	}}
	sender := &realTextSender{client: client}
	chatID, _ := model.NewChatID("123@lid")
	sticker, _ := model.NewMedia(model.MediaSticker, "media.webp", "image/webp")
	if _, err := sender.SendMedia(context.Background(), chatID, path, sticker, 4); !errors.Is(err, ErrMediaSendRejected) {
		t.Fatal(err)
	}
	image, _ := model.NewMedia(model.MediaImage, "media.webp", "image/webp")
	if _, err := sender.SendMedia(context.Background(), chatID, path, image, 5); !errors.Is(err, ErrMediaSendRejected) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sender.SendMedia(ctx, chatID, path, image, 4); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if client.uploadCalls.Load() != 0 || client.sendCalls.Load() != 0 {
		t.Fatal("rejected media reached network")
	}
}

func successfulUpload(size uint64) whatsmeow.UploadResponse {
	return whatsmeow.UploadResponse{
		URL: "https://synthetic.invalid/media", DirectPath: "/v/t62/synthetic", FileLength: size,
		MediaKey: []byte(strings.Repeat("k", 32)), FileSHA256: []byte(strings.Repeat("h", 32)), FileEncSHA256: []byte(strings.Repeat("e", 32)),
	}
}
