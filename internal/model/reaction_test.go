package model

import (
	"testing"
	"time"
)

func TestReactionModelAcceptsComplexEmojiAndRemoval(t *testing.T) {
	for _, emoji := range []string{"❤️", "👍🏽", "👨‍👩‍👧‍👦", ""} {
		reaction, err := NewReaction(ReactionInput{ChatID: "chat", TargetMessageID: "target", ReactorID: "person", Emoji: emoji, UpdatedAt: time.Unix(1, 0)})
		if err != nil || reaction.Emoji() != emoji || reaction.Removed() != (emoji == "") {
			t.Fatalf("emoji=%q reaction=%+v err=%v", emoji, reaction, err)
		}
	}
	if _, err := NewReaction(ReactionInput{ChatID: "chat", TargetMessageID: "target", ReactorID: "person", Emoji: "👍❤️", UpdatedAt: time.Unix(1, 0)}); err == nil {
		t.Fatal("multiple graphemes accepted")
	}
}

func TestReactionSummaryOwnsBoundedGroups(t *testing.T) {
	group, _ := NewReactionGroup("👍🏽", 2, true)
	summary, err := NewReactionSummary("chat", "target", []ReactionGroup{group})
	if err != nil || summary.Len() != 1 {
		t.Fatal(err)
	}
	got, ok := summary.At(0)
	if !ok || got.Emoji() != "👍🏽" || got.Count() != 2 || !got.Own() {
		t.Fatalf("group=%+v ok=%t", got, ok)
	}
}
