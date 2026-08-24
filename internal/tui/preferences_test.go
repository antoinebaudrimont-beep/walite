package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestEmojiPreferencesSaveLoadBoundsAndDeduplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walite", "preferences.json")
	var picker emojiPickerState
	for _, value := range []string{"😂", "❤️", "👍", "🙂", "🎉", "🇦🇹", "✌️", "👍🏽", "😀", "😃", "😄"} {
		picker.remember(value)
	}
	picker.remember("👍")
	if err := saveEmojiPreferences(path, &picker); err != nil {
		t.Fatal(err)
	}

	var loaded emojiPickerState
	if err := loadEmojiPreferences(path, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.recentCount != maxRecentEmoji || loaded.recent[0] != "👍" {
		t.Fatalf("loaded count=%d first=%q", loaded.recentCount, loaded.recent[0])
	}
	seen := make(map[string]bool)
	for index := 0; index < loaded.recentCount; index++ {
		if seen[loaded.recent[index]] {
			t.Fatalf("duplicate recent %q", loaded.recent[index])
		}
		seen[loaded.recent[index]] = true
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("preferences mode=%o", info.Mode().Perm())
	}
}

func TestEmojiInsertionPersistsRecentsAcrossPickerRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walite", "preferences.json")
	model := defaultDemoView()
	model.mode = modeCompose
	model.preferencesPath = path
	model.emojiPicker.open = true
	model.emojiPicker.category = 7
	model.emojiPicker.categoryCursor = 0
	model.emojiPicker.focus = emojiFocusCategory
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 80, 24); !changed || exit {
		t.Fatalf("insert changed=%t exit=%t", changed, exit)
	}

	var restarted emojiPickerState
	if err := loadEmojiPreferences(path, &restarted); err != nil {
		t.Fatal(err)
	}
	if restarted.recentCount != 1 || restarted.recent[0] != "❤️" {
		t.Fatalf("restarted recents=%d first=%q", restarted.recentCount, restarted.recent[0])
	}
}

func TestEmojiPreferencesLoadSanitizesOrderAndMaximum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	input := preferencesFile{RecentEmoji: []string{
		"😂", "❤️", "😂", "", "not emoji", "👍", "🙂", "🎉", "🇦🇹", "✌️", "👍🏽", "😀", "😃", "😄",
	}}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	picker := emojiPickerState{recentCursor: 99, recentCount: maxRecentEmoji, focus: emojiFocusRecent}
	if err := loadEmojiPreferences(path, &picker); err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), picker.recent[:picker.recentCount]...)
	want := []string{"😂", "❤️", "👍", "🙂", "🎉", "🇦🇹", "✌️", "👍🏽", "😀", "😃"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recents=%q want=%q", got, want)
	}
	if picker.recentCursor != 0 || picker.recentCursor >= picker.recentCount {
		t.Fatalf("recent cursor=%d count=%d", picker.recentCursor, picker.recentCount)
	}
}

func TestDefaultPreferencesPathUsesConfigDirectory(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	path, err := defaultPreferencesPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(config, "walite", "preferences.json")
	if path != want {
		t.Fatalf("path=%q want=%q", path, want)
	}
}

func TestRunLoadsRecentEmojiPreferencesAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walite", "preferences.json")
	var picker emojiPickerState
	picker.remember("❤️")
	if err := saveEmojiPreferences(path, &picker); err != nil {
		t.Fatal(err)
	}

	screen := newObservedScreen(80, 24)
	result := make(chan error, 1)
	go func() { result <- runWithPreferences(context.Background(), screen, path) }()
	<-screen.shown
	injectKeyAndWait(screen, tcell.KeyEnter, 0)
	injectKeyAndWait(screen, tcell.KeyCtrlE, 0)
	if !screenContainsRuneSequence(screen, "❤️") {
		t.Fatal("startup preferences were not loaded into Recent")
	}
	injectKeyAndWait(screen, tcell.KeyEscape, 0)
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("run=%v", err)
	}
}
