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

func requireNotification(t *testing.T, model *viewModel, event LiveMessage) Notification {
	t.Helper()
	mutation := applyLiveMessageMutation(model, event)
	notification, ok := notificationForLiveMessage(model, event, mutation)
	if !ok {
		t.Fatalf("event was not admitted: mutation=%+v event=%+v", mutation, event)
	}
	return notification
}

func notificationGraphemeCount(value string) int {
	count := 0
	for graphemes := uniseg.NewGraphemes(value); graphemes.Next(); {
		count++
	}
	return count
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

func TestNotificationPreviewsOffNeverLeakContentOrMetadata(t *testing.T) {
	tests := []struct {
		name, kind, mediaName string
		group                 bool
	}{
		{name: "direct text"},
		{name: "group text", group: true},
		{name: "image", kind: mediaImage},
		{name: "video", kind: mediaVideo},
		{name: "audio", kind: mediaAudio},
		{name: "document", kind: mediaDocument, mediaName: "secret-report.pdf"},
		{name: "sticker", kind: mediaSticker},
	}
	want := Notification{Title: "walite", Body: "New WhatsApp message"}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := notificationTestView(t, test.group)
			applyDisplayMetadata(&model, DisplayMetadata{ID: "alice@lid", Name: "Private Sender", Quality: 5})
			event := notificationEvent("private-"+string(rune('a'+index)), test.group)
			event.Text = "Private caption and message"
			event.MediaKind, event.MediaName = test.kind, test.mediaName
			if got := requireNotification(t, &model, event); got != want {
				t.Fatalf("notification=%+v want=%+v", got, want)
			}
		})
	}
}

func TestNotificationPreviewDirectGroupAndFallbackIdentity(t *testing.T) {
	direct := notificationTestView(t, false)
	direct.options.NotificationPreviews = true
	if got := requireNotification(t, &direct, notificationEvent("direct", false)); got != (Notification{Title: "Alice", Body: "Are you coming tonight?"}) {
		t.Fatalf("direct notification=%+v", got)
	}

	fallback := notificationTestView(t, false)
	fallback.options.NotificationPreviews = true
	fallback.chats.chats[0].title = "+12086708856"
	if got := requireNotification(t, &fallback, notificationEvent("fallback", false)); got.Title != "+12086708856" {
		t.Fatalf("direct fallback=%+v", got)
	}

	group := notificationTestView(t, true)
	group.options.NotificationPreviews = true
	applyDisplayMetadata(&group, DisplayMetadata{ID: "alice@lid", Name: "Alice", Quality: 5})
	if got := requireNotification(t, &group, notificationEvent("group", true)); got != (Notification{Title: "Alice — Family", Body: "Are you coming tonight?"}) {
		t.Fatalf("group notification=%+v", got)
	}

	groupFallback := notificationTestView(t, true)
	groupFallback.options.NotificationPreviews = true
	groupFallback.chats.chats[0].title = "family@g.us"
	event := notificationEvent("group-fallback", true)
	event.SenderID = "987654@lid"
	if got := requireNotification(t, &groupFallback, event); got.Title != "987654@lid — family@g.us" {
		t.Fatalf("group fallback=%+v", got)
	}
}

func TestNotificationPreviewMediaPlaceholdersAndCaptions(t *testing.T) {
	tests := []struct {
		name, kind, mediaName, caption, want string
	}{
		{name: "image", kind: mediaImage, want: "[Image]"},
		{name: "captioned image", kind: mediaImage, caption: "Holiday photo", want: "[Image] Holiday photo"},
		{name: "video", kind: mediaVideo, want: "[Video]"},
		{name: "audio", kind: mediaAudio, want: "[Audio]"},
		{name: "sticker", kind: mediaSticker, want: "[Sticker]"},
		{name: "document", kind: mediaDocument, mediaName: "report.pdf", want: "[Document: report.pdf]"},
		{name: "document path and caption", kind: mediaDocument, mediaName: "../../private/report\r\nfinal.pdf", caption: "Quarterly café results", want: "[Document: report final.pdf] Quarterly café results"},
		{name: "windows path", kind: mediaDocument, mediaName: `C:\private\voice.pdf`, want: "[Document: voice.pdf]"},
		{name: "empty document name", kind: mediaDocument, mediaName: "folder/", want: "[Document]"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := notificationTestView(t, false)
			model.options.NotificationPreviews = true
			event := notificationEvent("media-"+string(rune('a'+index)), false)
			event.Text, event.MediaKind, event.MediaName = test.caption, test.kind, test.mediaName
			if got := requireNotification(t, &model, event); got.Body != test.want {
				t.Fatalf("body=%q want=%q", got.Body, test.want)
			}
		})
	}
}

