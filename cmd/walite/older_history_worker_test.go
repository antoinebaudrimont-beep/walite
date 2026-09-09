package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type olderWorkerApplication struct {
	*receiveOnlyChatApplication
	calls atomic.Int32
	load  func(context.Context, model.ChatID, model.MessageID, time.Time, bool, int) ([]model.Message, error)
}

func (application *olderWorkerApplication) LoadOlderMessages(ctx context.Context, chat model.ChatID, message model.MessageID, at time.Time, fromMe bool, count int) ([]model.Message, error) {
	application.calls.Add(1)
	return application.load(ctx, chat, message, at, fromMe, count)
}

func newOlderWorkerApplication() *olderWorkerApplication {
	return &olderWorkerApplication{receiveOnlyChatApplication: &receiveOnlyChatApplication{authenticationReadyService: newAuthenticationReadyService()}}
}

func olderWorkerRequest(chat, message string, revision uint64, at time.Time) tui.OlderHistoryRequest {
	return tui.OlderHistoryRequest{ChatID: chat, Revision: revision, OldestMessageID: message, OldestSentAt: at, Count: tui.OlderHistoryPageSize}
}

func awaitOlderHistoryResult(t *testing.T, worker *olderHistoryWorker) tui.OlderHistoryResult {
	t.Helper()
	select {
	case result := <-worker.results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("older history result timeout")
		return tui.OlderHistoryResult{}
	}
}

func TestOlderHistoryWorkerOneInflightAndRepeatedRequestCoalesces(t *testing.T) {
	application := newOlderWorkerApplication()
	started, release := make(chan struct{}), make(chan struct{})
	application.load = func(_ context.Context, chat model.ChatID, message model.MessageID, _ time.Time, _ bool, count int) ([]model.Message, error) {
		if chat.String() != "chat@lid" || message.String() != "frontier" || count != 50 {
			t.Errorf("request changed chat=%q message=%q count=%d", chat.String(), message.String(), count)
		}
		close(started)
		<-release
		return nil, nil
	}
	worker := newOlderHistoryWorker(context.Background(), application)
	defer worker.stop()
	request := olderWorkerRequest("chat@lid", "frontier", 7, time.Unix(100, 0).UTC())
	if cap(worker.wake) != 1 || cap(worker.results) != 1 || !worker.admit(request) {
		t.Fatal("bounded worker rejected first request")
	}
	<-started
	if !worker.admit(request) {
		t.Fatal("repeated active request was not coalesced")
	}
	close(release)
	result := awaitOlderHistoryResult(t, worker)
	if result.Request != request || result.Kind != tui.OlderHistoryNoMore || application.calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", result, application.calls.Load())
	}
}

func TestOlderHistoryWorkerKeepsOnlyNewestPendingRequest(t *testing.T) {
	application := newOlderWorkerApplication()
	firstStarted, firstRelease := make(chan struct{}), make(chan struct{})
	secondStarted, secondRelease := make(chan struct{}), make(chan struct{})
	application.load = func(_ context.Context, chat model.ChatID, _ model.MessageID, _ time.Time, _ bool, _ int) ([]model.Message, error) {
		switch chat.String() {
		case "a@lid":
			close(firstStarted)
			<-firstRelease
		case "c@lid":
			close(secondStarted)
			<-secondRelease
		default:
			t.Errorf("coalesced request unexpectedly loaded %q", chat.String())
		}
		return nil, nil
	}
	worker := newOlderHistoryWorker(context.Background(), application)
	defer worker.stop()
	base := time.Unix(100, 0).UTC()
	a := olderWorkerRequest("a@lid", "frontier-a", 1, base)
	b := olderWorkerRequest("b@lid", "frontier-b", 1, base)
	c := olderWorkerRequest("c@lid", "frontier-c", 1, base)
	worker.admit(a)
	<-firstStarted
	worker.admit(b)
	worker.admit(c)
	close(firstRelease)
	<-secondStarted
	if result := awaitOlderHistoryResult(t, worker); result.Request != a {
		t.Fatalf("first result=%+v", result)
	}
	close(secondRelease)
	if result := awaitOlderHistoryResult(t, worker); result.Request != c {
		t.Fatalf("pending result=%+v", result)
	}
	if application.calls.Load() != 2 {
		t.Fatalf("calls=%d want A plus newest C", application.calls.Load())
	}
}

func TestOlderHistoryWorkerKeepsNearestBoundedPageAndMetadata(t *testing.T) {
	application := newOlderWorkerApplication()
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	application.load = func(context.Context, model.ChatID, model.MessageID, time.Time, bool, int) ([]model.Message, error) {
		messages := make([]model.Message, 40)
		for index := range messages {
			media := model.Media{}
			quote := model.TextQuote{}
			if index == 0 {
				media, _ = model.NewMedia(model.MediaImage, "", "image/jpeg")
			}
			if index == 1 {
				quoteMedia, _ := model.NewMedia(model.MediaImage, "", "image/jpeg")
				quote, _ = model.NewMediaQuote("quoted-image", "caption Café 👋", false, quoteMedia)
			}
			message, err := model.NewMessage(model.MessageInput{
				ChatID: "chat@lid", MessageID: fmt.Sprintf("message-%02d", 40-index), SentAt: base.Add(-time.Duration(index+1) * time.Minute),
				Text: fmt.Sprintf("body-%02d", index), Media: media, Quote: quote, SenderID: "person@lid",
			})
			if err != nil {
				t.Fatal(err)
			}
			messages[index] = message
		}
		return messages, nil // SQLite continuation order: nearest first.
	}
	worker := newOlderHistoryWorker(context.Background(), application)
	defer worker.stop()
	request := olderWorkerRequest("chat@lid", "frontier", 1, base)
	worker.admit(request)
	result := awaitOlderHistoryResult(t, worker)
	if result.Kind != tui.OlderHistoryLoaded || len(result.Messages) != 40 {
		t.Fatalf("kind=%d messages=%d", result.Kind, len(result.Messages))
	}
	if result.Messages[0].ID != "message-01" || result.Messages[len(result.Messages)-1].ID != "message-40" {
		t.Fatalf("nearest page order first=%q last=%q", result.Messages[0].ID, result.Messages[len(result.Messages)-1].ID)
	}
	if result.Messages[len(result.Messages)-1].MediaKind != "image" || result.Messages[len(result.Messages)-2].ReplyMediaKind != "image" || result.Messages[len(result.Messages)-2].ReplyToText != "caption Café 👋" {
		t.Fatalf("metadata lost: %+v %+v", result.Messages[len(result.Messages)-1], result.Messages[len(result.Messages)-2])
	}
}

func TestOlderHistoryWorkerDisconnectedIsControlled(t *testing.T) {
	worker := newOlderHistoryWorker(context.Background(), newAuthenticationReadyService())
	defer worker.stop()
	request := olderWorkerRequest("chat@lid", "frontier", 1, time.Unix(100, 0).UTC())
	if !worker.admit(request) {
		t.Fatal("disconnected request was not admitted for controlled completion")
	}
	if result := awaitOlderHistoryResult(t, worker); result.Request != request || result.Kind != tui.OlderHistoryUnavailable {
		t.Fatalf("result=%+v", result)
	}
}
