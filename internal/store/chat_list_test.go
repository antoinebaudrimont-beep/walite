package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLiteChatOrderingUsesCompleteActivityAcrossRestart(t *testing.T) {
	store, path := openTestSQLite(t)
	updated := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	activities := []struct {
		id   string
		when time.Time
	}{
		{id: "z-tie", when: time.Date(2026, 8, 30, 22, 0, 0, 0, time.UTC)},
		{id: "a-tie", when: time.Date(2026, 8, 30, 22, 0, 0, 0, time.UTC)},
		{id: "yesterday", when: time.Date(2026, 8, 31, 23, 50, 0, 0, time.UTC)},
		{id: "today", when: time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC)},
	}
	for _, value := range activities {
		chat, err := model.NewChat(model.ChatInput{ID: value.id, DisplayName: value.id, LastMessageAt: value.when, UpdatedAt: updated})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureChat(context.Background(), chat); err != nil {
			t.Fatal(err)
		}
	}
	assertOrder := func(t *testing.T, source *SQLiteStore, want ...string) {
		t.Helper()
		chats, err := source.ListChats(context.Background(), len(want))
		if err != nil || len(chats) != len(want) {
			t.Fatalf("chats=%d err=%v", len(chats), err)
		}
		for index, id := range want {
			if chats[index].ID().String() != id {
				t.Fatalf("order[%d]=%q want=%q", index, chats[index].ID().String(), id)
			}
		}
	}
	assertOrder(t, store, "today", "yesterday", "a-tie", "z-tie")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	assertOrder(t, restarted, "today", "yesterday", "a-tie", "z-tie")

	metadataOnly, _ := model.NewChat(model.ChatInput{
		ID: "yesterday", DisplayName: "renamed without activity", LastMessageAt: activities[2].when, UpdatedAt: updated.Add(time.Hour),
	})
	if err := restarted.EnsureChat(context.Background(), metadataOnly); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, restarted, "today", "yesterday", "a-tie", "z-tie")

	message, _ := model.NewMessage(model.MessageInput{
		ChatID: "yesterday", MessageID: "new-activity", SentAt: time.Date(2026, 9, 2, 6, 0, 0, 0, time.UTC), Text: "newer",
	})
	batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{message})
	if committed, err := restarted.WriteRealtime(context.Background(), batch); err != nil || committed.Len() != 1 {
		t.Fatalf("commit=%d err=%v", committed.Len(), err)
	}
	assertOrder(t, restarted, "yesterday", "today", "a-tie", "z-tie")
}

func TestSQLiteListChatsOrdersAndSurvivesRestart(t *testing.T) {
	store, path := openTestSQLite(t)
	seedChatSummaries(t, store, 100)
	chats, err := store.ListChats(context.Background(), 100)
	if err != nil || len(chats) != 100 {
		t.Fatalf("ListChats len=%d err=%v", len(chats), err)
	}
	if chats[0].ID().String() != "chat-00099" || chats[99].ID().String() != "chat-00000" ||
		chats[0].DisplayName() != "Synthetic 00099 Café" || chats[0].UnreadCount() != 99 || chats[0].IsGroup() {
		t.Fatalf("ordered summaries first=%+v last=%+v", chats[0], chats[99])
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(context.Background(), SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted, err := reopened.ListChats(context.Background(), 100)
	if err != nil || len(restarted) != len(chats) || restarted[0].ID() != chats[0].ID() || restarted[0].DisplayName() != chats[0].DisplayName() {
		t.Fatalf("restart summaries len=%d err=%v", len(restarted), err)
	}
}

func TestSQLiteListChatsTenThousandBoundAndStableTieBreak(t *testing.T) {
	store, _ := openTestSQLite(t)
	seedChatSummaries(t, store, model.MaxChatSummaries+1)
	chats, err := store.ListChats(context.Background(), model.MaxChatSummaries+1)
	if err != nil || len(chats) != model.MaxChatSummaries {
		t.Fatalf("ListChats len=%d err=%v", len(chats), err)
	}
	if chats[0].ID().String() != "chat-10000" || chats[len(chats)-1].ID().String() != "chat-00001" {
		t.Fatalf("bounded endpoints=%q..%q", chats[0].ID().String(), chats[len(chats)-1].ID().String())
	}
	// Equal activity uses the opaque stable ID as the deterministic tie-break.
	if _, err := store.db.Exec(`UPDATE chats SET last_message_at = 20000 WHERE chat_id IN ('chat-00010','chat-00011')`); err != nil {
		t.Fatal(err)
	}
	tied, err := store.ListChats(context.Background(), model.MaxChatSummaries)
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int{}
	for index, chat := range tied {
		positions[chat.ID().String()] = index
	}
	if positions["chat-00010"] >= positions["chat-00011"] {
		t.Fatalf("tie positions=%v", positions)
	}
}

func seedChatSummaries(t testing.TB, store *SQLiteStore, count int) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO chats(chat_id, display_name, is_group, last_message_at, unread_count, muted, archived, placeholder, updated_at, ingest_seq) VALUES (?, ?, 0, ?, ?, 0, 0, 0, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < count; index++ {
		if _, err := statement.Exec(fmt.Sprintf("chat-%05d", index), fmt.Sprintf("Synthetic %05d Café", index), index+1, index%1000, index+1, index+1); err != nil {
			t.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkSQLiteListChats(b *testing.B) {
	for _, count := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("Summaries_%d", count), func(b *testing.B) {
			store, _ := openBenchmarkSQLite(b)
			seedChatSummaries(b, store, count)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				chats, err := store.ListChats(context.Background(), count)
				if err != nil || len(chats) != count {
					b.Fatalf("ListChats len=%d err=%v", len(chats), err)
				}
			}
		})
	}
}
