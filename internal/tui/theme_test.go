package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestThemeCycleIsExactAndDeterministic(t *testing.T) {
	want := []string{ThemeTerminal, ThemeDark, ThemeLight, ThemeHighContrast, ThemeTerminal}
	got := []string{ThemeTerminal}
	for len(got) < len(want) {
		got = append(got, nextTheme(got[len(got)-1]))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("theme cycle=%v want=%v", got, want)
	}
	if labels := []string{themeLabel(ThemeTerminal), themeLabel(ThemeDark), themeLabel(ThemeLight), themeLabel(ThemeHighContrast)}; !reflect.DeepEqual(labels, []string{"Terminal", "Dark", "Light", "High contrast"}) {
		t.Fatalf("theme labels=%v", labels)
	}
}

func TestThemeSemanticStylesAreLegibleAndSelectionIsDistinct(t *testing.T) {
	for _, theme := range []string{ThemeTerminal, ThemeDark, ThemeLight, ThemeHighContrast} {
		t.Run(theme, func(t *testing.T) {
			styles := stylesFor(theme)
			if styles.selectedChat == styles.normal || styles.unreadChat == styles.normal || styles.popupSelected == styles.popup || styles.dateSeparator == styles.normal || styles.unreadSeparator == styles.normal || styles.groupSender == styles.normal || styles.warning == styles.normal {
				t.Fatalf("semantic role collapsed into normal style: %+v", styles)
			}
			if theme != ThemeTerminal {
				for name, style := range map[string]tcell.Style{
					"normal": styles.normal, "selected": styles.selectedChat, "unread": styles.unreadChat,
					"popup focus": styles.popupSelected, "reply": styles.replyQuote, "sender": styles.groupSender,
					"status": styles.status, "warning": styles.warning,
				} {
					foreground, background, _ := style.Decompose()
					if foreground != tcell.ColorDefault && background != tcell.ColorDefault && foreground == background {
						t.Fatalf("%s foreground and background are both %v", name, foreground)
					}
				}
			}
		})
	}
}

func TestThemesRenderSemanticRolesAndResponsivePopups(t *testing.T) {
	at := time.Date(2100, 9, 6, 10, 0, 0, 0, time.Local)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "group@g.us", Title: "Family Group", IsGroup: true, UnreadCount: 2, ActivityTime: at, Messages: []InitialMessage{
			{ID: "incoming", SentAt: at.Add(-2 * time.Minute), Text: "INCOMINGMARK", BodyRetained: true, SenderID: "sender@lid", IsGroup: true},
			{ID: "outgoing", SentAt: at.Add(-time.Minute), FromMe: true, Text: "OUTGOINGMARK", BodyRetained: true},
			{ID: "reply", SentAt: at, Text: "REPLYMARK", BodyRetained: true, ReplyToID: "incoming", ReplyToText: "quoted text", SenderID: "sender@lid", IsGroup: true},
		}},
		{ID: "unread@lid", Title: "Unread Chat", UnreadCount: 4, ActivityTime: at.Add(-time.Hour)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{ThemeTerminal, ThemeDark, ThemeLight, ThemeHighContrast} {
		t.Run(theme, func(t *testing.T) {
			model := viewModel{chats: state, options: Options{Theme: theme, ShowTimestamps: true, SendReadReceipts: true}}
			applyDisplayMetadata(&model, DisplayMetadata{ID: "sender@lid", Name: "SENDERLABEL", Quality: 4})
			model.chatView.unreadBoundary = unreadBoundaryForChat(&model.chats.chats[0])
			screen := initializedSimulationScreen(t, 80, 24)
			draw(screen, &model)
			screen.Show()
			text := screenText(screen)
			for _, want := range []string{"Family Group", "(2)", "Unread Chat", "(4)", "SENDERLABEL", "INCOMINGMARK", "OUTGOINGMARK", "REPLYMARK", "quoted text"} {
				if !strings.Contains(text, want) {
					t.Fatalf("theme %s missing %q:\n%s", theme, want, text)
				}
			}
			styles := stylesFor(theme)
			_, _, selectedStyle, _ := screen.GetContent(1, 3)
			if selectedStyle != styles.selectedChat.Bold(true) {
				t.Fatalf("selected chat style=%v want=%v", selectedStyle, styles.selectedChat.Bold(true))
			}
			if got, ok := firstTextStyle(screen, "SENDERLABEL"); !ok || got != styles.groupSender {
				t.Fatalf("group sender style=%v found=%t", got, ok)
			}
			if got, ok := firstTextStyle(screen, "OUTGOINGMARK"); !ok || got != styles.outgoing {
				t.Fatalf("outgoing style=%v found=%t", got, ok)
			}

			model.settingsOpen = true
			openSettings(&model)
			model.emojiPicker.prepareOpen()
			model.quitConfirm = true
			draw(screen, &model)
			screen.Show()
			if text := screenText(screen); !strings.Contains(text, "Quit walite?") {
				t.Fatalf("topmost confirmation missing over popups:\n%s", text)
			}
			left, top := (80-34)/2, (24-5)/2
			_, _, borderStyle, _ := screen.GetContent(left, top)
			if borderStyle != styles.border {
				t.Fatalf("confirmation border style=%v want=%v", borderStyle, styles.border)
			}

			model.quitConfirm = false
			for _, size := range [][2]int{{40, 12}, {18, 6}, {8, 3}, {80, 24}} {
				screen.SetSize(size[0], size[1])
				clampView(&model, size[0], size[1])
				draw(screen, &model)
				screen.Show()
				fresh := initializedSimulationScreen(t, size[0], size[1])
				draw(fresh, &model)
				fresh.Show()
				got, _, _ := screen.GetContents()
				want, _, _ := fresh.GetContents()
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("theme %s size %v left stale popup cells", theme, size)
				}
			}
		})
	}
}

