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
	for _, want := range []string{"Emoji", "Recent", "arrows", "Tab category", "Enter", "Esc close", "Demo Chat"} {
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
		if text := screenText(screen); !strings.Contains(text, "Smileys") || !strings.Contains(text, "Tab category") || !strings.Contains(text, "Esc") {
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

func TestEmojiPickerRepeatedOpenCloseFullyRedraws(t *testing.T) {
	screen := initializedSimulationScreen(t, 80, 24)
	model := defaultDemoView()
	model.mode = modeCompose
	draw(screen, &model)
	screen.Show()
	baseFrame := screenText(screen)
	for cycle := 0; cycle < 4; cycle++ {
		model.emojiPicker.prepareOpen()
		draw(screen, &model)
		screen.Show()
		if text := screenText(screen); !strings.Contains(text, "Emoji") || !screenContainsRuneSequence(screen, "😀") {
			t.Fatalf("cycle %d open picker incomplete:\n%s", cycle, text)
		}
		model.emojiPicker.close()
		draw(screen, &model)
		screen.Show()
		if text := screenText(screen); text != baseFrame || strings.Contains(text, " Emoji ") || screenContainsRuneSequence(screen, "😀") {
			t.Fatalf("cycle %d close left picker cells:\n%s", cycle, text)
		}
	}
}

func TestEmojiCategorySizeChangesClearOnlyTheGrid(t *testing.T) {
	originalPeople := emojiCategories[1].values
	emojiCategories[1].values = originalPeople[:5]
	defer func() { emojiCategories[1].values = originalPeople }()

	screen := initializedSimulationScreen(t, 80, 24)
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.remember("🙂")
	model.emojiPicker.prepareOpen()
	model.emojiPicker.focus = emojiFocusCategory
	draw(screen, &model)
	screen.Show()
	layout := emojiLayout(80, 24)
	left, top, right, bottom := emojiGridRegion(layout)
	if !screenRegionContainsRuneSequence(screen, "😡", left, top, right, bottom) {
		t.Fatal("large Smileys grid missing its final emoji")
	}

	for _, step := range []struct {
		category int
		delta    int
		present  string
		absent   string
	}{{1, 1, "👍", "😡"}, {2, 1, "🐶", "👍"}, {0, -2, "😀", "🐶"}} {
		if !model.emojiPicker.moveCategory(step.delta) {
			t.Fatal("category switch failed")
		}
		if model.emojiPicker.category != step.category || model.emojiPicker.categoryCursor != 0 || model.emojiPicker.focus != emojiFocusCategory {
			t.Fatalf("picker state=%+v", model.emojiPicker)
		}
		draw(screen, &model)
		screen.Show()
		if !screenRegionContainsRuneSequence(screen, step.present, left, top, right, bottom) ||
			screenRegionContainsRuneSequence(screen, step.absent, left, top, right, bottom) {
			t.Fatalf("category %d grid contains stale or missing emoji:\n%s", step.category, screenText(screen))
		}
		if step.category == 1 && (!screenContainsRuneSequence(screen, "🙂") ||
			screenRegionContainsRuneSequence(screen, "🙂", left, top, right, bottom)) {
			t.Fatal("Recent duplicate was confused with stale Smileys grid content")
		}
	}
	if !screenContainsRuneSequence(screen, "🙂") {
		t.Fatal("Recent duplicate disappeared while checking category grid")
	}
}

func TestEmojiPickerLargeSmallLargeRebuildsCurrentGeometry(t *testing.T) {
	screen := initializedSimulationScreen(t, 120, 40)
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.prepareOpen()
	model.emojiPicker.category = 8
	model.emojiPicker.categoryCursor = 15
	want := model.emojiPicker.selectedEmoji()

	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 20}, {40, 8}, {120, 40}} {
		screen.SetSize(size[0], size[1])
		clampView(&model, size[0], size[1])
		draw(screen, &model)
		screen.Show()
		layout := emojiLayout(size[0], size[1])
		if model.emojiPicker.selectedEmoji() != want || model.emojiPicker.categoryCursor >= len(emojiCategories[model.emojiPicker.category].values) {
			t.Fatalf("%dx%d invalid picker=%+v selected=%q", size[0], size[1], model.emojiPicker, model.emojiPicker.selectedEmoji())
		}
		for _, corner := range []struct {
			x, y int
			want rune
		}{{layout.left, layout.top, '┌'}, {layout.right, layout.top, '┐'}, {layout.left, layout.bottom, '└'}, {layout.right, layout.bottom, '┘'}} {
			main, _, _, _ := screen.GetContent(corner.x, corner.y)
			if main != corner.want {
				t.Fatalf("%dx%d corner %d,%d=%q want=%q", size[0], size[1], corner.x, corner.y, main, corner.want)
			}
		}
	}
}

