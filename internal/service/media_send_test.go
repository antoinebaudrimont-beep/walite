package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

type mediaSenderFixture struct {
	media func(context.Context, model.ChatID, string, model.Media, uint64) (model.Event, error)
	calls atomic.Int32
}

func (*mediaSenderFixture) SendText(context.Context, model.ChatID, string, ...model.TextQuote) (model.Event, error) {
	return model.Event{}, errors.New("unexpected text send")
}

func (sender *mediaSenderFixture) SendMedia(ctx context.Context, chatID model.ChatID, path string, media model.Media, size uint64) (model.Event, error) {
	sender.calls.Add(1)
	return sender.media(ctx, chatID, path, media, size)
}

func TestNewSendMediaRequestClassifiesContentAndBoundsMetadata(t *testing.T) {
	tests := []struct {
		name, filename string
		data           []byte
		kind           model.MediaKind
		mime           string
	}{
		{"image", "not-really.txt", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), model.MediaImage, "image/png"},
		{"video", "movie.bin", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isom"), model.MediaVideo, "video/mp4"},
		{"audio", "sound.data", []byte("ID3\x04\x00\x00\x00\x00\x00\x00audio"), model.MediaAudio, "audio/mpeg"},
		{"document", "report.jpg", []byte("%PDF-1.7\nsynthetic"), model.MediaDocument, "application/pdf"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.filename)
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			request, err := NewSendMediaRequest("123@lid", path)
			if err != nil {
				t.Fatal(err)
			}
			if request.Path() != path || request.Size() != uint64(len(test.data)) || request.Media().Kind() != test.kind ||
				request.Media().MIMEType() != test.mime || request.Media().Name() != test.filename {
				t.Fatalf("request path=%q size=%d media=%+v", request.Path(), request.Size(), request.Media())
			}
		})
	}
}

func TestNewSendMediaRequestRejectsInvalidDirectoryAndOversizedFile(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing")
	empty := filepath.Join(directory, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(directory, "oversized")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(model.MaxOutgoingMediaBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", missing, directory, empty, oversized, strings.Repeat("x", MaxLocalMediaPathBytes+1)} {
		if _, err := NewSendMediaRequest("123@lid", path); !errors.Is(err, ErrLocalMediaRejected) {
			t.Errorf("path class not rejected: %v", err)
		}
	}
}

func TestSendMediaUsesReservedCommittedPipelineExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Café $(no-shell).pdf")
	data := []byte("%PDF-1.7\ncontrolled")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	request, err := NewSendMediaRequest("123@lid", path)
	if err != nil {
		t.Fatal(err)
	}
	var core *Core
	sender := &mediaSenderFixture{}
	sender.media = func(_ context.Context, chatID model.ChatID, gotPath string, media model.Media, size uint64) (model.Event, error) {
		if stats := core.realtimeQ.Stats(); stats.Reservations != 1 || stats.UsedBytes != model.MaxNormalizedEventBytes {
			t.Fatalf("transport preceded reservation: %+v", stats)
		}
		if gotPath != path || media.Name() != "Café $(no-shell).pdf" || size != uint64(len(data)) {
			t.Fatalf("path=%q media=%+v size=%d", gotPath, media, size)
		}
		return outgoingMediaTestEvent(t, chatID, "media-id", writerTestTime, media, size), nil
	}
	core, err = NewWithTextSender(validCoreOptions(), newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime), sender)
	if err != nil {
		t.Fatal(err)
	}
	core.acceptingSends.Store(true)
	if err := core.SendMedia(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	lease, ok := core.realtimeQ.TryTake()
	if !ok {
		t.Fatal("media event not admitted")
	}
	message := lease.Value().Message()
	if sender.calls.Load() != 1 || message.Media().Kind() != model.MediaDocument || message.Media().Name() != request.Media().Name() || !message.FromMe() {
		t.Fatalf("calls=%d message=%+v", sender.calls.Load(), message)
	}
	_ = lease.Release()
}

