package tui

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

func notificationTestView(t *testing.T, group bool) viewModel {
	t.Helper()
	at := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "chat@lid", Title: map[bool]string{false: "Alice", true: "Family"}[group], IsGroup: group, ActivityTime: at,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 24}
}

func notificationEvent(id string, group bool) LiveMessage {
	at := time.Date(2100, 1, 2, 3, 5, 0, 0, time.UTC)
	return LiveMessage{
		ChatID: "chat@lid", MessageID: id, SentAt: at, ActivityTime: at,
		Text: "Are you coming tonight?", BodyRetained: true, UnreadCount: 1,
		SenderID: "alice@lid", IsGroup: group, NotificationEligible: true,
	}
}

func TestNotificationEligibilityIsOnlyNewReadyIncomingInsertion(t *testing.T) {
	for _, test := range []struct {
		name       string
		configure  func(*viewModel, *LiveMessage)
		wantNotify bool
	}{
		{name: "new live incoming", wantNotify: true},
		{name: "pre-ready reconnect or history", configure: func(_ *viewModel, event *LiveMessage) { event.NotificationEligible = false }},
		{name: "own outgoing", configure: func(_ *viewModel, event *LiveMessage) { event.FromMe = true }},
		{name: "disabled", configure: func(model *viewModel, _ *LiveMessage) { model.options.DesktopNotifications = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := notificationTestView(t, false)
			event := notificationEvent(test.name, false)
			if test.configure != nil {
				test.configure(&model, &event)
			}
			mutation := applyLiveMessageMutation(&model, event)
			_, notified := notificationForLiveMessage(&model, event, mutation)
			if notified != test.wantNotify {
				t.Fatalf("mutation=%+v notified=%t", mutation, notified)
			}
			// A replay of the identical committed MessageID may update metadata,
			// but cannot create another notification.
			duplicate := applyLiveMessageMutation(&model, event)
			if _, notified := notificationForLiveMessage(&model, event, duplicate); notified {
				t.Fatal("duplicate replay notified")
			}
		})
	}
}

func TestNotificationContentPrivacyDirectGroupAndMedia(t *testing.T) {
	direct := notificationTestView(t, false)
	directEvent := notificationEvent("direct", false)
	mutation := applyLiveMessageMutation(&direct, directEvent)
	privacy, ok := notificationForLiveMessage(&direct, directEvent, mutation)
	if !ok || privacy != (Notification{Title: "walite", Body: "New WhatsApp message"}) {
		t.Fatalf("privacy notification=%+v ok=%t", privacy, ok)
	}

	group := notificationTestView(t, true)
	group.options.NotificationPreviews = true
	applyDisplayMetadata(&group, DisplayMetadata{ID: "alice@lid", Name: "Alice", Quality: 5})
	groupEvent := notificationEvent("group", true)
	mutation = applyLiveMessageMutation(&group, groupEvent)
	presented, ok := notificationForLiveMessage(&group, groupEvent, mutation)
	if !ok || presented.Title != "Alice — Family" || presented.Body != "Are you coming tonight?" {
		t.Fatalf("group notification=%+v ok=%t", presented, ok)
	}

	media := notificationTestView(t, false)
	media.options.NotificationPreviews = true
	mediaEvent := notificationEvent("document", false)
	mediaEvent.Text = "Quarterly café results"
	mediaEvent.MediaKind, mediaEvent.MediaName, mediaEvent.MediaMIME = mediaDocument, "report.pdf", "application/pdf"
	mutation = applyLiveMessageMutation(&media, mediaEvent)
	presented, ok = notificationForLiveMessage(&media, mediaEvent, mutation)
	if !ok || presented.Title != "Alice" || presented.Body != "[Document: report.pdf] Quarterly café results" {
		t.Fatalf("media notification=%+v ok=%t", presented, ok)
	}
}

