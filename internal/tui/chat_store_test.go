package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestFileChatStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walite", "state.json")
	store := newFileChatStateStore(path)
	want := persistentTestChatState(t)

	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded chat state differs\ngot:  %+v\nwant: %+v", got, want)
	}

	chat, ok := got.chatAt(0)
	if !ok || chat.unreadCount != want.chats[0].unreadCount || chat.activity != want.chats[0].activity {
		t.Fatalf("top chat after load=%+v found=%t", chat, ok)
	}
	reply := chat.messages[1]
	if reply.id != want.chats[0].messages[1].id || !reply.hasReply || reply.replyToID != chat.messages[0].id {
		t.Fatalf("reply identity after load=%+v", reply)
	}
}

func TestMissingChatStateLoadsDemoState(t *testing.T) {
	store := newFileChatStateStore(filepath.Join(t.TempDir(), "missing", "state.json"))
	got, err := loadChatStateOrDemo(store)
	if err != nil {
		t.Fatal(err)
	}
	want := newDemoChatState()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missing state did not produce demo state\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestCorruptChatStateReturnsControlledError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"chats":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newFileChatStateStore(path).Load()
	if !errors.Is(err, errCorruptChatState) {
		t.Fatalf("Load error=%v", err)
	}
}

func TestAtomicChatStateSavePreservesExistingFileOnRejectedState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "walite")
	path := filepath.Join(directory, "state.json")
	store := newFileChatStateStore(path)
	state := persistentTestChatState(t)
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	invalid := *state
	invalid.nextMessageID = 0
	if err := store.Save(&invalid); err == nil {
		t.Fatal("invalid state was saved")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("rejected save changed the existing state file")
	}

	renameErr := errors.New("synthetic rename failure")
	updated := *state
	updated.chats[0].unreadCount++
	failingStore := &fileChatStateStore{
		path: path,
		renameFile: func(string, string) error {
			return renameErr
		},
	}
	if err := failingStore.Save(&updated); !errors.Is(err, renameErr) {
		t.Fatalf("Save error=%v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("failed atomic replacement changed the existing state file")
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(directory, ".state-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary files left behind: %v", temporaryFiles)
	}
}

func TestChatStateStorePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not supported")
	}
	directory := filepath.Join(t.TempDir(), "walite")
	path := filepath.Join(directory, "state.json")
	if err := newFileChatStateStore(path).Save(newDemoChatState()); err != nil {
		t.Fatal(err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory permissions=%#o", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file permissions=%#o", got)
	}
}

func TestSavedChatStateOmitsTransientUIState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := newFileChatStateStore(path).Save(newDemoChatState()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"terminalWidth", "terminalHeight", "scrollOffset", "composer", "emojiPicker", "mode"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("saved state contains transient field %q:\n%s", forbidden, data)
		}
	}
}

func TestRunSavesChatStateOnNormalExit(t *testing.T) {
	store := newFileChatStateStore(filepath.Join(t.TempDir(), "walite", "state.json"))
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() { result <- runWithStore(context.Background(), screen, "", store) }()

	<-screen.shown
	screen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("runWithStore=%v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.selected != 1 || loaded.chats[1].title != "Project Room" || loaded.chats[1].unreadCount != 0 {
		t.Fatalf("saved selection=%d chat=%q unread=%d", loaded.selected, loaded.chats[1].title, loaded.chats[1].unreadCount)
	}
}

func TestRunLoadsSavedChatStateAtStartup(t *testing.T) {
	store := newFileChatStateStore(filepath.Join(t.TempDir(), "walite", "state.json"))
	if err := store.Save(persistentTestChatState(t)); err != nil {
		t.Fatal(err)
	}
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() { result <- runWithStore(context.Background(), screen, "", store) }()

	<-screen.shown
	text := screenText(screen)
	if !strings.Contains(text, "Family Demo") || !strings.Contains(text, "Family-demo synthetic message 12") {
		t.Fatalf("first frame did not use saved selected chat:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("runWithStore=%v", err)
	}
}

func persistentTestChatState(t *testing.T) *chatState {
	t.Helper()
	state := newDemoChatState()
	state.selected = 2
	state.chats[0].unreadCount = 7
	state.chats[2].messages[1].hasReply = true
	state.chats[2].messages[1].replyToID = state.chats[2].messages[0].id
	if !state.recordUnreadActivity(2, 4) {
		t.Fatal("activity setup failed")
	}
	return state
}
