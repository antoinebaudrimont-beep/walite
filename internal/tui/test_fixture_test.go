package tui

import (
	"context"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"
)

func defaultDemoView() viewModel {
	return viewModel{chats: newDemoChatState(), options: DefaultOptions()}
}

func runWithPreferences(ctx context.Context, screen tcell.Screen, preferencesPath string) error {
	return runWithDependencies(ctx, screen, Input{Options: DefaultOptions(), InitialState: testInitialState()}, preferencesPath)
}

func newDemoChatState() *chatState {
	state, err := chatStateFromInitial(testInitialState())
	if err != nil {
		panic(err)
	}
	return state
}

func testInitialState() InitialState {
	base := time.Date(2100, 1, 2, 10, 0, 0, 0, time.UTC)
	definitions := [...]struct {
		id, title, label string
		count            int
		unread           uint32
	}{
		{id: "demo", title: "Demo Chat", label: "Demo", count: 18},
		{id: "project", title: "Project Room", label: "Project", count: 15, unread: 3},
		{id: "family", title: "Family Demo", label: "Family-demo", count: 12, unread: 1},
		{id: "contact", title: "Test Contact", label: "Test-contact", count: 10, unread: 12},
	}
	nextID := 1
	initial := InitialState{Chats: make([]InitialChat, len(definitions))}
	for chatIndex, definition := range definitions {
		chat := InitialChat{
			ID:           definition.id,
			Title:        definition.title,
			UnreadCount:  definition.unread,
			ActivityTime: base.Add(time.Duration(len(definitions)-chatIndex) * time.Minute),
			Messages:     make([]InitialMessage, definition.count),
		}
		for messageIndex := range chat.Messages {
			text := definition.label + " synthetic message " + strconv.Itoa(messageIndex+1)
			sentAt := base.Add(time.Duration(messageIndex) * time.Minute)
			if chatIndex == 0 {
				special := [...]string{
					"Synthetic message one",
					"Synthetic reply",
					"Synthetic terminal preview",
					"Synthetic wrapping preview that stays bounded while demonstrating a longer conversation line",
				}
				if messageIndex < len(special) {
					text = special[messageIndex]
					minute := [...]int{42, 45, 47, 49}[messageIndex]
					sentAt = time.Date(2100, 1, 2, 9, minute, 0, 0, time.UTC)
				}
			}
			chat.Messages[messageIndex] = InitialMessage{
				ID:           strconv.Itoa(nextID),
				SentAt:       sentAt,
				FromMe:       messageIndex%2 == 1,
				Text:         text,
				BodyRetained: true,
			}
			nextID++
		}
		initial.Chats[chatIndex] = chat
	}
	return initial
}

func testMessageID(value int) messageID {
	return messageID(strconv.Itoa(value))
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}