func TestNotificationPreviewUsesNewMessageOnlyAndHasEmptyFallback(t *testing.T) {
	model := notificationTestView(t, false)
	model.options.NotificationPreviews = true
	event := notificationEvent("reply", false)
	event.Text = "New reply text"
	event.ReplyToID, event.ReplyToText = "quoted", "Do not repeat this quoted content"
	if got := requireNotification(t, &model, event); got.Body != "New reply text" || strings.Contains(got.Body, event.ReplyToText) {
		t.Fatalf("reply notification=%+v", got)
	}

	empty := notificationTestView(t, false)
	empty.options.NotificationPreviews = true
	emptyEvent := notificationEvent("empty", false)
	emptyEvent.Text = "\n\t\x00\u202e"
	if got := requireNotification(t, &empty, emptyEvent); got.Body != "New message" {
		t.Fatalf("empty notification=%+v", got)
	}

	media := notificationTestView(t, false)
	media.options.NotificationPreviews = true
	mediaEvent := notificationEvent("empty-media", false)
	mediaEvent.Text, mediaEvent.MediaKind = "\n\t\x00", mediaImage
	if got := requireNotification(t, &media, mediaEvent); got.Body != "[Image]" {
		t.Fatalf("empty media notification=%+v", got)
	}
}

func TestNotificationTextSanitizationIsUnicodeSafeAndBounded(t *testing.T) {
	value := "  Café Ελληνικά 東京 🖕 👍🏽 👨‍👩‍👧‍👦\r\n\t\x1b[31m ' \" \\ & ; $ ` --flag (x) [y]\u202e  " + strings.Repeat("🙂", notificationBodyLimit+20)
	got := sanitizeNotificationText(value, notificationBodyLimit)
	if !utf8.ValidString(got) || strings.ContainsAny(got, "\n\r\t\x1b") || strings.ContainsRune(got, '\u202e') {
		t.Fatalf("unsafe sanitized text=%q", got)
	}
	if !strings.Contains(got, "Café Ελληνικά 東京 🖕 👍🏽 👨‍👩‍👧‍👦 [31m ' \" \\ & ; $ ` --flag (x) [y]") || !strings.HasSuffix(got, "…") {
		t.Fatalf("Unicode/special data not preserved or bounded: %q", got)
	}
	if clusters := notificationGraphemeCount(got); clusters > notificationBodyLimit {
		t.Fatalf("clusters=%d limit=%d", clusters, notificationBodyLimit)
	}

	if got := sanitizeNotificationText("  one\r\ntwo\tthree\u0000four\u0085five  ", 50); got != "one two three four five" {
		t.Fatalf("whitespace/control collapse=%q", got)
	}
	codeLooking := `--hello ' " \\ & ; $ ` + "`" + ` ( ) [] display notification do shell script`
	if got := sanitizeNotificationText(codeLooking, 100); got != codeLooking {
		t.Fatalf("punctuation changed: %q", got)
	}
	if got := sanitizeNotificationText("\n\t\x00\u202e", 10); got != "" {
		t.Fatalf("control-only text=%q", got)
	}
}

func TestNotificationTruncationIsGraphemeSafeAndEllipsized(t *testing.T) {
	exact := strings.Repeat("é", notificationTitleLimit)
	if got := sanitizeNotificationText(exact, notificationTitleLimit); got != exact {
		t.Fatalf("exact-bound text changed: %q", got)
	}
	oneOver := strings.Repeat("界", notificationTitleLimit+1)
	got := sanitizeNotificationText(oneOver, notificationTitleLimit)
	if !utf8.ValidString(got) || notificationGraphemeCount(got) != notificationTitleLimit || !strings.HasSuffix(got, "…") {
		t.Fatalf("one-over truncation=%q clusters=%d", got, notificationGraphemeCount(got))
	}
	emoji := strings.Repeat("👨‍👩‍👧‍👦", notificationBodyLimit+1)
	got = sanitizeNotificationText(emoji, notificationBodyLimit)
	if !utf8.ValidString(got) || notificationGraphemeCount(got) != notificationBodyLimit || !strings.HasSuffix(got, "…") || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("emoji truncation invalid: clusters=%d", notificationGraphemeCount(got))
	}
	if got := sanitizeNotificationText("👍🏽x", 2); got != "👍🏽x" {
		t.Fatalf("skin-tone grapheme changed: %q", got)
	}
}

func TestNotificationLongGroupTitleAndFilenameStayBounded(t *testing.T) {
	model := notificationTestView(t, true)
	model.options.NotificationPreviews = true
	model.chats.chats[0].title = strings.Repeat("家族", notificationTitleLimit)
	applyDisplayMetadata(&model, DisplayMetadata{ID: "alice@lid", Name: strings.Repeat("Elena👨‍👩‍👧‍👦", 25), Quality: 5})
	event := notificationEvent("long", true)
	event.MediaKind = mediaDocument
	event.MediaName = "/private/path/--" + strings.Repeat("résumé", 15) + ".pdf"
	event.Text = strings.Repeat("caption🙂", notificationBodyLimit)
	got := requireNotification(t, &model, event)
	if notificationGraphemeCount(got.Title) > notificationTitleLimit || !strings.Contains(got.Title, " — ") || strings.Contains(got.Body, "/private/path/") ||
		notificationGraphemeCount(got.Body) > notificationBodyLimit || !strings.HasSuffix(got.Body, "…") || !utf8.ValidString(got.Title+got.Body) {
		t.Fatalf("bounded notification=%+v titleClusters=%d bodyClusters=%d", got, notificationGraphemeCount(got.Title), notificationGraphemeCount(got.Body))
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
