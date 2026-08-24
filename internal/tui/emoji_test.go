package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

func TestEmojiCatalogHasAllBoundedCategoriesAndGraphemes(t *testing.T) {
	wantNames := []string{"Smileys", "People", "Animals", "Food", "Activities", "Travel", "Objects", "Symbols", "Flags"}
	total := 0
	all := strings.Builder{}
	for index, category := range emojiCategories {
		if category.name != wantNames[index] || len(category.values) == 0 {
			t.Fatalf("category %d=%q items=%d", index, category.name, len(category.values))
		}
		for _, value := range category.values {
			if !utf8.ValidString(value) || uniseg.GraphemeClusterCount(value) != 1 || uniseg.StringWidth(value) < 1 {
				t.Fatalf("invalid emoji grapheme %q in %s", value, category.name)
			}
			all.WriteString(value)
		}
		total += len(category.values)
	}
	if total <= 48 {
		t.Fatalf("catalog size=%d", total)
	}
	for _, required := range []string{"🙂", "❤️", "👍🏽", "✌️", "👨‍👩‍👧‍👦", "🇦🇹"} {
		if !strings.Contains(all.String(), required) {
			t.Fatalf("catalog missing %q", required)
		}
	}
}

func TestEmojiPickerOpensOnlyInComposeAndEscCloses(t *testing.T) {
	model := defaultDemoView()
	ctrlE := tcell.NewEventKey(tcell.KeyCtrlE, 0, tcell.ModNone)
	if changed, exit := handleKey(&model, ctrlE, 100, 30); changed || exit || model.emojiPicker.open {
		t.Fatalf("navigation Ctrl-E changed=%t exit=%t open=%t", changed, exit, model.emojiPicker.open)
	}
	model.mode = modeCompose
	insertComposerText(t, &model.composer, "draft")
	if changed, exit := handleKey(&model, ctrlE, 100, 30); !changed || exit || !model.emojiPicker.open {
		t.Fatalf("compose Ctrl-E changed=%t exit=%t open=%t", changed, exit, model.emojiPicker.open)
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 30); !changed || exit {
		t.Fatalf("picker Esc changed=%t exit=%t", changed, exit)
	}
	if model.emojiPicker.open || model.mode != modeCompose || model.composer.text() != "draft" {
		t.Fatalf("open=%t mode=%d draft=%q", model.emojiPicker.open, model.mode, model.composer.text())
	}
}

func TestEmojiPickerNavigationClampsWithoutRedraw(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.open = true
	left := tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone)
	up := tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
	if changed, _ := handleKey(&model, left, 100, 30); changed {
		t.Fatal("left boundary requested redraw")
	}
	if changed, _ := handleKey(&model, up, 100, 30); changed {
		t.Fatal("up boundary requested redraw")
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), 100, 30); !changed || model.emojiPicker.categoryCursor != 1 {
		t.Fatalf("right changed=%t selected=%d", changed, model.emojiPicker.categoryCursor)
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), 100, 30); !changed || model.emojiPicker.categoryCursor != 10 {
		t.Fatalf("down changed=%t selected=%d", changed, model.emojiPicker.categoryCursor)
	}
	model.emojiPicker.categoryCursor = len(emojiCategories[0].values) - 1
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), 100, 30); changed {
		t.Fatal("bottom boundary requested redraw")
	}
}

func TestEmojiPickerTabChangesCategoryAndSelectsItsFirstEmoji(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.open = true
	model.emojiPicker.remember("😂")
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone), 80, 24); !changed {
		t.Fatal("Tab did not change category")
	}
	if model.emojiPicker.category != 1 || model.emojiPicker.selectedEmoji() != emojiCategories[1].values[0] {
		t.Fatalf("category=%d selected=%q", model.emojiPicker.category, model.emojiPicker.selectedEmoji())
	}
}

func TestEmojiCategoryChangeResetsIndependentCursor(t *testing.T) {
	var picker emojiPickerState
	picker.categoryCursor = 12
	picker.recentCursor = 4
	picker.focus = emojiFocusRecent
	for _, value := range []string{"😂", "❤️", "👍", "🙂", "🎉"} {
		picker.remember(value)
	}
	picker.recentCursor = 4
	if !picker.moveCategory(1) {
		t.Fatal("category did not change")
	}
	if picker.category != 1 || picker.categoryCursor != 0 || picker.focus != emojiFocusCategory {
		t.Fatalf("category=%d categoryCursor=%d focus=%d", picker.category, picker.categoryCursor, picker.focus)
	}
	if picker.recentCursor != 4 {
		t.Fatalf("recent cursor changed=%d", picker.recentCursor)
	}
}

func TestEmojiPickerInsertsExactEmojiAndCloses(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.open = true
	model.emojiPicker.categoryCursor = 7
	selected := model.emojiPicker.selectedEmoji()
	if selected != "🙂" {
		t.Fatalf("selected=%q", selected)
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 30); !changed || exit {
		t.Fatalf("insert changed=%t exit=%t", changed, exit)
	}
	if model.composer.text() != selected || model.composer.cursor != len(selected) || model.emojiPicker.open || model.mode != modeCompose {
		t.Fatalf("draft=%q cursor=%d open=%t mode=%d", model.composer.text(), model.composer.cursor, model.emojiPicker.open, model.mode)
	}
	if model.emojiPicker.recentCount != 1 || model.emojiPicker.recent[0] != selected {
		t.Fatalf("recents=%d first=%q", model.emojiPicker.recentCount, model.emojiPicker.recent[0])
	}
}

func TestEmojiPickerInsertsAtComposerCursor(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("hello world") {
		t.Fatal("draft insert failed")
	}
	model.composer.cursor = len("hello ")
	model.emojiPicker.open = true
	model.emojiPicker.categoryCursor = 7
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 30); !changed {
		t.Fatal("emoji insertion did not redraw")
	}
	if got := model.composer.text(); got != "hello 🙂world" {
		t.Fatalf("draft=%q", got)
	}
}

func TestRecentEmojiIsBoundedAndDeduplicated(t *testing.T) {
	var picker emojiPickerState
	values := []string{"😀", "😃", "😄", "😁", "😆", "😅", "😂", "🙂", "🙃", "😉", "😊"}
	for index := 0; index < maxRecentEmoji+1; index++ {
		picker.remember(values[index])
	}
	if picker.recentCount != maxRecentEmoji || picker.recent[0] != values[maxRecentEmoji] || picker.recent[maxRecentEmoji-1] != values[1] {
		t.Fatalf("count=%d first=%q last=%q", picker.recentCount, picker.recent[0], picker.recent[maxRecentEmoji-1])
	}
	picker.remember(values[5])
	if picker.recentCount != maxRecentEmoji || picker.recent[0] != values[5] {
		t.Fatalf("dedup count=%d first=%q", picker.recentCount, picker.recent[0])
	}
	occurrences := 0
	for index := 0; index < picker.recentCount; index++ {
		if picker.recent[index] == values[5] {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("duplicate recent occurrences=%d", occurrences)
	}
}
