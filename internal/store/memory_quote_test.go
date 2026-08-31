package store

import (
	"context"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestMemoryCommittedReplySurvivesQuoteLessDuplicate(t *testing.T) {
	for _, sameBatch := range []bool{false, true} {
		ctx := context.Background()
		memory, _ := NewMemory(1)
		now := time.Date(2100, 8, 1, 12, 0, 0, 0, time.UTC)
		quote, _ := model.NewTextQuote("original", "synthetic Café 日本語 👋", true)
		echo := mustLiveMessage(t, "chat", "reply", now, true, "reply 👋")
		reply := echo.WithQuote(quote)
		messages := []model.Message{reply}
		if sameBatch {
			messages = append(messages, echo)
		}
		batch, _ := model.NewWriteBatch(model.WriteRealtime, messages)
		committed, err := memory.WriteRealtime(ctx, batch)
		if err != nil || committed.Len() != 1 {
			t.Fatalf("commit: %d %v", committed.Len(), err)
		}
		live, _ := committed.At(0)
		if live.Message().Quote() != quote || live.UnreadCount() != 0 || !live.ActivityTime().Equal(now) {
			t.Fatal("live quote/metadata lost")
		}
		for _, duplicate := range []model.Message{echo, echo.WithoutBody(), reply} {
			batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{duplicate})
			events, err := memory.WriteRealtime(ctx, batch)
			if err != nil || events.Len() != 0 {
				t.Fatalf("duplicate emitted: %d %v", events.Len(), err)
			}
			page, _, err := memory.Page(ctx, reply.ChatID(), model.NoCursor(), 10)
			if err != nil || len(page) != 1 || page[0] != reply {
				t.Fatal("duplicate downgraded reply or added message")
			}
		}
	}
}
