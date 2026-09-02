package model

import (
	"testing"
	"time"
)

func TestReadReceiptRequestOwnsBoundedStableIdentity(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	inputs := []ReadReceiptMessageInput{{MessageID: "first", SentAt: base, SenderID: "sender@lid"}, {MessageID: "second", SentAt: base.Add(time.Minute), SenderID: "sender@lid"}}
	request, err := NewReadReceiptRequest("group@g.us", true, inputs)
	if err != nil {
		t.Fatal(err)
	}
	inputs[0].MessageID = "changed"
	first, _ := request.At(0)
	if request.ChatID().String() != "group@g.us" || !request.IsGroup() || request.Len() != 2 || first.MessageID().String() != "first" || first.SenderID().String() != "sender@lid" || request.NewestSentAt() != base.Add(time.Minute) {
		t.Fatalf("request=%+v first=%+v", request, first)
	}
}

func TestReadReceiptRequestRejectsInvalidUnboundedAndGroupWithoutSender(t *testing.T) {
	base := time.Unix(1, 0).UTC()
	tooMany := make([]ReadReceiptMessageInput, MaxReadReceiptMessages+1)
	for index := range tooMany {
		tooMany[index] = ReadReceiptMessageInput{MessageID: string(rune('a' + index)), SentAt: base}
	}
	for _, inputs := range [][]ReadReceiptMessageInput{nil, tooMany, {{MessageID: "", SentAt: base}}, {{MessageID: "same", SentAt: base}, {MessageID: "same", SentAt: base}}} {
		if _, err := NewReadReceiptRequest("chat@lid", false, inputs); err == nil {
			t.Fatalf("accepted invalid inputs len=%d", len(inputs))
		}
	}
	if _, err := NewReadReceiptRequest("group@g.us", true, []ReadReceiptMessageInput{{MessageID: "id", SentAt: base}}); err == nil {
		t.Fatal("accepted group receipt without participant")
	}
}
