package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func reactionFixture(t *testing.T, reactor, emoji string, at time.Time) model.Reaction {
	t.Helper()
	reaction, err := model.NewReaction(model.ReactionInput{
		ChatID: "reaction-chat", TargetMessageID: "target", ReactorID: reactor, Emoji: emoji, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return reaction
}

func TestSQLiteReactionLatestValueRemovalIdempotencyAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	cache, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	steps := []model.Reaction{
		reactionFixture(t, "alice", "👍", base),
		reactionFixture(t, "alice", "👍", base),
		reactionFixture(t, model.SelfReactorID, "👍", base.Add(time.Second)),
		reactionFixture(t, "alice", "❤️", base.Add(2*time.Second)),
		reactionFixture(t, "alice", "👍", base.Add(-time.Second)),
		reactionFixture(t, "alice", "", base.Add(3*time.Second)),
	}
	var summary model.ReactionSummary
	for _, reaction := range steps {
		summary, err = cache.ApplyReaction(context.Background(), reaction)
		if err != nil {
			t.Fatal(err)
		}
	}
	if summary.Len() != 1 {
		t.Fatalf("groups=%d", summary.Len())
	}
	group, _ := summary.At(0)
	if group.Emoji() != "👍" || group.Count() != 1 || !group.Own() {
		t.Fatalf("group=%+v", group)
	}
	if got := queryCount(t, cache.db, "SELECT COUNT(*) FROM reactions WHERE chat_id='reaction-chat' AND target_message_id='target'"); got != 2 {
		t.Fatalf("latest rows=%d", got)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	cache, err = OpenSQLite(context.Background(), SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	restored, err := cache.ReactionSummary(context.Background(), reactionFixture(t, "alice", "", base).ChatID(), reactionFixture(t, "alice", "", base).TargetMessageID())
	if err != nil || restored.Len() != 1 {
		t.Fatalf("restored groups=%d err=%v", restored.Len(), err)
	}
}

func TestSQLiteReactionPerMessageBound(t *testing.T) {
	cache, _ := openTestSQLite(t)
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for index := 0; index < model.MaxReactionsPerMessage+5; index++ {
		if _, err := cache.ApplyReaction(context.Background(), reactionFixture(t, fmt.Sprintf("reactor-%03d", index), "👍", base.Add(time.Duration(index)*time.Millisecond))); err != nil {
			t.Fatal(err)
		}
	}
	if got := queryCount(t, cache.db, "SELECT COUNT(*) FROM reactions WHERE chat_id='reaction-chat' AND target_message_id='target'"); got != model.MaxReactionsPerMessage {
		t.Fatalf("rows=%d bound=%d", got, model.MaxReactionsPerMessage)
	}
	summary, err := cache.ReactionSummary(context.Background(), reactionFixture(t, "alice", "", base).ChatID(), reactionFixture(t, "alice", "", base).TargetMessageID())
	if err != nil || summary.Len() != 1 {
		t.Fatalf("summary=%d err=%v", summary.Len(), err)
	}
	group, _ := summary.At(0)
	if group.Count() != model.MaxReactionsPerMessage {
		t.Fatalf("count=%d", group.Count())
	}
}

func TestSQLiteReactionUnknownTargetFrontierIsBounded(t *testing.T) {
	cache, _ := openTestSQLite(t)
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for index := 0; index < maxOrphanReactionsPerChat+5; index++ {
		reaction, err := model.NewReaction(model.ReactionInput{
			ChatID: "reaction-chat", TargetMessageID: fmt.Sprintf("target-%03d", index),
			ReactorID: "alice", Emoji: "👍", UpdatedAt: base.Add(time.Duration(index) * time.Millisecond),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cache.ApplyReaction(context.Background(), reaction); err != nil {
			t.Fatal(err)
		}
	}
	if got := queryCount(t, cache.db, "SELECT COUNT(*) FROM reactions WHERE chat_id='reaction-chat'"); got != maxOrphanReactionsPerChat {
		t.Fatalf("orphan rows=%d bound=%d", got, maxOrphanReactionsPerChat)
	}
	if got := queryCount(t, cache.db, "SELECT COUNT(*) FROM reactions WHERE target_message_id='target-000'"); got != 0 {
		t.Fatalf("oldest orphan retained=%d", got)
	}
}
