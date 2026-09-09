package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestVisibleMediaPreviewAndSaveUseStableNewestVisibleIdentity(t *testing.T) {
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{ID: "chat", Title: "Chat", ActivityTime: time.Unix(3, 0), Messages: []InitialMessage{
		{ID: "image-old", SentAt: time.Unix(1, 0), MediaKind: mediaImage},
		{ID: "text", SentAt: time.Unix(2, 0), Text: "text", BodyRetained: true},
		{ID: "document-new", SentAt: time.Unix(3, 0), MediaKind: mediaDocument, MediaName: "report.pdf"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	var requests []MediaRequest
	model := viewModel{chats: state, options: DefaultOptions(), media: func(request MediaRequest) bool { requests = append(requests, request); return true }}
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModNone), 100, 24)
	if !changed || exit || len(requests) != 1 || requests[0].ChatID != "chat" || requests[0].MessageID != "document-new" || requests[0].Action != MediaSave {
		t.Fatalf("changed=%t exit=%t requests=%+v", changed, exit, requests)
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), 100, 24); !changed || len(requests) != 1 || model.sendStatus == "" {
		t.Fatal("non-image preview was not rejected locally")
	}
	model.replySelect = replySelectionState{valid: true, index: 0}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), 100, 24); !changed || len(requests) != 2 || requests[1].MessageID != "image-old" || requests[1].Action != MediaPreview {
		t.Fatalf("focused request=%+v", requests)
	}
}

func TestMediaPreviewClosesOnEscapeNavigationAndResize(t *testing.T) {
	state, _ := chatStateFromInitial(InitialState{Chats: []InitialChat{{ID: "chat", Title: "Chat", ActivityTime: time.Unix(1, 0), Messages: []InitialMessage{{ID: "image", SentAt: time.Unix(1, 0), MediaKind: mediaImage}}}}})
	closed := 0
	model := viewModel{chats: state, options: DefaultOptions(), closeMedia: func() { closed++ }, mediaTarget: mediaTargetState{active: true, previewing: true, chatID: "chat", messageID: "image"}}
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 24)
	if !changed || exit || closed != 1 || model.mediaTarget.active {
		t.Fatalf("changed=%t exit=%t closed=%d", changed, exit, closed)
	}
	model.mediaTarget = mediaTargetState{active: true, previewing: true, chatID: "chat", messageID: "image"}
	handleKey(&model, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), 100, 24)
	if closed != 2 || model.mediaTarget.active {
		t.Fatalf("navigation close=%d state=%+v", closed, model.mediaTarget)
	}
	x, y, width, height := mediaPreviewRectangle(100, 24)
	if x <= 0 || y != 3 || width <= 0 || height <= 0 || x+width > 100 || y+height > 24 {
		t.Fatalf("rectangle=%d,%d %dx%d", x, y, width, height)
	}
}

func TestStaleMediaResultCannotReopenPreview(t *testing.T) {
	model := viewModel{mediaTarget: mediaTargetState{active: true, chatID: "chat", messageID: "new"}}
	if applyMediaResult(&model, MediaResult{ChatID: "chat", MessageID: "old", Previewing: true, Status: "stale"}) || model.sendStatus != "" {
		t.Fatal("stale result mutated UI")
	}
	if !applyMediaResult(&model, MediaResult{ChatID: "chat", MessageID: "new", Previewing: true, Status: "ready"}) || !model.mediaTarget.previewing {
		t.Fatal("matching preview result rejected")
	}
}

func TestPreviewKeyTogglesExistingOverlayClosed(t *testing.T) {
	state, _ := chatStateFromInitial(InitialState{Chats: []InitialChat{{ID: "chat", Title: "Chat", ActivityTime: time.Unix(1, 0), Messages: []InitialMessage{{ID: "image", SentAt: time.Unix(1, 0), MediaKind: mediaImage}}}}})
	closed, admitted := 0, 0
	model := viewModel{chats: state, options: DefaultOptions(), media: func(MediaRequest) bool { admitted++; return true }, closeMedia: func() { closed++ }, mediaTarget: mediaTargetState{active: true, previewing: true, chatID: "chat", messageID: "image"}}
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), 100, 24)
	if !changed || exit || closed != 1 || admitted != 0 || model.mediaTarget.active {
		t.Fatalf("changed=%t exit=%t closed=%d admitted=%d", changed, exit, closed, admitted)
	}
}

func TestStickerPreviewUsesExistingMediaRequestPath(t *testing.T) {
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{ID: "chat", Title: "Chat", ActivityTime: time.Unix(1, 0), Messages: []InitialMessage{{ID: "sticker", SentAt: time.Unix(1, 0), MediaKind: mediaSticker, MediaMIME: "image/webp"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var request MediaRequest
	model := viewModel{chats: state, options: DefaultOptions(), media: func(value MediaRequest) bool { request = value; return true }}
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), 100, 24)
	if !changed || exit || request.Kind != mediaSticker || request.Action != MediaPreview || request.MessageID != "sticker" {
		t.Fatalf("changed=%t exit=%t request=%+v", changed, exit, request)
	}
}
