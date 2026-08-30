package wa

import (
	"context"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestOfflineTextSenderOwnsDeterministicIdentityTimeAndUnicode(t *testing.T) {
	base := time.Date(2200, 3, 4, 5, 6, 7, 0, time.UTC)
	next := 0
	sender, err := NewOfflineTextSender(func() time.Time {
		next++
		return base.Add(time.Duration(next) * time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	chatID, _ := model.NewChatID("offline-chat")
	const text = "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦"
	for index, wantID := range []string{"offline-outgoing-000001", "offline-outgoing-000002"} {
		event, err := sender.SendText(context.Background(), chatID, text)
		if err != nil {
			t.Fatal(err)
		}
		message := event.Message()
		wantTime := base.Add(time.Duration(index+1) * time.Second)
		if message.ChatID() != chatID || message.MessageID().String() != wantID || !message.SentAt().Equal(wantTime) ||
			!event.ReceivedAt().Equal(wantTime) || !message.FromMe() || message.Text() != text || !message.BodyRetained() {
			t.Fatalf("event %d=%+v message=%+v", index, event, message)
		}
	}
}

func TestOfflineTextSenderHonorsCancellationWithoutConsumingID(t *testing.T) {
	base := time.Date(2200, 3, 4, 5, 6, 7, 0, time.UTC)
	sender, _ := NewOfflineTextSender(func() time.Time { return base })
	chatID, _ := model.NewChatID("offline-chat")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sender.SendText(ctx, chatID, "cancelled"); err == nil {
		t.Fatal("cancelled send succeeded")
	}
	event, err := sender.SendText(context.Background(), chatID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if got := event.Message().MessageID().String(); got != "offline-outgoing-000001" {
		t.Fatalf("ID after cancellation=%q", got)
	}
}
