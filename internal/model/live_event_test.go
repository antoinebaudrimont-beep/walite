package model

import (
	"strings"
	"testing"
	"time"
)

func TestLiveMessageCommittedOwnsBoundedFidelity(t *testing.T) {
	activity := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	message, err := NewMessage(MessageInput{
		ChatID: "live-chat", MessageID: "live-message", SentAt: activity.Add(-time.Minute),
		FromMe: true, Text: "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦",
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := NewLiveMessageCommitted(message, 7, activity)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind() != LiveMessageCommitted || event.Message().ChatID().String() != "live-chat" ||
		event.Message().MessageID().String() != "live-message" || !event.Message().FromMe() ||
		event.Message().Text() != message.Text() || event.UnreadCount() != 7 ||
		!event.ActivityTime().Equal(activity) || event.ByteSize() <= message.ByteSize() ||
		event.ByteSize() > MaxNormalizedLiveEventBytes {
		t.Fatalf("event fidelity=%+v message=%+v", event, event.Message())
	}

	bodyless, err := NewLiveMessageCommitted(message.WithoutBody(), 7, activity)
	if err != nil {
		t.Fatal(err)
	}
	if bodyless.Message().BodyRetained() || bodyless.Message().Text() != "" {
		t.Fatalf("bodyless=%+v", bodyless.Message())
	}
}

func TestLiveEventValidationAndBatchOwnership(t *testing.T) {
	now := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	message, _ := NewMessage(MessageInput{ChatID: "chat", MessageID: "message", SentAt: now, Text: "text"})
	if _, err := NewLiveMessageCommitted(Message{}, 0, now); err == nil || strings.Contains(err.Error(), "live-chat") || strings.Contains(err.Error(), "text") {
		t.Fatalf("malformed message error=%v", err)
	}
	if _, err := NewLiveMessageCommitted(message, 0, time.Time{}); err == nil {
		t.Fatal("zero activity accepted")
	}
	if _, err := NewLiveMessageCommitted(message, 0, now.Add(-time.Second)); err == nil {
		t.Fatal("activity before message accepted")
	}
	event, _ := NewLiveMessageCommitted(message, 1, now)
	source := []LiveEvent{event}
	batch, err := NewLiveEventBatch(source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = LiveEvent{}
	got, ok := batch.At(0)
	if !ok || batch.Len() != 1 || got.Message().Text() != "text" {
		t.Fatalf("batch=%+v got=%+v ok=%t", batch, got, ok)
	}
	if _, ok := batch.At(-1); ok {
		t.Fatal("negative batch index accepted")
	}
	if _, err := NewLiveEventBatch(make([]LiveEvent, MaxLiveEventsPerCommit+1)); err == nil {
		t.Fatal("oversized live batch accepted")
	}
}