func TestSendMediaFailureAndSaturationNeverCreateCommittedEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nsynthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	request, _ := NewSendMediaRequest("123@lid", path)
	failure := errors.New("synthetic upload failure")
	sender := &mediaSenderFixture{media: func(context.Context, model.ChatID, string, model.Media, uint64) (model.Event, error) {
		return model.Event{}, failure
	}}
	core, err := NewWithTextSender(validCoreOptions(), newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime), sender)
	if err != nil {
		t.Fatal(err)
	}
	core.acceptingSends.Store(true)
	if err := core.SendMedia(context.Background(), request); !errors.Is(err, failure) || !errors.Is(err, &CoreError{Kind: CoreDegraded}) {
		t.Fatalf("failure=%v", err)
	}
	if core.realtimeQ.Stats().Entries != 0 || sender.calls.Load() != 1 {
		t.Fatal("failed transport admitted media")
	}

	options := validCoreOptions()
	options.Realtime.Bytes = int64(options.Realtime.Entries) * model.MaxNormalizedEventBytes
	sender.calls.Store(0)
	core, err = NewWithTextSender(options, newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime), sender)
	if err != nil {
		t.Fatal(err)
	}
	core.acceptingSends.Store(true)
	chatID, _ := model.NewChatID("123@lid")
	for index := 0; index < options.Realtime.Entries; index++ {
		if err := core.realtimeQ.TryPut(outgoingTestEvent(t, chatID, fmt.Sprintf("full-%d", index), writerTestTime.Add(time.Duration(index)*time.Second), "occupied")); err != nil {
			t.Fatal(err)
		}
	}
	if err := core.SendMedia(context.Background(), request); !errors.Is(err, &CoreError{Kind: CoreBusy}) || sender.calls.Load() != 0 {
		t.Fatalf("saturated err=%v calls=%d", err, sender.calls.Load())
	}
	core.realtimeQ.drainAndRelease()
}

func TestSendMediaCommitsToStoreBeforeLivePublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice.mp3")
	data := []byte("ID3\x04\x00\x00\x00\x00\x00\x00audio")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	request, _ := NewSendMediaRequest("pipeline-chat", path)
	sender := &mediaSenderFixture{media: func(_ context.Context, chatID model.ChatID, _ string, media model.Media, size uint64) (model.Event, error) {
		return outgoingMediaTestEvent(t, chatID, "committed-media", writerTestTime, media, size), nil
	}}
	store := &writerStore{}
	core := runningSendCore(t, store, sender)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx) }()
	if ready := <-core.Updates(); ready.Kind() != model.UpdateReady {
		t.Fatal("service not ready")
	}
	updatesDone := make(chan struct{})
	go func() {
		for range core.Updates() {
		}
		close(updatesDone)
	}()
	if err := core.SendMedia(ctx, request); err != nil {
		t.Fatal(err)
	}
	live := <-core.LiveEvents()
	if live.Message().Media().Kind() != model.MediaAudio || live.Message().MessageID().String() != "committed-media" || !live.Message().FromMe() {
		t.Fatalf("live=%+v", live)
	}
	if writes := store.observations(); len(writes) != 1 || writes[0].ids[0] != "committed-media" {
		t.Fatalf("writes=%+v", writes)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-updatesDone
}

func outgoingMediaTestEvent(t *testing.T, chatID model.ChatID, messageID string, sentAt time.Time, media model.Media, size uint64) model.Event {
	t.Helper()
	committed, err := model.NewDownloadableMedia(media.Kind(), media.Name(), media.MIMEType(), "/synthetic/outgoing",
		[]byte(strings.Repeat("k", 32)), []byte(strings.Repeat("h", 32)), []byte(strings.Repeat("e", 32)), size)
	if err != nil {
		t.Fatal(err)
	}
	message, err := model.NewMessage(model.MessageInput{ChatID: chatID.String(), MessageID: messageID, SentAt: sentAt, FromMe: true, Media: committed})
	if err != nil {
		t.Fatal(err)
	}
	event, err := model.NewEvent(message, sentAt)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
