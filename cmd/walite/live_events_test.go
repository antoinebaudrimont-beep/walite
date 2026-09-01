package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

type liveApplicationService struct {
	updates   chan model.Update
	live      chan model.LiveEvent
	emit      chan model.LiveEvent
	done      chan struct{}
	chats     []model.Chat
	messages  map[string][]model.Message
	liveCalls atomic.Int32
}

type finiteLiveApplicationService struct {
	updates chan model.Update
	live    chan model.LiveEvent
	chat    model.Chat
	events  []model.LiveEvent
}

func (application *finiteLiveApplicationService) Run(context.Context) error {
	ready, _ := model.NewUpdate(model.UpdateInput{Kind: model.UpdateReady})
	application.updates <- ready
	for _, event := range application.events {
		application.live <- event
	}
	close(application.live)
	close(application.updates)
	return nil
}
func (application *finiteLiveApplicationService) Updates() <-chan model.Update {
	return application.updates
}
func (application *finiteLiveApplicationService) LiveEvents() <-chan model.LiveEvent {
	return application.live
}
func (application *finiteLiveApplicationService) InitialChats(context.Context, int) ([]model.Chat, error) {
	return []model.Chat{application.chat}, nil
}
func (*finiteLiveApplicationService) InitialMessages(context.Context, model.ChatID, int) ([]model.Message, error) {
	return nil, nil
}

func newLiveApplicationService(chats []model.Chat, messages map[string][]model.Message) *liveApplicationService {
	return &liveApplicationService{
		updates: make(chan model.Update, 1), live: make(chan model.LiveEvent), emit: make(chan model.LiveEvent), done: make(chan struct{}),
		chats: chats, messages: messages,
	}
}

func (application *liveApplicationService) Run(ctx context.Context) error {
	defer close(application.done)
	defer close(application.live)
	defer close(application.updates)
	ready, _ := model.NewUpdate(model.UpdateInput{Kind: model.UpdateReady})
	application.updates <- ready
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-application.emit:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case application.live <- event:
			}
		}
	}
}

func (application *liveApplicationService) Updates() <-chan model.Update { return application.updates }
func (application *liveApplicationService) LiveEvents() <-chan model.LiveEvent {
	application.liveCalls.Add(1)
	return application.live
}
func (application *liveApplicationService) InitialChats(context.Context, int) ([]model.Chat, error) {
	return append([]model.Chat(nil), application.chats...), nil
}
func (application *liveApplicationService) InitialMessages(_ context.Context, chatID model.ChatID, _ int) ([]model.Message, error) {
	return append([]model.Message(nil), application.messages[chatID.String()]...), nil
}

