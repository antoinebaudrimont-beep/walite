package service

import (
	"context"
	"encoding/binary"
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
	media func(context.Context, model.ChatID, string, model.Media, uint64, StickerMetadata) (model.Event, error)
	calls atomic.Int32
}

func (*mediaSenderFixture) SendText(context.Context, model.ChatID, string, ...model.TextQuote) (model.Event, error) {
	return model.Event{}, errors.New("unexpected text send")
}

func (sender *mediaSenderFixture) SendMedia(ctx context.Context, chatID model.ChatID, path string, media model.Media, size uint64, sticker StickerMetadata) (model.Event, error) {
	sender.calls.Add(1)
	return sender.media(ctx, chatID, path, media, size, sticker)
}

func TestNewSendMediaRequestClassifiesContentAndBoundsMetadata(t *testing.T) {
	tests := []struct {
		name, filename string
		data           []byte
		kind           model.MediaKind
		mime           string
	}{
		{"image", "not-really.txt", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), model.MediaImage, "image/png"},
		{"video", "movie.bin", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isommp42"), model.MediaVideo, "video/mp4"},
		{"audio", "sound.data", []byte("ID3\x04\x00\x00\x00\x00\x00\x00audio"), model.MediaAudio, "audio/mpeg"},
		{"document", "report.jpg", []byte("%PDF-1.7\nsynthetic"), model.MediaDocument, "application/pdf"},
		{"static sticker", "static.bin", syntheticStickerWebP(512, 512, false), model.MediaSticker, "image/webp"},
		{"animated sticker", "animated.bin", syntheticStickerWebP(512, 512, true), model.MediaSticker, "image/webp"},
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
			if test.kind == model.MediaSticker && (request.Sticker().Width() != 512 || request.Sticker().Height() != 512 || request.Sticker().Animated() != strings.Contains(test.name, "animated")) {
				t.Fatalf("sticker=%+v", request.Sticker())
			}
		})
	}
}

func TestNewSendMediaRequestRejectsInvalidOrIncompatibleWebP(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"renamed non-WebP", []byte("not a WebP")},
		{"wrong dimensions", syntheticStickerWebP(511, 512, false)},
		{"short animated frame", syntheticStickerWebPWithDuration(512, 512, 7)},
		{"long animation", syntheticAnimatedStickerWebP(512, 512, []uint32{5_001, 5_001})},
		{"static too large", paddedWebP(syntheticStickerWebP(512, 512, false), MaxStaticStickerBytes+2)},
		{"animated too large", paddedWebP(syntheticStickerWebP(512, 512, true), MaxAnimatedStickerBytes+2)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sticker.webp")
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewSendMediaRequest("123@lid", path); !errors.Is(err, ErrStickerRejected) || !errors.Is(err, ErrLocalMediaRejected) {
				t.Fatalf("error=%v", err)
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
	sender.media = func(_ context.Context, chatID model.ChatID, gotPath string, media model.Media, size uint64, sticker StickerMetadata) (model.Event, error) {
		if stats := core.realtimeQ.Stats(); stats.Reservations != 1 || stats.UsedBytes != model.MaxNormalizedEventBytes {
			t.Fatalf("transport preceded reservation: %+v", stats)
		}
		if gotPath != path || media.Name() != "Café $(no-shell).pdf" || size != uint64(len(data)) {
			t.Fatalf("path=%q media=%+v size=%d", gotPath, media, size)
		}
		if !sticker.IsZero() {
			t.Fatalf("document carried sticker metadata: %+v", sticker)
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
	path := filepath.Join(t.TempDir(), "sticker.webp")
	if err := os.WriteFile(path, syntheticStickerWebP(512, 512, false), 0o600); err != nil {
		t.Fatal(err)
	}
	request, _ := NewSendMediaRequest("123@lid", path)
	failure := errors.New("synthetic upload failure")
	sender := &mediaSenderFixture{media: func(context.Context, model.ChatID, string, model.Media, uint64, StickerMetadata) (model.Event, error) {
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
	path := filepath.Join(t.TempDir(), "sticker.webp")
	data := syntheticStickerWebP(512, 512, true)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	request, _ := NewSendMediaRequest("pipeline-chat", path)
	sender := &mediaSenderFixture{media: func(_ context.Context, chatID model.ChatID, _ string, media model.Media, size uint64, sticker StickerMetadata) (model.Event, error) {
		if !sticker.Animated() {
			t.Fatal("animated sticker metadata lost before transport")
		}
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
	if live.Message().Media().Kind() != model.MediaSticker || live.Message().MessageID().String() != "committed-media" || !live.Message().FromMe() {
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

func syntheticStickerWebP(width, height uint32, animated bool) []byte {
	if animated {
		return syntheticStickerWebPWithDuration(width, height, 100)
	}
	return webPFile(webPTestChunk("VP8L", losslessWebPHeader(width, height)))
}

func syntheticStickerWebPWithDuration(width, height, duration uint32) []byte {
	return syntheticAnimatedStickerWebP(width, height, []uint32{duration})
}

func syntheticAnimatedStickerWebP(width, height uint32, durations []uint32) []byte {
	canvas := make([]byte, 10)
	canvas[0] = 0x02
	putLittleEndian24(canvas[4:7], width-1)
	putLittleEndian24(canvas[7:10], height-1)
	chunks := [][]byte{webPTestChunk("VP8X", canvas), webPTestChunk("ANIM", make([]byte, 6))}
	for _, duration := range durations {
		frame := make([]byte, 16)
		putLittleEndian24(frame[6:9], width-1)
		putLittleEndian24(frame[9:12], height-1)
		putLittleEndian24(frame[12:15], duration)
		frame = append(frame, webPTestChunk("VP8L", losslessWebPHeader(width, height))...)
		chunks = append(chunks, webPTestChunk("ANMF", frame))
	}
	return webPFile(chunks...)
}

func losslessWebPHeader(width, height uint32) []byte {
	bits := width - 1 | (height-1)<<14
	payload := make([]byte, 5)
	payload[0] = 0x2f
	binary.LittleEndian.PutUint32(payload[1:], bits)
	return payload
}

func webPTestChunk(kind string, payload []byte) []byte {
	chunk := make([]byte, 8, 8+len(payload)+len(payload)%2)
	copy(chunk[:4], kind)
	binary.LittleEndian.PutUint32(chunk[4:], uint32(len(payload)))
	chunk = append(chunk, payload...)
	if len(payload)%2 != 0 {
		chunk = append(chunk, 0)
	}
	return chunk
}

func webPFile(chunks ...[]byte) []byte {
	file := []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}
	for _, chunk := range chunks {
		file = append(file, chunk...)
	}
	binary.LittleEndian.PutUint32(file[4:8], uint32(len(file)-8))
	return file
}

func paddedWebP(file []byte, size int) []byte {
	padding := size - len(file) - 8
	if padding < 0 || padding%2 != 0 {
		panic("invalid synthetic WebP size")
	}
	file = append(file, webPTestChunk("JUNK", make([]byte, padding))...)
	binary.LittleEndian.PutUint32(file[4:8], uint32(len(file)-8))
	return file
}

func putLittleEndian24(destination []byte, value uint32) {
	destination[0], destination[1], destination[2] = byte(value), byte(value>>8), byte(value>>16)
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
