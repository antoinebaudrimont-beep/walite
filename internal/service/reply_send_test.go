package service

import (
	"context"
	"errors"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

type quotedSenderFunc func(context.Context, model.ChatID, string, ...model.TextQuote) (model.Event, error)

func (f quotedSenderFunc) SendText(ctx context.Context, id model.ChatID, text string, quotes ...model.TextQuote) (model.Event, error) {
	return f(ctx, id, text, quotes...)
}

func TestReplySendUsesSameReservationAndSuccessCancellationContract(t *testing.T) {
	for _, mode := range []string{"success", "failure", "success after cancel", "saturated"} {
		t.Run(mode, func(t *testing.T) {
			quote, _ := model.NewTextQuote("quoted-ID", "original é 日本語 🐧", false)
			request, err := NewSendTextRequest("opaque-chat", "reply 👋", quote)
			if err != nil {
				t.Fatal(err)
			}
			var core *Core
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("transport failure")
			sender := quotedSenderFunc(func(_ context.Context, id model.ChatID, text string, quotes ...model.TextQuote) (model.Event, error) {
				calls++
				if stats := core.realtimeQ.Stats(); stats.Reservations != 1 || stats.UsedBytes != model.MaxNormalizedEventBytes {
					t.Fatalf("reply reached transport before reservation: %+v", stats)
				}
				if len(quotes) != 1 || quotes[0] != quote || id != request.ChatID() || text != request.Text() {
					t.Fatal("reply metadata lost")
				}
				if mode == "failure" {
					return model.Event{}, failure
				}
				if mode == "success after cancel" {
					cancel()
				}
				return outgoingTestEvent(t, id, "authoritative-reply", writerTestTime, text), nil
			})
			options := validCoreOptions()
			options.Realtime.Bytes = int64(options.Realtime.Entries) * model.MaxNormalizedEventBytes
			core, err = NewWithTextSender(options, newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime), sender)
			if err != nil {
				t.Fatal(err)
			}
			core.acceptingSends.Store(true)
			if mode == "saturated" {
				for i := 0; i < core.options.Realtime.Entries; i++ {
					if err := core.realtimeQ.TryPut(outgoingTestEvent(t, request.ChatID(), "occupied", writerTestTime, "occupied")); err != nil {
						t.Fatal(err)
					}
				}
			}
			err = core.SendText(ctx, request)
			if mode == "saturated" {
				if !errors.Is(err, &CoreError{Kind: CoreBusy}) || calls != 0 {
					t.Fatalf("saturated reply err=%v calls=%d", err, calls)
				}
				return
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			if mode == "failure" {
				if !errors.Is(err, failure) {
					t.Fatal(err)
				}
				if _, ok := core.realtimeQ.TryTake(); ok {
					t.Fatal("failure admitted an event")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				event, ok := core.realtimeQ.TryTake()
				if !ok {
					t.Fatal("success missing admitted event")
				}
				if event.Value().Message().MessageID().String() != "authoritative-reply" {
					t.Fatal("fabricated identity")
				}
				if event.Value().Message().Quote() != quote {
					t.Fatal("successful transport lost quote before realtime admission")
				}
				_ = event.Release()
				if _, ok := core.realtimeQ.TryTake(); ok {
					t.Fatal("duplicate admission")
				}
			}
			if stats := core.realtimeQ.Stats(); stats.Reservations != 0 || stats.UsedBytes != 0 {
				t.Fatalf("reservation leaked: %+v", stats)
			}
		})
	}
}

func TestSendRequestAllowsOnlyOneValidatedQuote(t *testing.T) {
	quote, _ := model.NewTextQuote("id", "quoted", false)
	for _, quotes := range [][]model.TextQuote{{{}}, {quote, quote}} {
		if _, err := NewSendTextRequest("chat", "reply", quotes...); err == nil {
			t.Fatal("invalid quotes accepted")
		}
	}
}

func TestQuotedMessageFitsMinimumWriterQueueBudget(t *testing.T) {
	const maxMessageBytes = int64(4*model.MaxIdentifierBytes + model.MaxRetainedTextBytes + model.MaxQuoteTextBytes)
	for _, history := range []bool{false, true} {
		options := validCoreOptions()
		queue := &options.LiveWrites
		if history {
			queue = &options.HistoryWrites
		}
		queue.Bytes = maxMessageBytes - 1
		if _, err := New(options, newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime)); err == nil {
			t.Fatal("writer queue accepted less than one maximum quoted message")
		}
		queue.Bytes = maxMessageBytes
		if _, err := New(options, newCoreTestSource(), defaultHistoryStore(t), &historyPolicy{}, newManualClock(writerTestTime)); err != nil {
			t.Fatal(err)
		}
	}
}
