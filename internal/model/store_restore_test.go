package model

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMessageSummaryMetadataConstructor(t *testing.T) {
	message, _ := NewMessage(MessageInput{ChatID: "chat", MessageID: "one", SentAt: time.Unix(1, 0).UTC(), Text: "Café 👋"})
	for _, message := range []Message{message, message.WithoutBody()} {
		want := NewMessageSummary(message)
		input := MessageSummaryInput{ChatID: message.ChatID(), MessageID: message.MessageID(), SentAt: message.SentAt(), FromMe: message.FromMe(), BodyRetained: message.BodyRetained(), BodyBytes: len(message.Text())}
		got, err := NewMessageSummaryFromMetadata(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("metadata summary: %+v %v", got, err)
		}
		for _, bytes := range []int{-1, MaxRetainedTextBytes + 1} {
			input.BodyBytes = bytes
			if _, err := NewMessageSummaryFromMetadata(input); err == nil {
				t.Fatal("invalid byte count accepted")
			}
		}
		input.BodyRetained, input.BodyBytes = false, 1
		if _, err := NewMessageSummaryFromMetadata(input); err == nil {
			t.Fatal("bodyless byte charge accepted")
		}
	}
	if _, err := NewMessageSummaryFromMetadata(MessageSummaryInput{}); err == nil {
		t.Fatal("zero summary accepted")
	}
}

func TestRestoreBodyTruncationMarker(t *testing.T) {
	input := MessageInput{ChatID: "chat", MessageID: "one", SentAt: time.Unix(1, 0).UTC(), Text: strings.Repeat("é", MaxRetainedTextBytes)}
	message, err := NewMessage(input)
	if err != nil || !message.BodyTruncated() {
		t.Fatal("normal truncation missing")
	}
	input.Text, input.BodyTruncated = message.Text(), message.BodyTruncated()
	got, err := NewMessage(input)
	if err != nil || !reflect.DeepEqual(got, message) {
		t.Fatal("restored text lost truncation marker")
	}
}