func TestLiveEventAdapterPreservesFidelityAndBodylessState(t *testing.T) {
	now := time.Date(2100, 8, 9, 10, 11, 12, 0, time.UTC)
	message := mustSnapshotMessage(t, "adapter-chat", "adapter-message", now, true, "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦")
	event, err := model.NewLiveMessageCommitted(message, 7, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	presentation, ok := adaptLiveMessage(event)
	if !ok || presentation.ChatID != "adapter-chat" || presentation.MessageID != "adapter-message" ||
		presentation.Text != message.Text() || !presentation.FromMe || !presentation.BodyRetained || presentation.UnreadCount != 7 ||
		!presentation.SentAt.Equal(now) || !presentation.ActivityTime.Equal(now.Add(time.Minute)) {
		t.Fatalf("presentation=%+v open=%t", presentation, ok)
	}
	bodylessEvent, _ := model.NewLiveMessageCommitted(message.WithoutBody(), 7, now.Add(time.Minute))
	bodyless, ok := adaptLiveMessage(bodylessEvent)
	if !ok || bodyless.BodyRetained || bodyless.Text != "" {
		t.Fatalf("bodyless=%+v open=%t", bodyless, ok)
	}
	if livePresentationCapacity != 64 || service.LiveEventCapacity != 64 {
		t.Fatalf("adapter capacity=%d service=%d", livePresentationCapacity, service.LiveEventCapacity)
	}
}

func TestForwardLiveMessagesPreservesOrderAndCancelsUnderBackpressure(t *testing.T) {
	now := time.Date(2100, 8, 9, 10, 11, 12, 0, time.UTC)
	source := make(chan model.LiveEvent, 2)
	for index := 1; index <= 2; index++ {
		message := mustSnapshotMessage(t, "forward-chat", "message-"+string(rune('0'+index)), now.Add(time.Duration(index)*time.Minute), false, "forward")
		event, _ := model.NewLiveMessageCommitted(message, uint32(index), message.SentAt())
		source <- event
	}
	close(source)
	destination := make(chan tui.LiveMessage, 2)
	forwardLiveMessages(context.Background(), source, destination)
	first, second := <-destination, <-destination
	if first.MessageID != "message-1" || second.MessageID != "message-2" {
		t.Fatalf("forward order=%q,%q", first.MessageID, second.MessageID)
	}
	if _, ok := <-destination; ok {
		t.Fatal("adapter destination remained open")
	}

	blockedSource := make(chan model.LiveEvent)
	message := mustSnapshotMessage(t, "blocked-chat", "blocked-1", now.Add(time.Minute), false, "blocked")
	blockedEvent, _ := model.NewLiveMessageCommitted(message, 1, message.SentAt())
	blockedDestination := make(chan tui.LiveMessage)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { forwardLiveMessages(ctx, blockedSource, blockedDestination); close(done) }()
	accepted := make(chan struct{})
	go func() { blockedSource <- blockedEvent; close(accepted) }()
	<-accepted
	cancel()
	<-done
}

func TestApplicationForwardsCommittedLiveEventIntoExistingChat(t *testing.T) {
	isolateApplicationFiles(t)
	base := time.Date(2100, 8, 9, 10, 0, 0, 0, time.UTC)
	chatA := mustSnapshotChat(t, "a", "Alpha", false, 0, base.Add(30*time.Minute))
	chatB := mustSnapshotChat(t, "b", "Bravo", false, 0, base.Add(20*time.Minute))
	chatC := mustSnapshotChat(t, "c", "Charlie", false, 0, base.Add(10*time.Minute))
	initialA := mustSnapshotMessage(t, "a", "a-initial", base.Add(25*time.Minute), false, "Alpha initial")
	application := newLiveApplicationService([]model.Chat{chatA, chatB, chatC}, map[string][]model.Message{"a": {initialA}})
	screen := newStartupObservedScreen()
	done := make(chan error, 1)
	go func() {
		done <- runStartedApplication(context.Background(), screen, tui.DefaultOptions(), application, tui.Run)
	}()
	<-screen.shown
	<-screen.shown // asynchronous selected-chat cache page

	liveMessage := mustSnapshotMessage(t, "c", "c-live", base.Add(35*time.Minute), false, "Charlie committed live")
	committed, err := model.NewLiveMessageCommitted(liveMessage, 4, base.Add(40*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	application.emit <- committed
	<-screen.shown
	text := startupScreenText(screen)
	lines := strings.Split(text, "\n")
	if len(lines) < 5 || !strings.Contains(lines[3], "Charlie (4)") || !strings.Contains(lines[4], "> Alpha") ||
		!strings.Contains(text, "Alpha initial") || strings.Contains(text, "Charlie committed live") {
		t.Fatalf("background live frame did not preserve Alpha selection/promote Charlie:\n%s", text)
	}

	screen.InjectKey(tcell.KeyRune, 'k', tcell.ModNone)
	<-screen.shown
	if text := startupScreenText(screen); !strings.Contains(text, "Charlie committed live") {
		t.Fatalf("promoted chat did not contain committed message:\n%s", text)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls := application.liveCalls.Load(); calls != 1 {
		t.Fatalf("LiveEvents consumers=%d want=1", calls)
	}
	select {
	case <-application.done:
	default:
		t.Fatal("application service was not joined")
	}
}

func TestApplicationForwardsCommittedLiveEventIntoFifthChat(t *testing.T) {
	isolateApplicationFiles(t)
	base := time.Date(2100, 8, 9, 10, 0, 0, 0, time.UTC)
	chats := []model.Chat{
		mustSnapshotChat(t, "a", "Alpha", false, 0, base.Add(40*time.Minute)),
		mustSnapshotChat(t, "b", "Bravo", false, 0, base.Add(30*time.Minute)),
		mustSnapshotChat(t, "c", "Charlie", false, 0, base.Add(20*time.Minute)),
		mustSnapshotChat(t, "d", "Delta", false, 0, base.Add(10*time.Minute)),
	}
	initial := mustSnapshotMessage(t, "a", "a-initial", base.Add(35*time.Minute), false, "Alpha remains selected")
	application := newLiveApplicationService(chats, map[string][]model.Message{"a": {initial}})
	screen := newStartupObservedScreen()
	done := make(chan error, 1)
	go func() {
		done <- runStartedApplication(context.Background(), screen, tui.DefaultOptions(), application, tui.Run)
	}()
	<-screen.shown
	<-screen.shown // asynchronous selected-chat cache page

	message := mustSnapshotMessage(t, "new-chat", "new-chat-message", base.Add(50*time.Minute), true, "Café 東京 ❤️")
	committed, err := model.NewLiveMessageCommitted(message, 5, base.Add(60*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	application.emit <- committed
	<-screen.shown
	if rendered := startupScreenText(screen); !strings.Contains(rendered, "new-chat (5)") ||
		!strings.Contains(rendered, "> Alpha") || !strings.Contains(rendered, "Alpha remains selected") {
		t.Fatalf("new-chat frame did not insert and preserve selection:\n%s", rendered)
	}

	screen.InjectKey(tcell.KeyRune, 'k', tcell.ModNone)
	<-screen.shown
	if rendered := startupScreenText(screen); !strings.Contains(rendered, "Café") ||
		!strings.Contains(rendered, "東") || !strings.Contains(rendered, "❤") {
		t.Fatalf("new chat did not display its committed message:\n%s", rendered)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestApplicationDrainsCommittedEventsBeforeServiceLedShutdown(t *testing.T) {
	base := time.Date(2100, 8, 9, 10, 0, 0, 0, time.UTC)
	chat := mustSnapshotChat(t, "drain-chat", "Drain Chat", false, 0, base)
	events := make([]model.LiveEvent, 3)
	for index := range events {
		message := mustSnapshotMessage(t, "drain-chat", "drain-"+string(rune('1'+index)), base.Add(time.Duration(index+1)*time.Minute), false, "drained")
		events[index], _ = model.NewLiveMessageCommitted(message, uint32(index+1), message.SentAt())
	}
	application := &finiteLiveApplicationService{updates: make(chan model.Update, 1), live: make(chan model.LiveEvent), chat: chat, events: events}
	received := make(chan []string, 1)
	runTUI := func(ctx context.Context, _ tcell.Screen, input tui.Input) error {
		var ids []string
		for event := range input.LiveEvents {
			ids = append(ids, event.MessageID)
		}
		received <- ids
		<-ctx.Done()
		return ctx.Err()
	}
	err := runStartedApplication(context.Background(), newStartupObservedScreen(), tui.DefaultOptions(), application, runTUI)
	if !errors.Is(err, errServiceStopped) {
		t.Fatalf("runStartedApplication=%v", err)
	}
	if got := <-received; strings.Join(got, ",") != "drain-1,drain-2,drain-3" {
		t.Fatalf("drained order=%v", got)
	}
}
