package main

import (
	"context"
	"strings"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

func TestReplyWorkerPreservesBoundedQuoteThroughAdmission(t *testing.T) {
	for _, fail := range []bool{false, true} {
		application := newWorkerTestApplication()
		request := tui.SendRequest{ChatID: "12345@lid", Text: "reply é 日本語 🐧", ReplyToID: "quoted-ID", ReplyToText: strings.Repeat("q", 2*model.MaxQuoteTextBytes), ReplyToFromMe: true}
		application.send = func(_ context.Context, got service.SendTextRequest) error {
			q := got.Reply()
			if got.ChatID().String() != request.ChatID || got.Text() != request.Text || q.MessageID().String() != request.ReplyToID || !q.FromMe() || q.Text() != request.ReplyToText[:model.MaxQuoteTextBytes] {
				t.Error("worker lost bounded quote")
			}
			if fail {
				return wa.ErrTextUncertain
			}
			return nil
		}
		worker := newSendWorker(context.Background(), application)
		if err := worker.admit(context.Background(), request); err != nil {
			worker.stop()
			t.Fatal(err)
		}
		result := <-worker.results
		worker.stop()
		if result.Failed != fail || result.Uncertain != fail || application.calls.Load() != 1 {
			t.Fatalf("result=%+v calls=%d", result, application.calls.Load())
		}
	}
}

func TestReplyWorkerPreservesGroupParticipantThroughSend(t *testing.T) {
	application := newWorkerTestApplication()
	application.send = func(_ context.Context, request service.SendTextRequest) error {
		if request.Reply().MessageID().String() != "quoted-ID" || request.Reply().ParticipantID().String() != "55555@lid" {
			t.Error("send lost group quote participant")
		}
		return nil
	}
	worker := newSendWorker(context.Background(), application)
	err := worker.admit(context.Background(), tui.SendRequest{ChatID: "123-456@g.us", Text: "reply", ReplyToID: "quoted-ID", ReplyToText: "original", ReplyTargetSenderID: "55555@lid", ReplyIsGroup: true})
	if err != nil {
		worker.stop()
		t.Fatal(err)
	}
	result := <-worker.results
	worker.stop()
	if result.Failed || application.calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", result, application.calls.Load())
	}
}