func TestSavingThemeAppliesLiveWithoutChangingInteractionState(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.composer.insertText("draft 👋")
	model.composer.moveLeft()
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[0].messages[0].id}
	model.chatView.scrollOffset = 2
	model.emojiPicker.prepareOpen()
	beforeChats := *model.chats
	beforeComposer, beforeReply, beforeView, beforeEmoji := model.composer, model.replyTarget, model.chatView, model.emojiPicker
	screen := initializedSimulationScreen(t, 100, 30)
	draw(screen, &model)
	screen.Show()
	openSettings(&model)
	settingsKey(t, &model, tcell.KeyEnter, 0)
	if model.settings.draft.Theme != ThemeDark || model.options.Theme != ThemeTerminal {
		t.Fatalf("draft=%q active=%q", model.settings.draft.Theme, model.options.Theme)
	}
	model.settings.selected = settingsSaveRow
	settingsKey(t, &model, tcell.KeyEnter, 0)
	requestSettingsSave(&model, func(value Options) bool { return value.Theme == ThemeDark })
	finishSettingsSave(&model, nil)
	if model.options.Theme != ThemeDark || model.settingsOpen {
		t.Fatalf("saved theme=%q popup=%t", model.options.Theme, model.settingsOpen)
	}
	if !reflect.DeepEqual(*model.chats, beforeChats) || model.composer != beforeComposer || model.replyTarget != beforeReply || model.chatView != beforeView || model.emojiPicker != beforeEmoji || model.mode != modeCompose {
		t.Fatal("theme save changed chat, draft, cursor, reply, scroll, or popup state")
	}
	draw(screen, &model)
	screen.Show()
	fresh := initializedSimulationScreen(t, 100, 30)
	draw(fresh, &model)
	fresh.Show()
	gotCells, _, _ := screen.GetContents()
	wantCells, _, _ := fresh.GetContents()
	if !reflect.DeepEqual(gotCells, wantCells) {
		for index := range gotCells {
			if !reflect.DeepEqual(gotCells[index], wantCells[index]) {
				t.Logf("first stale cell index=%d got=%+v want=%+v", index, gotCells[index], wantCells[index])
				break
			}
		}
		t.Fatal("live theme switch left stale cells")
	}
	foreground, background, _ := stylesFor(ThemeDark).border.Decompose()
	_, _, applied, _ := screen.GetContent(0, 0)
	gotForeground, gotBackground, _ := applied.Decompose()
	if gotForeground != foreground || gotBackground != background {
		t.Fatalf("live frame base=%v/%v want=%v/%v", gotForeground, gotBackground, foreground, background)
	}
}

func firstTextStyle(screen tcell.SimulationScreen, text string) (tcell.Style, bool) {
	width, height := screen.Size()
	runes := []rune(text)
	for y := 0; y < height; y++ {
		for x := 0; x+len(runes) <= width; x++ {
			matches := true
			for index, want := range runes {
				got, _, _, _ := screen.GetContent(x+index, y)
				if got != want {
					matches = false
					break
				}
			}
			if matches {
				_, _, style, _ := screen.GetContent(x, y)
				return style, true
			}
		}
	}
	return tcell.StyleDefault, false
}
