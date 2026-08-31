package tui

import "errors"

// ErrGroupReplyUnavailable is a safe application-to-view rejection reason.
var ErrGroupReplyUnavailable = errors.New("group replies not available yet")

// SendResult completes one admitted request, not a chat-data mutation. Only
// LiveEvents can add the resulting message. Uncertain disables Enter-to-retry.
type SendResult struct{ Failed, Uncertain bool }

func applySendResult(model *viewModel, result SendResult) bool {
	if !model.sendPending {
		return false
	}
	model.sendPending = false
	if result.Failed {
		model.sendUncertain = result.Uncertain
		model.sendStatus = "Send failed; draft kept"
		if result.Uncertain {
			model.sendStatus = "Delivery unknown; check recipient. Esc discard"
		}
		return true
	}
	model.sendStatus = ""
	clearSentDraft(model)
	return true
}

func clearSentDraft(model *viewModel) {
	model.composer.clear()
	model.replyTarget = replyTarget{}
	model.replySelect = replySelectionState{}
	model.chatView.resetScroll()
}
