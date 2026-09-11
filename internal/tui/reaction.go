package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

type reactionTargetState struct {
	valid, fromCompose, fromMe, group bool
	chatID, targetID, senderID        string
}

func messageReactionText(message messageView) string {
	if message.reactionCount <= 0 {
		return ""
	}
	parts := make([]string, 0, message.reactionCount)
	for index := 0; index < message.reactionCount; index++ {
		group := message.reactions[index]
		if group.emoji != "" && group.count > 0 {
			parts = append(parts, group.emoji+" "+strconv.Itoa(int(group.count)))
		}
	}
	return strings.Join(parts, "   ")
}

func beginReaction(model *viewModel) bool {
	if model == nil || !model.replySelect.valid {
		return false
	}
	chat, ok := model.chats.selectedChat()
	if !ok || model.replySelect.index < 0 || model.replySelect.index >= chat.messageCount {
		return false
	}
	message := chat.messages[model.replySelect.index]
	if message.id == "" || chat.isGroup && !message.fromMe && message.senderID == "" {
		model.sendStatus = "Reaction target unavailable"
		return true
	}
	model.reactionTarget = reactionTargetState{
		valid: true, fromCompose: model.replySelect.fromCompose, fromMe: message.fromMe, group: chat.isGroup,
		chatID: chat.id, targetID: string(message.id), senderID: message.senderID,
	}
	model.emojiPicker.prepareOpen()
	model.sendStatus = ""
	return true
}

func submitOutgoingReaction(model *viewModel, emoji string) bool {
	if model == nil || !model.reactionTarget.valid || model.send == nil || model.sendPending || model.sendUncertain ||
		len(emoji) > maxReactionEmojiBytes || !utf8.ValidString(emoji) || emoji != "" && !validEmojiPreference(emoji) {
		return false
	}
	target := model.reactionTarget
	request := SendRequest{
		ChatID: target.chatID, ReactionTargetID: target.targetID, ReactionTargetSenderID: target.senderID,
		ReactionTargetFromMe: target.fromMe, ReactionIsGroup: target.group, ReactionEmoji: emoji,
	}
	if err := model.send(request); err != nil {
		if model.asyncSend {
			model.sendStatus = "Reaction unavailable or rejected; target kept"
			return true
		}
		return false
	}
	if emoji != "" {
		model.emojiPicker.remember(emoji)
		if model.preferencesPath != "" {
			_ = saveEmojiPreferences(model.preferencesPath, &model.emojiPicker)
		}
	}
	model.emojiPicker.close()
	if model.asyncSend {
		model.sendPending = true
		model.sendStatus = "Sending reaction…"
		return true
	}
	model.reactionTarget = reactionTargetState{}
	model.replySelect = replySelectionState{}
	return true
}

func applyReactionUpdate(model *viewModel, update ReactionUpdate) bool {
	if model == nil || model.chats == nil || update.ChatID == "" || update.TargetMessageID == "" ||
		!utf8.ValidString(update.ChatID) || !utf8.ValidString(update.TargetMessageID) || !validReactionGroups(update.Groups) {
		return false
	}
	chatIndex, ok := model.chats.chatIndexByID(update.ChatID)
	if ok {
		if messageIndex, found := model.chats.messageIndexByID(chatIndex, messageID(update.TargetMessageID)); found {
			message := &model.chats.chats[chatIndex].messages[messageIndex]
			var next [maxReactionGroups]reactionGroupView
			for index, group := range update.Groups {
				next[index] = reactionGroupView{emoji: group.Emoji, count: group.Count, own: group.Own}
			}
			if message.reactionCount == len(update.Groups) && message.reactions == next {
				return false
			}
			message.reactions, message.reactionCount = next, len(update.Groups)
			model.chats.chats[chatIndex].revision++
			return true
		}
	}
	for index := 0; index < model.pendingReactionCount; index++ {
		if model.pendingReactions[index].ChatID == update.ChatID && model.pendingReactions[index].TargetMessageID == update.TargetMessageID {
			model.pendingReactions[index] = cloneReactionUpdate(update)
			return false
		}
	}
	if model.pendingReactionCount < len(model.pendingReactions) {
		model.pendingReactions[model.pendingReactionCount] = cloneReactionUpdate(update)
		model.pendingReactionCount++
	}
	return false
}

func cloneReactionUpdate(update ReactionUpdate) ReactionUpdate {
	return ReactionUpdate{ChatID: strings.Clone(update.ChatID), TargetMessageID: strings.Clone(update.TargetMessageID), Groups: append([]ReactionGroup(nil), update.Groups...)}
}

func applyPendingReaction(model *viewModel, chatID, targetID string) bool {
	for index := 0; index < model.pendingReactionCount; index++ {
		update := model.pendingReactions[index]
		if update.ChatID != chatID || update.TargetMessageID != targetID {
			continue
		}
		copy(model.pendingReactions[index:], model.pendingReactions[index+1:model.pendingReactionCount])
		model.pendingReactionCount--
		model.pendingReactions[model.pendingReactionCount] = ReactionUpdate{}
		return applyReactionUpdate(model, update)
	}
	return false
}
