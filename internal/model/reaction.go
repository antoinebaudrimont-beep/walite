package model

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

const (
	// MaxReactionsPerMessage bounds persisted reactor state, including removal
	// tombstones needed to reject an older replay.
	MaxReactionsPerMessage = 64
	// MaxReactionGroupsPerMessage bounds the aggregate copied to presentation.
	MaxReactionGroupsPerMessage = 16
	// MaxReactionEmojiBytes accommodates one complete complex emoji grapheme.
	MaxReactionEmojiBytes = 64
	// SelfReactorID is the transport-neutral identity for the linked account.
	SelfReactorID = "walite:self"
)

// ReactionInput is the unvalidated transport-neutral reaction mutation.
// Empty Emoji is a removal. UpdatedAt is the upstream sender timestamp.
type ReactionInput struct {
	ChatID, TargetMessageID, ReactorID string
	Emoji                              string
	UpdatedAt                          time.Time
}

// Reaction is an immutable latest-value mutation for one reactor and target.
type Reaction struct {
	chatID, targetMessageID, reactorID string
	emoji                              string
	updatedAt                          time.Time
}

func NewReaction(input ReactionInput) (Reaction, error) {
	if _, err := NewChatID(input.ChatID); err != nil {
		return Reaction{}, err
	}
	if _, err := NewMessageID(input.TargetMessageID); err != nil {
		return Reaction{}, err
	}
	if _, err := NewContactID(input.ReactorID); err != nil {
		return Reaction{}, err
	}
	if input.UpdatedAt.IsZero() {
		return Reaction{}, newValidationError(Required, "reaction_updated_at", 0)
	}
	if input.Emoji != "" && (!utf8.ValidString(input.Emoji) || len(input.Emoji) > MaxReactionEmojiBytes || uniseg.GraphemeClusterCount(input.Emoji) != 1) {
		return Reaction{}, newValidationError(InvalidValue, "reaction_emoji", MaxReactionEmojiBytes)
	}
	return Reaction{
		chatID: strings.Clone(input.ChatID), targetMessageID: strings.Clone(input.TargetMessageID),
		reactorID: strings.Clone(input.ReactorID), emoji: strings.Clone(input.Emoji), updatedAt: input.UpdatedAt.UTC(),
	}, nil
}

func (reaction Reaction) ChatID() ChatID {
	id, _ := NewChatID(reaction.chatID)
	return id
}

func (reaction Reaction) TargetMessageID() MessageID {
	id, _ := NewMessageID(reaction.targetMessageID)
	return id
}

func (reaction Reaction) ReactorID() ContactID {
	id, _ := NewContactID(reaction.reactorID)
	return id
}

func (reaction Reaction) Emoji() string        { return reaction.emoji }
func (reaction Reaction) Removed() bool        { return reaction.emoji == "" }
func (reaction Reaction) UpdatedAt() time.Time { return reaction.updatedAt }

// WithChatID returns an alias-normalized immutable copy.
func (reaction Reaction) WithChatID(id ChatID) (Reaction, error) {
	return NewReaction(ReactionInput{
		ChatID: id.String(), TargetMessageID: reaction.targetMessageID, ReactorID: reaction.reactorID,
		Emoji: reaction.emoji, UpdatedAt: reaction.updatedAt,
	})
}

// WithReactorID returns an alias-normalized immutable copy.
func (reaction Reaction) WithReactorID(id ContactID) (Reaction, error) {
	return NewReaction(ReactionInput{
		ChatID: reaction.chatID, TargetMessageID: reaction.targetMessageID, ReactorID: id.String(),
		Emoji: reaction.emoji, UpdatedAt: reaction.updatedAt,
	})
}

// ReactionGroup is one bounded aggregate for presentation.
type ReactionGroup struct {
	emoji string
	count uint16
	own   bool
}

func NewReactionGroup(emoji string, count uint16, own bool) (ReactionGroup, error) {
	if count == 0 || count > MaxReactionsPerMessage || emoji == "" || !utf8.ValidString(emoji) ||
		len(emoji) > MaxReactionEmojiBytes || uniseg.GraphemeClusterCount(emoji) != 1 {
		return ReactionGroup{}, errors.New("reaction group rejected")
	}
	return ReactionGroup{emoji: strings.Clone(emoji), count: count, own: own}, nil
}