func TestEmojiCategorySwitchThenResizeDoesNotLeaveStaleCells(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.mode = modeCompose
	model.emojiPicker.prepareOpen()
	draw(screen, &model)
	screen.Show()
	if !screenContainsRuneSequence(screen, "🙂") {
		t.Fatal("initial category missing")
	}

	if !model.emojiPicker.moveCategory(1) {
		t.Fatal("category switch failed")
	}
	screen.SetSize(60, 20)
	clampView(&model, 60, 20)
	draw(screen, &model)
	screen.Show()
	if screenContainsRuneSequence(screen, "🙂") || !screenContainsRuneSequence(screen, "👍") {
		t.Fatalf("medium redraw contains stale or missing category:\n%s", screenText(screen))
	}

	if !model.emojiPicker.moveCategory(1) {
		t.Fatal("second category switch failed")
	}
	screen.SetSize(40, 8)
	clampView(&model, 40, 8)
	draw(screen, &model)
	screen.Show()
	if screenContainsRuneSequence(screen, "👍") || !screenContainsRuneSequence(screen, "🐶") {
		t.Fatalf("narrow redraw contains stale or missing category:\n%s", screenText(screen))
	}
}

func TestEmojiGridClearsEmptyAndSmallCategories(t *testing.T) {
	screen := initializedSimulationScreen(t, 30, 8)
	for y := 1; y < 4; y++ {
		for x := 2; x < 18; x++ {
			screen.SetContent(x, y, 'X', nil, tcell.StyleDefault)
		}
	}
	drawEmojiCategoryGrid(screen, nil, 99, true, 2, 1, 18, 3, 4)
	for y := 1; y < 4; y++ {
		for x := 2; x < 18; x++ {
			main, _, _, _ := screen.GetContent(x, y)
			if main != ' ' {
				t.Fatalf("empty grid left %q at %d,%d", main, x, y)
			}
		}
	}

	drawEmojiCategoryGrid(screen, []string{"❤️"}, 99, true, 2, 1, 18, 3, 4)
	screen.Show()
	if !screenContainsRuneSequence(screen, "❤️") {
		t.Fatal("small category did not clamp and render its only emoji")
	}
}

func TestEmojiCellUsesVisibleFallbackInsteadOfDroppingUnsupportedValue(t *testing.T) {
	screen := initializedSimulationScreen(t, 20, 4)
	drawEmojiCell(screen, "👨‍👩‍👧‍👦", false, 2, 1, 18)
	drawEmojiCell(screen, "🙂🙂", true, 2, 1, 18)
	screen.Show()
	if !screenContainsRuneSequence(screen, emojiFallback) {
		t.Fatal("unsupported sequence disappeared instead of using fallback")
	}
	if screenContainsRuneSequence(screen, "👨‍👩‍👧‍👦") {
		t.Fatal("replacement left the previous grapheme in the logical cell")
	}
	for x := 2; x < 2+emojiCellWidth; x++ {
		attributes := attributesAt(screen, x, 1)
		if attributes&tcell.AttrBold == 0 || attributes&tcell.AttrReverse == 0 {
			t.Fatalf("logical cell column %d attributes=%v", x, attributes)
		}
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
	if text := screenText(screen); !strings.Contains(text, "Tab category") {
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

func emojiGridRegion(layout emojiPickerLayout) (left, top, right, bottom int) {
	return layout.left + 2, layout.top + 3, layout.right - 1, layout.top + 3 + layout.gridRows
}

func screenRegionContainsRuneSequence(screen tcell.SimulationScreen, want string, left, top, right, bottom int) bool {
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			main, combining, _, _ := screen.GetContent(x, y)
			runes := append([]rune{main}, combining...)
			if string(runes) == want {
				return true
			}
		}
	}
	return false
}
