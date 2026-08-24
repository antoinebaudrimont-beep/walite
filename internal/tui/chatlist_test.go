package tui

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

func TestDrawUnreadChatLabelAndStyle(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	draw(screen, &model)
	screen.Show()

	if text := screenText(screen); !strings.Contains(text, "Project Room (3)") {
		t.Fatalf("unread label missing:\n%s", text)
	}
	if attributesAt(screen, 4, 4)&tcell.AttrBold == 0 {
		t.Fatal("unread chat title is not bold")
	}
}

func TestDrawSelectedUnreadChatKeepsBoldAndReverse(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.selectedChat = 1
	draw(screen, &model)
	screen.Show()

	if text := screenText(screen); !strings.Contains(text, "> Project Room (3)") {
		t.Fatalf("selected unread label missing:\n%s", text)
	}
	attributes := attributesAt(screen, 2, 4)
	if attributes&tcell.AttrBold == 0 || attributes&tcell.AttrReverse == 0 {
		t.Fatalf("selected unread attributes=%v", attributes)
	}
}

func TestSelectionClearsOnlyNewChatUnreadCount(t *testing.T) {
	model := defaultDemoView()
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), 100, 30)
	if !changed || exit || model.selectedChat != 1 || model.chats[1].unreadCount != 0 {
		t.Fatalf("changed=%t exit=%t selected=%d unread=%d", changed, exit, model.selectedChat, model.chats[1].unreadCount)
	}
}

func TestBoundarySelectionPreservesUnreadCounts(t *testing.T) {
	model := defaultDemoView()
	before := unreadCounts(model)
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), 100, 30); changed {
		t.Fatal("top boundary requested redraw")
	}
	if after := unreadCounts(model); !reflect.DeepEqual(after, before) {
		t.Fatalf("top boundary unread=%v want %v", after, before)
	}

	model.selectedChat = model.chatCount - 1
	before = unreadCounts(model)
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), 100, 30); changed {
		t.Fatal("bottom boundary requested redraw")
	}
	if after := unreadCounts(model); !reflect.DeepEqual(after, before) {
		t.Fatalf("bottom boundary unread=%v want %v", after, before)
	}
}

func TestChatRowTruncationReservesUnreadSuffix(t *testing.T) {
	const width = 22
	got := formatChatRow("Very long person name", 4, true, width)
	if got != "> Very long perso… (4)" {
		t.Fatalf("row=%q", got)
	}
	if measured := uniseg.StringWidth(got); measured > width {
		t.Fatalf("width=%d limit=%d row=%q", measured, width, got)
	}
}

func TestChatRowUnicodeTruncationPreservesGraphemes(t *testing.T) {
	const width = 16
	got := formatChatRow("Café 🙂 Project Room", 4, false, width)
	if got != "  Café 🙂 P… (4)" {
		t.Fatalf("row=%q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8 row=%q", got)
	}
	if measured := uniseg.StringWidth(got); measured > width {
		t.Fatalf("width=%d limit=%d row=%q", measured, width, got)
	}
}

func TestChatRowLargeUnreadCountFits(t *testing.T) {
	const width = 18
	got := formatChatRow("Project Room", 123, false, width)
	if !strings.HasSuffix(got, " (123)") {
		t.Fatalf("large unread suffix missing row=%q", got)
	}
	if measured := uniseg.StringWidth(got); measured > width {
		t.Fatalf("width=%d limit=%d row=%q", measured, width, got)
	}
}

func attributesAt(screen tcell.SimulationScreen, x, y int) tcell.AttrMask {
	_, _, style, _ := screen.GetContent(x, y)
	_, _, attributes := style.Decompose()
	return attributes
}

func unreadCounts(model viewModel) [maxChats]uint16 {
	var counts [maxChats]uint16
	for index := range model.chats {
		counts[index] = model.chats[index].unreadCount
	}
	return counts
}
