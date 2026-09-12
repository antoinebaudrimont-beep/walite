package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestNarrowLayoutTabTogglesSinglePaneWithoutChangingChatState(t *testing.T) {
	model := defaultDemoView()
	wantChats := *model.chats
	wantSelected := model.chats.selectedIndex()
	screen := initializedSimulationScreen(t, narrowWidth-1, 20)

	draw(screen, &model)
	screen.Show()
	conversation := screenText(screen)
	if !strings.Contains(conversation, "Demo synthetic message 18") || strings.Contains(conversation, "Project Room") {
		t.Fatalf("initial narrow conversation pane is wrong:\n%s", conversation)
	}

	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone), narrowWidth-1, 20)
	if !changed || exit || model.narrowPane != narrowPaneChats {
		t.Fatalf("Tab changed=%t exit=%t pane=%d", changed, exit, model.narrowPane)
	}
	draw(screen, &model)
	screen.Show()
	chatList := screenText(screen)
	for _, want := range []string{"walite — Chats", "> Demo Chat", "Project Room", "Tab switch pane"} {
		if !strings.Contains(chatList, want) {
			t.Fatalf("narrow chat list missing %q:\n%s", want, chatList)
		}
	}
	if strings.Contains(chatList, "Demo synthetic message 18") {
		t.Fatalf("conversation remained visible behind narrow chat list:\n%s", chatList)
	}

	changed, exit = handleKey(&model, tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone), narrowWidth-1, 20)
	if !changed || exit || model.narrowPane != narrowPaneConversation {
		t.Fatalf("second Tab changed=%t exit=%t pane=%d", changed, exit, model.narrowPane)
	}
	if model.chats.selectedIndex() != wantSelected || !reflect.DeepEqual(*model.chats, wantChats) {
		t.Fatal("pane switching changed the selected chat or message working set")
	}
}

func TestNarrowPaneChoiceSurvivesWideResizeAndWideLayoutKeepsBothPanes(t *testing.T) {
	model := defaultDemoView()
	wantChats := *model.chats
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone), narrowWidth-1, 20); !changed {
		t.Fatal("failed to select narrow chat-list pane")
	}

	wide := initializedSimulationScreen(t, narrowWidth, 20)
	clampView(&model, narrowWidth, 20)
	draw(wide, &model)
	wide.Show()
	wideText := screenText(wide)
	if !strings.Contains(wideText, "Project Room") || !strings.Contains(wideText, "Demo synthetic message 18") || !strings.Contains(wideText, "┬") {
		t.Fatalf("wide layout did not restore both panes:\n%s", wideText)
	}

	narrow := initializedSimulationScreen(t, narrowWidth-1, 20)
	clampView(&model, narrowWidth-1, 20)
	draw(narrow, &model)
	narrow.Show()
	narrowText := screenText(narrow)
	if !strings.Contains(narrowText, "Project Room") || strings.Contains(narrowText, "Demo synthetic message 18") || strings.Contains(narrowText, "┬") {
		t.Fatalf("narrow layout did not restore the chosen chat-list pane:\n%s", narrowText)
	}
	if !reflect.DeepEqual(*model.chats, wantChats) {
		t.Fatal("wide/narrow resize changed the message working set")
	}
}

func TestTabInComposerDoesNotSwitchNarrowPaneOrChangeDraft(t *testing.T) {
	model := defaultDemoView()
	model.narrowPane = narrowPaneChats
	model.mode = modeCompose
	if !model.composer.insertText("draft stays here") {
		t.Fatal("draft setup failed")
	}

	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyTAB, 0, tcell.ModNone), narrowWidth-1, 20)
	if changed || exit || model.narrowPane != narrowPaneChats || model.mode != modeCompose || model.composer.text() != "draft stays here" {
		t.Fatalf("Tab changed=%t exit=%t pane=%d mode=%d draft=%q", changed, exit, model.narrowPane, model.mode, model.composer.text())
	}

	screen := initializedSimulationScreen(t, narrowWidth-1, 20)
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "draft stays here") || strings.Contains(text, "walite — Chats") {
		t.Fatalf("composer was not kept visible in narrow mode:\n%s", text)
	}
}