func (group ReactionGroup) Emoji() string { return group.emoji }
func (group ReactionGroup) Count() uint16 { return group.count }
func (group ReactionGroup) Own() bool     { return group.own }

// ReactionSummary is the complete bounded visible aggregate for one message.
type ReactionSummary struct {
	chatID, targetMessageID string
	groups                  [MaxReactionGroupsPerMessage]ReactionGroup
	count                   int
}

func NewReactionSummary(chatID, targetMessageID string, groups []ReactionGroup) (ReactionSummary, error) {
	if _, err := NewChatID(chatID); err != nil {
		return ReactionSummary{}, err
	}
	if _, err := NewMessageID(targetMessageID); err != nil {
		return ReactionSummary{}, err
	}
	if len(groups) > MaxReactionGroupsPerMessage {
		return ReactionSummary{}, errors.New("reaction summary rejected")
	}
	result := ReactionSummary{chatID: strings.Clone(chatID), targetMessageID: strings.Clone(targetMessageID), count: len(groups)}
	seen := make(map[string]struct{}, len(groups))
	for index, group := range groups {
		owned, err := NewReactionGroup(group.Emoji(), group.Count(), group.Own())
		if err != nil {
			return ReactionSummary{}, err
		}
		if _, duplicate := seen[owned.Emoji()]; duplicate {
			return ReactionSummary{}, errors.New("reaction summary rejected")
		}
		seen[owned.Emoji()] = struct{}{}
		result.groups[index] = owned
	}
	return result, nil
}

func (summary ReactionSummary) ChatID() ChatID {
	id, _ := NewChatID(summary.chatID)
	return id
}

func (summary ReactionSummary) TargetMessageID() MessageID {
	id, _ := NewMessageID(summary.targetMessageID)
	return id
}

func (summary ReactionSummary) Len() int { return summary.count }
func (summary ReactionSummary) At(index int) (ReactionGroup, bool) {
	if index < 0 || index >= summary.count {
		return ReactionGroup{}, false
	}
	return summary.groups[index], true
}

// SendReactionRequest contains only neutral target identity and one emoji.
type SendReactionRequest struct {
	chatID, targetMessageID, targetSenderID string
	targetFromMe, group                     bool
	emoji                                   string
}

func NewSendReactionRequest(chatID, targetMessageID, targetSenderID string, targetFromMe, group bool, emoji string) (SendReactionRequest, error) {
	if _, err := NewChatID(chatID); err != nil {
		return SendReactionRequest{}, err
	}
	if _, err := NewMessageID(targetMessageID); err != nil {
		return SendReactionRequest{}, err
	}
	if targetSenderID != "" {
		if _, err := NewContactID(targetSenderID); err != nil {
			return SendReactionRequest{}, err
		}
	}
	if group && !targetFromMe && targetSenderID == "" {
		return SendReactionRequest{}, errors.New("group reaction target rejected")
	}
	if emoji != "" && (!utf8.ValidString(emoji) || len(emoji) > MaxReactionEmojiBytes || uniseg.GraphemeClusterCount(emoji) != 1) {
		return SendReactionRequest{}, errors.New("reaction request rejected")
	}
	return SendReactionRequest{chatID: strings.Clone(chatID), targetMessageID: strings.Clone(targetMessageID), targetSenderID: strings.Clone(targetSenderID),
		targetFromMe: targetFromMe, group: group, emoji: strings.Clone(emoji)}, nil
}

func (request SendReactionRequest) ChatID() ChatID {
	id, _ := NewChatID(request.chatID)
	return id
}
func (request SendReactionRequest) TargetMessageID() MessageID {
	id, _ := NewMessageID(request.targetMessageID)
	return id
}
func (request SendReactionRequest) TargetSenderID() ContactID {
	if request.targetSenderID == "" {
		return ContactID{}
	}
	id, _ := NewContactID(request.targetSenderID)
	return id
}
func (request SendReactionRequest) TargetFromMe() bool { return request.targetFromMe }
func (request SendReactionRequest) IsGroup() bool      { return request.group }
func (request SendReactionRequest) Emoji() string      { return request.emoji }
