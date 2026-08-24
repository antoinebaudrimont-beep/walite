package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestDrawEmojiPickerOverlayAndSelectedStyle(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.open = true
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	for _, want := range []string{"Emoji", "Recent", "arrows choose", "Enter insert", "Esc close", "Demo Chat"} {
		if !strings.Contains(text, want) {
			t.Fatalf("picker missing %q:\n%s", want, text)
		}
	}
	if _, _, visible := screen.GetCursor(); visible {
		t.Fatal("composer cursor visible through emoji picker")
	}
	if !screenHasAttributes(screen, tcell.AttrBold|tcell.AttrReverse) {
		t.Fatal("selected emoji is not visibly highlighted")
	}
}

func TestDrawCompactEmojiPickerFallback(t *testing.T) {
	screen := initializedSimulationScreen(t, 30, 6)
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.open = true
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	for _, want := range []string{"Emoji", "terminal too small", "Esc close"} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact picker missing %q:\n%s", want, text)
		}
	}
}

func TestEmojiPopupLayoutAdaptsAndStaysInsideRequestedScreens(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 20}, {40, 8}} {
		layout := emojiLayout(size[0], size[1])
		if layout.compact {
			t.Fatalf("%dx%d unexpectedly compact", size[0], size[1])
		}
		if layout.left < 0 || layout.top < 0 || layout.right >= size[0] || layout.bottom >= size[1] || layout.columns < 1 || layout.gridRows < 1 {
			t.Fatalf("%dx%d layout=%+v", size[0], size[1], layout)
		}
		screen := initializedSimulationScreen(t, size[0], size[1])
		model := defaultDemoView()
		model.mode = modeCompose
		model.emojiPicker.open = true
		draw(screen, &model)
		screen.Show()
		if text := screenText(screen); !strings.Contains(text, "Smileys") || !strings.Contains(text, "Enter insert") || !strings.Contains(text, "Esc close") {
			t.Fatalf("%dx%d picker incomplete:\n%s", size[0], size[1], text)
		}
		if _, _, visible := screen.GetCursor(); visible {
			t.Fatalf("%dx%d picker cursor visible", size[0], size[1])
		}
	}
	normal := emojiLayout(80, 24)
	if width, height := normal.right-normal.left+1, normal.bottom-normal.top+1; width != 42 || height != 12 {
		t.Fatalf("normal popup=%dx%d want 42x12", width, height)
	}
}

func TestEmojiGridKeepsComplexGraphemeInOneCellSequence(t *testing.T) {
	screen := initializedSimulationScreen(t, 80, 24)
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.open = true
	for category, values := range map[int][]string{
		1: {"✌️", "👍🏽", "👨‍👩‍👧‍👦"},
		7: {"❤️"},
		8: {"🇦🇹"},
	} {
		model.emojiPicker.category = category
		draw(screen, &model)
		screen.Show()
		for _, want := range values {
			if !screenContainsRuneSequence(screen, want) {
				t.Fatalf("category %d split, omitted, or replaced %q", category, want)
			}
		}
	}
}

func TestEmojiCategoryRedrawDoesNotLeavePreviousCategory(t *testing.T) {
	screen := initializedSimulationScreen(t, 80, 24)
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.open = true
	model.emojiPicker.categoryCursor = 7
	draw(screen, &model)
	screen.Show()
	if !screenContainsRuneSequence(screen, "🙂") {
		t.Fatal("initial Smileys grid missing")
	}
	if !model.emojiPicker.moveCategory(1) {
		t.Fatal("category did not change")
	}
	draw(screen, &model)
	screen.Show()
	if screenContainsRuneSequence(screen, "🙂") {
		t.Fatal("previous category emoji remained after redraw")
	}
	if !screenContainsRuneSequence(screen, "👍") {
		t.Fatal("new People category was not rebuilt")
	}
}

func TestEmojiCellUsesVisibleFallbackInsteadOfDroppingUnsupportedValue(t *testing.T) {
	screen := initializedSimulationScreen(t, 20, 4)
	drawEmojiCell(screen, "🙂🙂", true, 2, 1, 18)
	screen.Show()
	if !screenContainsRuneSequence(screen, emojiFallback) {
		t.Fatal("unsupported sequence disappeared instead of using fallback")
	}
}

func TestEmojiPickerStateSurvivesResizeWithoutCursorCorruption(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.open = true
	model.emojiPicker.category = 8
	model.emojiPicker.categoryCursor = 15
	want := model.emojiPicker.selectedEmoji()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 20}, {40, 8}, {20, 6}, {80, 24}} {
		clampView(&model, size[0], size[1])
		if model.emojiPicker.category != 8 || model.emojiPicker.categoryCursor != 15 ||
			model.emojiPicker.selectedEmoji() != want {
			t.Fatalf("%dx%d picker=%+v selected=%q", size[0], size[1], model.emojiPicker, model.emojiPicker.selectedEmoji())
		}
	}
}

func TestRunPreservesOpenEmojiPickerAcrossResize(t *testing.T) {
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() { result <- runWithPreferences(context.Background(), screen, "") }()

	<-screen.shown
	injectKeyAndWait(screen, tcell.KeyEnter, 0)
	injectKeyAndWait(screen, tcell.KeyCtrlE, 0)
	injectKeyAndWait(screen, tcell.KeyRight, 0)
	resizeObservedScreen(t, screen, 60, 20)
	if text := screenText(screen); !strings.Contains(text, "Emoji") || !strings.Contains(text, "Esc close") {
		t.Fatalf("narrow resize lost picker:\n%s", text)
	}
	resizeObservedScreen(t, screen, 30, 6)
	if text := screenText(screen); !strings.Contains(text, "terminal too small") || !strings.Contains(text, "Esc close") {
		t.Fatalf("short resize lost picker:\n%s", text)
	}
	resizeObservedScreen(t, screen, 100, 30)
	if text := screenText(screen); !strings.Contains(text, "arrows choose") {
		t.Fatalf("wide resize lost picker:\n%s", text)
	}
	injectKeyAndWait(screen, tcell.KeyEscape, 0)
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("Run=%v", err)
	}
}

func screenHasAttributes(screen tcell.SimulationScreen, want tcell.AttrMask) bool {
	cells, _, _ := screen.GetContents()
	for _, cell := range cells {
		_, _, attributes := cell.Style.Decompose()
		if attributes&want == want {
			return true
		}
	}
	return false
}

func screenContainsRuneSequence(screen tcell.SimulationScreen, want string) bool {
	cells, _, _ := screen.GetContents()
	for _, cell := range cells {
		if string(cell.Runes) == want {
			return true
		}
	}
	return false
}
