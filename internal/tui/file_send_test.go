package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestFileModeSendsExactPathAndPreservesItOnFailure(t *testing.T) {
	model := defaultDemoView()
	model.asyncSend = true
	var requests []SendRequest
	model.send = func(request SendRequest) error {
		requests = append(requests, request)
		return nil
	}
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModShift), 100, 30)
	if !changed || exit || model.mode != modeFile || model.composer.length != 0 {
		t.Fatalf("file mode changed=%t exit=%t mode=%d", changed, exit, model.mode)
	}
	path := "/tmp/Café $(never-a-shell) report.pdf"
	if !model.composer.insertText(path) {
		t.Fatal("path setup failed")
	}
	if changed, exit = handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 30); !changed || exit || !model.sendPending {
		t.Fatalf("submit changed=%t exit=%t pending=%t", changed, exit, model.sendPending)
	}
	chat, _ := model.chats.selectedChat()
	if len(requests) != 1 || requests[0] != (SendRequest{ChatID: chat.id, FilePath: path}) {
		t.Fatalf("requests=%+v", requests)
	}
	if !applySendResult(&model, SendResult{Failed: true, Media: true}) || model.mode != modeFile || model.composer.text() != path ||
		!strings.Contains(model.sendStatus, "path kept") {
		t.Fatalf("failed state mode=%d path=%q status=%q", model.mode, model.composer.text(), model.sendStatus)
	}
	if !submitOutgoingFile(&model) || !model.sendPending || len(requests) != 2 {
		t.Fatal("explicit retry was not admitted once")
	}
	if !applySendResult(&model, SendResult{Media: true}) || model.mode != modeNavigate || model.composer.length != 0 || model.sendPending {
		t.Fatalf("success mode=%d path=%q pending=%t", model.mode, model.composer.text(), model.sendPending)
	}
}

func TestFileModeEscapeCancelsWithoutSending(t *testing.T) {
	model := defaultDemoView()
	calls := 0
	model.send = func(SendRequest) error { calls++; return nil }
	handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModShift), 100, 30)
	model.composer.insertText("/tmp/not-sent")
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 30)
	if !changed || exit || model.mode != modeNavigate || model.composer.length != 0 || calls != 0 {
		t.Fatalf("cancel changed=%t exit=%t mode=%d path=%q calls=%d", changed, exit, model.mode, model.composer.text(), calls)
	}
}

func TestFileModeRendersPathFooterAndCompactCancel(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeFile
	model.composer.insertText("/tmp/image.png")
	for _, size := range [][2]int{{100, 30}, {60, 20}, {40, 6}} {
		screen := initializedSimulationScreen(t, size[0], size[1])
		draw(screen, &model)
		screen.Show()
		text := screenText(screen)
		if size[1] < shortHeight {
			if !strings.Contains(text, "Esc cancel") || strings.Contains(text, "Esc quit") {
				t.Fatalf("compact file mode:\n%s", text)
			}
			continue
		}
		if !strings.Contains(text, "> /tmp/image.png") || !strings.Contains(text, "Enter send") || !strings.Contains(text, "Esc cancel") {
			t.Fatalf("file mode %dx%d:\n%s", size[0], size[1], text)
		}
	}
}

func TestNavigationFooterAdvertisesFileSend(t *testing.T) {
	model := defaultDemoView()
	if footer := navigationFooter(&model, false); !strings.Contains(footer, "F file") {
		t.Fatalf("footer=%q", footer)
	}
}

func TestCommittedOutgoingMediaRendersThroughLivePresentation(t *testing.T) {
	model := defaultDemoView()
	chat, _ := model.chats.selectedChat()
	at := time.Date(2200, 1, 2, 11, 0, 0, 0, time.UTC)
	if !applyLiveMessage(&model, LiveMessage{
		ChatID: chat.id, MessageID: "outgoing-document", SentAt: at, FromMe: true, BodyRetained: true,
		MediaKind: mediaDocument, MediaName: "report Café.pdf", MediaMIME: "application/pdf", ActivityTime: at,
	}) {
		t.Fatal("committed outgoing media rejected")
	}
	screen := initializedSimulationScreen(t, 100, 30)
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "[Document: report Café.pdf]") {
		t.Fatalf("outgoing placeholder missing:\n%s", text)
	}
}
