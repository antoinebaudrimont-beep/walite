package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type readWorkerApplication struct {
	*workerTestApplication
	mark func(context.Context, model.ReadReceiptRequest) error
}

func (application *readWorkerApplication) MarkRead(ctx context.Context, request model.ReadReceiptRequest) error {
	return application.mark(ctx, request)
}

func tuiReadRequest(chat string, at time.Time, ids ...string) tui.ReadReceiptRequest {
	request := tui.ReadReceiptRequest{ChatID: chat, Messages: make([]tui.ReadReceiptMessage, len(ids))}
	for index, id := range ids {
		request.Messages[index] = tui.ReadReceiptMessage{MessageID: id, SentAt: at.Add(time.Duration(index) * time.Second)}
	}
	return request
}

func TestReadReceiptWorkerCoalescesPendingChatFrontier(t *testing.T) {
	base := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	started, release := make(chan struct{}), make(chan struct{})
	calls := make(chan model.ReadReceiptRequest, 3)
	application := &readWorkerApplication{workerTestApplication: newWorkerTestApplication()}
	application.mark = func(ctx context.Context, request model.ReadReceiptRequest) error {
		calls <- request
		if request.NewestSentAt() == base {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	worker := newReadReceiptWorker(context.Background(), application)
	defer worker.stop()
	if !worker.admit(tuiReadRequest("chat@lid", base, "first")) {
		t.Fatal("first receipt rejected")
	}
	first := <-calls
	<-started
	if !worker.admit(tuiReadRequest("chat@lid", base.Add(time.Minute), "second")) ||
		!worker.admit(tuiReadRequest("chat@lid", base.Add(2*time.Minute), "second", "third")) {
		t.Fatal("pending coalescing rejected")
	}
	close(release)
	second := <-calls
	if first.Len() != 1 || second.Len() != 2 || second.NewestSentAt() != base.Add(2*time.Minute).Add(time.Second) {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	one, _ := second.At(0)
	two, _ := second.At(1)
	if one.MessageID().String() != "second" || two.MessageID().String() != "third" {
		t.Fatalf("coalesced IDs=%s,%s", one.MessageID().String(), two.MessageID().String())
	}
}

func TestReadReceiptWorkerQueueIsFixedAndCancellationDoesNotDrain(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	started := make(chan struct{})
	calls := make(chan model.ReadReceiptRequest, readReceiptQueueCapacity+2)
	application := &readWorkerApplication{workerTestApplication: newWorkerTestApplication()}
	application.mark = func(ctx context.Context, request model.ReadReceiptRequest) error {
		calls <- request
		if request.ChatID().String() == "active@lid" {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	worker := newReadReceiptWorker(context.Background(), application)
	if !worker.admit(tuiReadRequest("active@lid", base, "active")) {
		t.Fatal("active rejected")
	}
	<-started
	for index := 0; index < readReceiptQueueCapacity; index++ {
		chat := "queued-" + string(rune('A'+index)) + "@lid"
		if !worker.admit(tuiReadRequest(chat, base, "id")) {
			t.Fatalf("queue rejected index %d", index)
		}
	}
	if worker.admit(tuiReadRequest("overflow@lid", base, "overflow")) {
		t.Fatal("fixed queue accepted overflow")
	}
	worker.stop()
	if len(calls) != 1 {
		t.Fatalf("shutdown drained %d queued network calls", len(calls)-1)
	}
}

func TestReadReceiptWorkerFailureHasNoRetry(t *testing.T) {
	base := time.Unix(200, 0).UTC()
	called := make(chan struct{}, 1)
	application := &readWorkerApplication{workerTestApplication: newWorkerTestApplication()}
	application.mark = func(context.Context, model.ReadReceiptRequest) error {
		called <- struct{}{}
		return errors.New("offline")
	}
	worker := newReadReceiptWorker(context.Background(), application)
	if !worker.admit(tuiReadRequest("chat@lid", base, "id")) {
		t.Fatal("receipt rejected")
	}
	<-called
	worker.stop()
	if len(called) != 0 {
		t.Fatal("failed receipt retried")
	}
}

func TestReadReceiptWorkerRejectsUnavailableAndOversizedRequests(t *testing.T) {
	unavailable := newReadReceiptWorker(context.Background(), newWorkerTestApplication())
	if unavailable.admit(tuiReadRequest("chat@lid", time.Unix(1, 0).UTC(), "id")) {
		t.Fatal("application without receipt capability accepted request")
	}
	unavailable.stop()
	application := &readWorkerApplication{workerTestApplication: newWorkerTestApplication(), mark: func(context.Context, model.ReadReceiptRequest) error { return nil }}
	worker := newReadReceiptWorker(context.Background(), application)
	defer worker.stop()
	oversized := make([]string, model.MaxReadReceiptMessages+1)
	for index := range oversized {
		oversized[index] = "id-" + string(rune('A'+index))
	}
	if worker.admit(tuiReadRequest("chat@lid", time.Unix(1, 0).UTC(), oversized...)) {
		t.Fatal("oversized request accepted")
	}
}
