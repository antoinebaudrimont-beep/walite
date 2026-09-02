package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
	"github.com/gdamore/tcell/v2"
)

type capturingSendApplication struct {
	request service.SendTextRequest
	calls   int
	failure error
}

func TestUnavailableApplicationSendKeepsTUIDraft(t *testing.T) {
	source, err := wa.NewFakeSource(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newConnectedApplicationService(source, nil, nil, nil); err == nil {
		t.Fatal("connected application accepted missing real sender")
	}

	base := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	receiveOnly := &receiveOnlyChatApplication{
		authenticationReadyService: newAuthenticationReadyService(),
		chat:                       mustSnapshotChat(t, "real-chat@s.whatsapp.net", "Real chat", false, 0, base),
		message:                    mustSnapshotMessage(t, "real-chat@s.whatsapp.net", "incoming-1", base, false, "incoming text"),
	}
	screen := newStartupObservedScreen()
	done := make(chan error, 1)
	go func() {
		done <- runStartedApplication(context.Background(), screen, tui.DefaultOptions(), receiveOnly, tui.Run)
	}()
	<-screen.shown
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	for _, character := range "draft" {
		screen.InjectKey(tcell.KeyRune, character, tcell.ModNone)
		<-screen.shown
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown // controlled rejection status
	screen.InjectKey(tcell.KeyLeft, 0, tcell.ModNone)
	<-screen.shown
	if rendered := startupScreenText(screen); !strings.Contains(rendered, "draft") || !strings.Contains(rendered, "incoming text") {
		t.Fatalf("rejected connected send changed presentation:\n%s", rendered)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type receiveOnlyChatApplication struct {
	*authenticationReadyService
	chat    model.Chat
	message model.Message
}

func (application *receiveOnlyChatApplication) InitialChats(context.Context, int) ([]model.Chat, error) {
	return []model.Chat{application.chat}, nil
}

func (application *receiveOnlyChatApplication) InitialMessages(context.Context, model.ChatID, int) ([]model.Message, error) {
	return []model.Message{application.message}, nil
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

func TestSendTextFromTUIAdaptsPlainTextAndReply(t *testing.T) {
	application := &capturingSendApplication{}
	request := tui.SendRequest{ChatID: "stable-chat", Text: "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦"}
	if err := sendTextFromTUI(context.Background(), application, request); err != nil {
		t.Fatal(err)
	}
	if application.calls != 1 || application.request.ChatID().String() != request.ChatID || application.request.Text() != request.Text {
		t.Fatalf("calls=%d request chat=%q text=%q", application.calls, application.request.ChatID(), application.request.Text())
	}
	request.ReplyToID = "stable-reply-id"
	request.ReplyToText = "quoted é 日本語 🐧"
	request.ReplyToFromMe = true
	if err := sendTextFromTUI(context.Background(), application, request); err != nil {
		t.Fatalf("reply=%v", err)
	}
	if application.calls != 2 || application.request.Reply().MessageID().String() != request.ReplyToID || application.request.Reply().Text() != request.ReplyToText || !application.request.Reply().FromMe() {
		t.Fatalf("reply metadata lost: calls=%d request=%+v", application.calls, application.request)
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
