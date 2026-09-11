package tui

import "errors"

// ErrGroupReplyUnavailable is a safe application-to-view rejection reason.
var ErrGroupReplyUnavailable = errors.New("group replies not available yet")

// SendResult completes one admitted request, not a chat-data mutation. Only
// LiveEvents can add the resulting message. Uncertain disables Enter-to-retry.
type SendResult struct{ Failed, Uncertain, Media, Reaction, StickerRejected bool }

func applySendResult(model *viewModel, result SendResult) bool {
	if !model.sendPending {
		return false
	}
	model.sendPending = false
	if result.Failed {
		model.sendUncertain = result.Uncertain
		model.sendStatus = "Send failed; draft kept"
		if result.Media {
			model.sendStatus = "Send failed; path kept"
		}
		if result.Reaction {
			model.sendStatus = "Reaction failed; target kept"
		}
		if result.StickerRejected {
			model.sendStatus = "Sticker rejected; use a 512x512 WebP within the sticker size limit; path kept"
		}
		if result.Uncertain {
			model.sendStatus = "Delivery unknown; check recipient. Esc discard"
		}
		return true
	}
	model.sendStatus = ""
	if result.Reaction {
		model.reactionTarget = reactionTargetState{}
		model.replySelect = replySelectionState{}
		return true
	}
	clearSentDraft(model)
	if result.Media {
		model.mode = modeNavigate
	}
	return true
}

func clearSentDraft(model *viewModel) {
	model.composer.clear()
	model.replyTarget = replyTarget{}
	model.replySelect = replySelectionState{}
	model.chatView.resetScroll()
}
