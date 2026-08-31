package model

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDisplayMetadataBoundsPriorityAndIdentity(t *testing.T) {
	saved, err := NewDisplayMetadata("opaque", "Café é 家族 👨‍👩‍👧‍👦", DisplaySaved, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, quality := range []DisplayQuality{DisplayOpaque, DisplayPhone, DisplayPush, DisplayBusiness} {
		next, _ := NewDisplayMetadata("opaque", "lower", quality, false)
		if saved.Merge(next) != saved {
			t.Fatal("lower quality replaced saved name")
		}
	}
	empty, _ := NewDisplayMetadata("opaque", "", DisplaySaved, false)
	if saved.Merge(empty) != saved {
		t.Fatal("empty erased name")
	}
	rename, _ := NewDisplayMetadata("opaque", "Renamed 👋", DisplaySaved, false)
	if saved.Merge(rename) != rename {
		t.Fatal("equal-quality rename lost")
	}
	other, _ := NewDisplayMetadata("other", saved.Name(), DisplaySaved, false)
	if saved.Merge(other) != saved {
		t.Fatal("same name merged identities")
	}
	bounded, _ := NewDisplayMetadata("opaque", strings.Repeat("x", MaxChatDisplayNameBytes-1)+"👋", DisplaySaved, false)
	if len(bounded.Name()) > MaxChatDisplayNameBytes || !utf8.ValidString(bounded.Name()) {
		t.Fatal("name bound/UTF-8")
	}
	singleLine, _ := NewDisplayMetadata("opaque", "Name\n\x1b title", DisplaySaved, false)
	if strings.ContainsAny(singleLine.Name(), "\n\x1b") {
		t.Fatal("control characters retained")
	}
	for _, input := range []struct {
		id string
		q  DisplayQuality
	}{{"", DisplaySaved}, {strings.Repeat("i", MaxIdentifierBytes+1), DisplaySaved}, {"id", 99}} {
		if _, err := NewDisplayMetadata(input.id, "name", input.q, false); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
}

func TestGroupSenderSurvivesCommittedCopiesAndPruning(t *testing.T) {
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	message, err := NewMessage(MessageInput{ChatID: "group", MessageID: "message", SentAt: now, SenderID: "sender", IsGroup: true, Text: "body 👋"})
	if err != nil {
		t.Fatal(err)
	}
	event, err := NewEvent(message, now)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewWriteBatch(WriteRealtime, []Message{event.Message()})
	if err != nil {
		t.Fatal(err)
	}
	copy, _ := batch.At(0)
	live, err := NewLiveMessageCommitted(copy, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if live.Message() != message || message.SenderID().String() != "sender" || !message.IsGroup() {
		t.Fatal("sender identity lost")
	}
	without := message.WithoutBody()
	if without.SenderID() != message.SenderID() || !without.IsGroup() || without.ByteSize() != len("groupmessagesender") {
		t.Fatal("pruning lost/undercharged identity")
	}
	id, _ := NewChatID("other")
	aliased, err := message.WithChatID(id)
	if err != nil || aliased.SenderID() != message.SenderID() || !aliased.IsGroup() {
		t.Fatal("chat copy changed sender")
	}
	if _, err := NewMessage(MessageInput{ChatID: "group", MessageID: "m", SentAt: now, SenderID: strings.Repeat("x", MaxIdentifierBytes+1)}); err == nil {
		t.Fatal("unbounded sender")
	}
}
