package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMessageDateSeparatorsUseLocalCalendarDays(t *testing.T) {
	firstDay := time.Date(2026, 8, 31, 21, 44, 0, 0, time.Local)
	secondDay := time.Date(2026, 9, 1, 5, 38, 0, 0, time.Local)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "dates", Title: "Dates", ActivityTime: secondDay,
		Messages: []InitialMessage{
			{ID: "one", SentAt: firstDay.UTC(), Text: "incoming Café 👋", BodyRetained: true},
			{ID: "two", SentAt: firstDay.Add(3 * time.Minute).UTC(), FromMe: true, Text: "outgoing 日本語", BodyRetained: true},
			{ID: "three", SentAt: secondDay.UTC(), Text: "next day ❤️", BodyRetained: true, ReplyToID: "one", ReplyToText: "incoming Café 👋"},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions()}
	screen := initializedSimulationScreen(t, 100, 30)
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	for _, at := range []time.Time{firstDay, secondDay} {
		if label := messageDateLabel(at); strings.Count(text, label) != 1 {
			t.Fatalf("date %q count=%d:\n%s", label, strings.Count(text, label), text)
		}
	}
	if strings.Count(text, messageDateLabel(firstDay)) != 1 || !strings.Contains(text, "incoming Café 👋") ||
		!strings.Contains(text, "outgoing") || !strings.Contains(text, "↪ "+localMessageTime(firstDay)+" incoming Café 👋") || !strings.Contains(text, "next day") {
		t.Fatalf("message presentation changed:\n%s", text)
	}
}

func TestUTCInstantsShareOrSplitSeparatorByLocalDay(t *testing.T) {
	localMorning := time.Date(2026, 9, 1, 0, 15, 0, 0, time.Local)
	for _, test := range []struct {
		name   string
		times  []time.Time
		labels int
	}{
		{name: "same local day", times: []time.Time{localMorning.UTC(), localMorning.Add(22 * time.Hour).UTC()}, labels: 1},
		{name: "different local days", times: []time.Time{localMorning.Add(-16 * time.Minute).UTC(), localMorning.UTC()}, labels: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			messages := make([]InitialMessage, len(test.times))
			for index, at := range test.times {
				messages[index] = InitialMessage{ID: fmt.Sprintf("m-%d", index), SentAt: at, Text: fmt.Sprintf("body-%d", index), BodyRetained: true}
			}
			state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{ID: "chat", Title: "Chat", ActivityTime: test.times[len(test.times)-1], Messages: messages}}})
			if err != nil {
				t.Fatal(err)
			}
			model := viewModel{chats: state, options: DefaultOptions()}
			screen := initializedSimulationScreen(t, 100, 20)
			draw(screen, &model)
			screen.Show()
			if got := strings.Count(screenText(screen), " 2026 "); got != test.labels {
				t.Fatalf("separator count=%d want=%d:\n%s", got, test.labels, screenText(screen))
			}
		})
	}
}

func TestScrolledViewportStartsWithDateContext(t *testing.T) {
	day := time.Date(2026, 9, 1, 8, 0, 0, 0, time.Local)
	messages := make([]InitialMessage, 20)
	for index := range messages {
		messages[index] = InitialMessage{ID: fmt.Sprintf("m-%02d", index), SentAt: day.Add(time.Duration(index) * time.Minute), Text: fmt.Sprintf("message-%02d", index), BodyRetained: true}
	}
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{ID: "chat", Title: "Chat", ActivityTime: day.Add(19 * time.Minute), Messages: messages}}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions()}
	model.chatView.scrollOffset = 5
	screen := initializedSimulationScreen(t, 70, 12)
	draw(screen, &model)
	screen.Show()
	start, _ := visibleMessageRange(&model, 70, 12)
	if start == 0 || strings.Count(screenText(screen), messageDateLabel(day)) != 1 {
		t.Fatalf("start=%d date context missing:\n%s", start, screenText(screen))
	}
}

func TestRealtimeNewLocalDayAddsSeparatorBesideUnreadBoundary(t *testing.T) {
	firstDay := time.Date(2026, 8, 31, 23, 58, 0, 0, time.Local)
	secondDay := firstDay.Add(3 * time.Minute)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "chat", Title: "Chat", ActivityTime: firstDay, UnreadCount: 1,
		Messages: []InitialMessage{{ID: "old", SentAt: firstDay, Text: "old 👋", BodyRetained: true}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions()}
	model.chatView.unreadBoundary = unreadBoundaryForChat(&state.chats[0])
	if !applyLiveMessage(&model, LiveMessage{
		ChatID: "chat", MessageID: "new", SentAt: secondDay, Text: "new 日本語", BodyRetained: true,
		UnreadCount: 2, ActivityTime: secondDay,
	}) {
		t.Fatal("live message rejected")
	}
	screen := initializedSimulationScreen(t, 100, 30)
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	if strings.Count(text, messageDateLabel(firstDay)) != 1 || strings.Count(text, messageDateLabel(secondDay)) != 1 || !strings.Contains(text, "1 new message") ||
		rowContaining(text, messageDateLabel(firstDay)) >= rowContaining(text, "1 new message") ||
		rowContaining(text, messageDateLabel(secondDay)) >= rowContaining(text, localMessageTime(secondDay)) {
		t.Fatalf("date/unread ordering: counts=%d/%d rows=%d/%d/%d/%d:\n%s",
			strings.Count(text, messageDateLabel(firstDay)), strings.Count(text, messageDateLabel(secondDay)),
			rowContaining(text, messageDateLabel(firstDay)), rowContaining(text, "1 new message"),
			rowContaining(text, messageDateLabel(secondDay)), rowContaining(text, localMessageTime(secondDay)), text)
	}
}
