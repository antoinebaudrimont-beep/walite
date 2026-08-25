package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestUnicodeAcceptancePreservesComposerAndUsesVisiblePickerFallback(t *testing.T) {
	values := []string{"ASCII", "Café", "界", "❤️", "👍🏽", "👨‍👩‍👧‍👦", "🇦🇹"}
	for _, value := range values {
		var composer composerState
		if !composer.insertText(value) || composer.text() != value {
			t.Fatalf("composer did not preserve %q", value)
		}

		screen := initializedSimulationScreen(t, 20, 4)
		drawEmojiCell(screen, value, true, 2, 1, 18)
		screen.Show()
		if !screenContainsRuneSequence(screen, value) && !screenContainsRuneSequence(screen, emojiFallback) {
			t.Fatalf("picker silently dropped %q", value)
		}
		if composer.text() != value {
			t.Fatalf("picker fallback changed composer value %q", value)
		}
		for x := 2; x < 2+emojiCellWidth; x++ {
			attributes := attributesAt(screen, x, 1)
			if attributes&tcell.AttrReverse == 0 {
				t.Fatalf("selected cell for %q missing reverse style at x=%d", value, x)
			}
		}
	}
}

func TestRequiredTerminalSizeMatrixPreservesState(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.composer.insertText("Café 界 ❤️")
	model.emojiPicker.prepareOpen()
	model.chatView.scrollOffset = 2
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 20}, {40, 8}, {30, 6}} {
		screen := initializedSimulationScreen(t, size[0], size[1])
		clampView(&model, size[0], size[1])
		draw(screen, &model)
		screen.Show()
		if model.mode != modeCompose || model.composer.text() != "Café 界 ❤️" || !model.emojiPicker.open || model.chatView.scrollOffset < 0 {
			t.Fatalf("%dx%d state changed: mode=%d draft=%q picker=%t offset=%d", size[0], size[1], model.mode, model.composer.text(), model.emojiPicker.open, model.chatView.scrollOffset)
		}
	}
}
