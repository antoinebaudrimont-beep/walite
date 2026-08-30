package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

type capturingSendApplication struct {
	request service.SendTextRequest
	calls   int
	failure error
}

func (*capturingSendApplication) Run(context.Context) error          { return nil }
func (*capturingSendApplication) Updates() <-chan model.Update       { return nil }
func (*capturingSendApplication) LiveEvents() <-chan model.LiveEvent { return nil }
func (*capturingSendApplication) InitialChats(context.Context, int) ([]model.Chat, error) {
	return nil, nil
}
func (*capturingSendApplication) InitialMessages(context.Context, model.ChatID, int) ([]model.Message, error) {
	return nil, nil
}
func (application *capturingSendApplication) SendText(_ context.Context, request service.SendTextRequest) error {
	application.calls++
	application.request = request
	return application.failure
}

func TestSendTextFromTUIAdaptsPlainTextAndDefersReply(t *testing.T) {
	application := &capturingSendApplication{}
	request := tui.SendRequest{ChatID: "stable-chat", Text: "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦"}
	if err := sendTextFromTUI(context.Background(), application, request); err != nil {
		t.Fatal(err)
	}
	if application.calls != 1 || application.request.ChatID().String() != request.ChatID || application.request.Text() != request.Text {
		t.Fatalf("calls=%d request chat=%q text=%q", application.calls, application.request.ChatID(), application.request.Text())
	}
	request.ReplyToID = "stable-reply-id"
	if err := sendTextFromTUI(context.Background(), application, request); !errors.Is(err, errReplySendUnsupported) {
		t.Fatalf("reply=%v", err)
	}
	if application.calls != 1 {
		t.Fatalf("reply reached service calls=%d", application.calls)
	}
}

func TestOfflineApplicationOutgoingPathCommitsBeforeLivePresentation(t *testing.T) {
	isolateApplicationFiles(t)
	applicationValue, err := newOfflineApplicationService()
	if err != nil {
		t.Fatal(err)
	}
	application := applicationValue.(*offlineApplicationService)
	var presented tui.LiveMessage
	runTUI := func(ctx context.Context, _ tcell.Screen, input tui.Input) error {
		if input.Send == nil || len(input.InitialState.Chats) != 4 {
			return errors.New("outgoing TUI wiring missing")
		}
		const text = "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦"
		if err := input.Send(ctx, tui.SendRequest{ChatID: "snapshot-contact", Text: text}); err != nil {
			return err
		}
		var ok bool
		presented, ok = <-input.LiveEvents
		if !ok {
			return errors.New("committed presentation stream closed")
		}
		return nil
	}
	if err := runStartedApplication(context.Background(), newStartupObservedScreen(), tui.DefaultOptions(), application, runTUI); err != nil {
		t.Fatal(err)
	}
	wantAt := demoStart.Add(2 * time.Hour)
	if presented.ChatID != "snapshot-contact" || presented.MessageID != "offline-outgoing-000001" ||
		!presented.SentAt.Equal(wantAt) || !presented.ActivityTime.Equal(wantAt) || !presented.FromMe ||
		presented.Text != "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦" || !presented.BodyRetained || presented.UnreadCount != 12 {
		t.Fatalf("presented=%+v", presented)
	}
	chatID, _ := model.NewChatID("snapshot-contact")
	messages, _, err := application.store.Page(context.Background(), chatID, model.NoCursor(), 1)
	if err != nil || len(messages) != 1 {
		t.Fatalf("stored messages=%d err=%v", len(messages), err)
	}
	stored := messages[0]
	if stored.MessageID().String() != presented.MessageID || stored.Text() != presented.Text || !stored.FromMe() || !stored.SentAt().Equal(wantAt) {
		t.Fatalf("stored=%+v presented=%+v", stored, presented)
	}
}
