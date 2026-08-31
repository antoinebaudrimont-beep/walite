package model

import (
	"strings"
	"testing"
	"time"
)

func TestMessageWithChatIDPreservesFieldsAndRecalculatesBoundedCharge(t *testing.T) {
	message, err := NewMessage(MessageInput{ChatID: "original", MessageID: "stable", SentAt: time.Now().UTC(), FromMe: true, Text: strings.Repeat("x", MaxRetainedTextBytes+1)})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewChatID("different-opaque-identity")
	for _, original := range []Message{message, message.WithoutBody()} {
		changed, err := original.WithChatID(other)
		if err != nil {
			t.Fatal(err)
		}
		back, err := changed.WithChatID(original.ChatID())
		if err != nil || back != original {
			t.Fatal("readdressing changed message fields")
		}
		if changed.ChatID() != other || changed.ByteSize() != original.ByteSize()+len(other.String())-len(original.ChatID().String()) {
			t.Fatal("wrong identity/byte charge")
		}
		if _, err := NewEvent(changed, original.SentAt()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := message.WithChatID(ChatID{}); err == nil {
		t.Fatal("empty identity accepted")
	}
	if _, err := (Message{}).WithChatID(other); err == nil {
		t.Fatal("invalid message accepted")
	}
}