func TestNotificationTextSanitizationIsUnicodeSafeAndBounded(t *testing.T) {
	value := "  Café 👨‍👩‍👧‍👦\n\x1b[31m ' \" & --flag\u202e  " + strings.Repeat("🙂", notificationBodyLimit+20)
	got := sanitizeNotificationText(value, notificationBodyLimit)
	if !utf8.ValidString(got) || strings.ContainsAny(got, "\n\r\t\x1b") || strings.ContainsRune(got, '\u202e') {
		t.Fatalf("unsafe sanitized text=%q", got)
	}
	if !strings.Contains(got, "Café 👨‍👩‍👧‍👦 [31m ' \" & --flag") || !strings.HasSuffix(got, "…") {
		t.Fatalf("Unicode/special data not preserved or bounded: %q", got)
	}
	clusters := 0
	for graphemes := uniseg.NewGraphemes(got); graphemes.Next(); {
		clusters++
	}
	if clusters > notificationBodyLimit {
		t.Fatalf("clusters=%d limit=%d", clusters, notificationBodyLimit)
	}
}

func TestRunNotifiesOnlyForEligibleLiveInsertion(t *testing.T) {
	at := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	live := make(chan LiveMessage)
	reactions := make(chan ReactionUpdate)
	display := make(chan DisplayMetadata)
	olderResults := make(chan OlderHistoryResult)
	olderRequests := make(chan OlderHistoryRequest, 1)
	notifications := make(chan Notification, 2)
	screen := newObservedScreen(100, 24)
	done := make(chan error, 1)
	go func() {
		done <- runWithDependencies(context.Background(), screen, Input{
			Options: DefaultOptions(),
			InitialState: InitialState{Chats: []InitialChat{{ID: "chat@lid", Title: "Alice", UnreadCount: 4, ActivityTime: at, Messages: []InitialMessage{
				{ID: "cached", SentAt: at, Text: "startup cache", BodyRetained: true},
			}}}},
			LiveEvents: live, ReactionUpdates: reactions, DisplayUpdates: display,
			OlderHistory: func(request OlderHistoryRequest) bool { olderRequests <- request; return true }, OlderResults: olderResults,
			Notify: func(notification Notification) bool { notifications <- notification; return true },
		}, "")
	}()
	<-screen.shown
	select {
	case notification := <-notifications:
		t.Fatalf("startup cache notified: %+v", notification)
	default:
	}
	screen.InjectKey(tcell.KeyRune, 'O', tcell.ModNone)
	request := <-olderRequests
	<-screen.shown // loading status
	olderResults <- OlderHistoryResult{Request: request, Kind: OlderHistoryLoaded, Messages: []InitialMessage{{
		ID: "older-incoming", SentAt: at.Add(-time.Hour), Text: "unread historical page", BodyRetained: true,
	}}}
	<-screen.shown
	select {
	case notification := <-notifications:
		t.Fatalf("older-history page notified: %+v", notification)
	default:
	}
	display <- DisplayMetadata{ID: "chat@lid", Name: "Alice Updated", Quality: 5}
	<-screen.shown
	reactions <- ReactionUpdate{ChatID: "chat@lid", TargetMessageID: "cached", Groups: []ReactionGroup{{Emoji: "👍", Count: 1}}}
	<-screen.shown
	select {
	case notification := <-notifications:
		t.Fatalf("metadata/reaction update notified: %+v", notification)
	default:
	}
	event := LiveMessage{
		ChatID: "chat@lid", MessageID: "live", SentAt: at.Add(time.Minute), ActivityTime: at.Add(time.Minute),
		Text: "new live", BodyRetained: true, UnreadCount: 1, NotificationEligible: true,
	}
	live <- event
	if notification := <-notifications; notification != (Notification{Title: "walite", Body: "New WhatsApp message"}) {
		t.Fatalf("notification=%+v", notification)
	}
	<-screen.shown
	live <- event
	select {
	case notification := <-notifications:
		t.Fatalf("duplicate live event notified: %+v", notification)
	default:
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
