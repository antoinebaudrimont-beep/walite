package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

const prototypeStateFixture = `{
  "version": 1,
  "selectedChat": 1,
  "nextMessageId": 44,
  "nextActivity": 9,
  "chats": [
    {
      "title": "Prototype First",
      "unreadCount": 7,
      "activity": 8,
      "messages": [
        {"id": 41, "timestamp": "08:00", "text": "prototype first body"}
      ]
    },
    {
      "title": "Prototype Selected",
      "unreadCount": 3,
      "activity": 7,
      "messages": [
        {"id": 42, "timestamp": "08:01", "text": "prototype selected body"},
        {"id": 43, "timestamp": "08:02", "text": "prototype reply body", "replyToId": 42, "hasReply": true}
      ]
    }
  ],
  "futureUnknown": {"ignored": true}
}`

func TestPrototypeStateIsIgnoredAndNeverRewritten(t *testing.T) {
	home := t.TempDir()
	path := writePrototypeState(t, home, []byte(prototypeStateFixture))
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	screen, result := startPersistenceTestTUI(t, home)

	text := screenText(screen)
	if !strings.Contains(text, "Demo Chat") || !strings.Contains(text, "Synthetic message one") {
		t.Fatalf("first frame did not use fresh demo state:\n%s", text)
	}
	for _, obsolete := range []string{"Prototype First", "Prototype Selected", "prototype selected body"} {
		if strings.Contains(text, obsolete) {
			t.Fatalf("first frame restored obsolete field %q:\n%s", obsolete, text)
		}
	}

	enterComposeAndSend(t, screen, "runtime-only message")
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("Run=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("prototype state.json was modified")
	}
}

func TestMalformedAndOversizedPrototypeStateCannotBreakStartup(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "truncated", data: []byte(`{"version":1,"chats":[`)},
		{name: "oversized", data: []byte(strings.Repeat("x", 64*1024+1))},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			path := writePrototypeState(t, home, test.data)
			screen, result := startPersistenceTestTUI(t, home)
			if text := screenText(screen); !strings.Contains(text, "Synthetic message one") {
				t.Fatalf("startup did not fall back to demo state:\n%s", text)
			}
			screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
			if err := <-result; err != nil {
				t.Fatalf("Run=%v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(test.data) {
				t.Fatal("obsolete malformed state was replaced")
			}
		})
	}
}

func TestRuntimeChatMutationsDoNotSurviveRestartOrCreateState(t *testing.T) {
	home := t.TempDir()
	statePath := prototypeStatePath(home)

	firstScreen, firstResult := startPersistenceTestTUI(t, home)
	firstScreen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	<-firstScreen.shown
	enterComposeAndSend(t, firstScreen, "restart must forget this")
	firstScreen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	<-firstScreen.shown
	firstScreen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-firstResult; err != nil {
		t.Fatalf("first Run=%v", err)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state.json exists after chat mutation: %v", err)
	}

	secondScreen, secondResult := startPersistenceTestTUI(t, home)
	text := screenText(secondScreen)
	if !strings.Contains(text, "Synthetic message one") || strings.Contains(text, "restart must forget this") {
		t.Fatalf("restart did not rebuild fresh demo state:\n%s", text)
	}
	secondScreen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-secondResult; err != nil {
		t.Fatalf("second Run=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "walite", "ui-state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected replacement UI state exists: %v", err)
	}
}

func startPersistenceTestTUI(t *testing.T, home string) (*observedScreen, <-chan error) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() {
		result <- Run(context.Background(), screen, Input{Options: DefaultOptions(), InitialState: testInitialState()})
	}()
	<-screen.shown
	return screen, result
}

func enterComposeAndSend(t *testing.T, screen *observedScreen, text string) {
	t.Helper()
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	for _, value := range text {
		screen.InjectKey(tcell.KeyRune, value, tcell.ModNone)
		<-screen.shown
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
}

func writePrototypeState(t *testing.T, home string, data []byte) string {
	t.Helper()
	path := prototypeStatePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func prototypeStatePath(home string) string {
	return filepath.Join(home, ".local", "share", "walite", "state.json")
}
