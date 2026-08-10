package model

import "time"

const (
	// MaxRetentionSnapshotSummaries is the hard summary count for one prune
	// decision input.
	MaxRetentionSnapshotSummaries = 551
	// MaxPrunePlanDeletes is the hard message-row deletion count for one plan.
	MaxPrunePlanDeletes = 551
)

// MessageSummary is immutable body-free policy input for one message row.
type MessageSummary struct {
	chatID       ChatID
	messageID    MessageID
	sentAt       time.Time
	fromMe       bool
	bodyRetained bool
	bodyBytes    int
}

// NewMessageSummary derives a body-free summary from an immutable Message.
// An invalid zero Message yields the zero summary and is rejected by bounded
// snapshot construction before policy use.
func NewMessageSummary(message Message) MessageSummary {
	chatID, chatErr := NewChatID(message.ChatID().String())
	messageID, messageErr := NewMessageID(message.MessageID().String())
	if chatErr != nil || messageErr != nil || message.SentAt().IsZero() {
		return MessageSummary{}
	}
	bodyBytes := 0
	if message.BodyRetained() {
		bodyBytes = len(message.Text())
	}
	return MessageSummary{
		chatID:       chatID,
		messageID:    messageID,
		sentAt:       message.SentAt(),
		fromMe:       message.FromMe(),
		bodyRetained: message.BodyRetained(),
		bodyBytes:    bodyBytes,
	}
}

// ChatID returns the summary chat identifier.
func (summary MessageSummary) ChatID() ChatID {
	return summary.chatID
}

// MessageID returns the summary message identifier.
func (summary MessageSummary) MessageID() MessageID {
	return summary.messageID
}

// SentAt returns the summary timestamp.
func (summary MessageSummary) SentAt() time.Time {
	return summary.sentAt
}

// FromMe returns the summary direction.
func (summary MessageSummary) FromMe() bool {
	return summary.fromMe
}

// BodyRetained reports whether the summarized row has a retained body.
func (summary MessageSummary) BodyRetained() bool {
	return summary.bodyRetained
}

// BodyBytes returns the retained body byte count without exposing its text.
func (summary MessageSummary) BodyBytes() int {
	return summary.bodyBytes
}

// CacheUsage is an immutable deterministic cache-estimator snapshot.
type CacheUsage struct {
	estimatedBytes int64
	chats          int
	messages       int
	bodies         int
	anchors        int
	valid          bool
}

// NewCacheUsage validates five nonnegative cache counters.
func NewCacheUsage(
	estimatedBytes int64,
	chats int,
	messages int,
	bodies int,
	anchors int,
) (CacheUsage, error) {
	if estimatedBytes < 0 {
		return CacheUsage{}, newValidationError(InvalidValue, "estimated_bytes", 0)
	}
	for _, counter := range [...]struct {
		field string
		value int
	}{
		{field: "chats", value: chats},
		{field: "messages", value: messages},
		{field: "bodies", value: bodies},
		{field: "anchors", value: anchors},
	} {
		if counter.value < 0 {
			return CacheUsage{}, newValidationError(InvalidValue, counter.field, 0)
		}
	}
	return CacheUsage{
		estimatedBytes: estimatedBytes,
		chats:          chats,
		messages:       messages,
		bodies:         bodies,
		anchors:        anchors,
		valid:          true,
	}, nil
}

// Validate reports whether usage was constructed with valid nonnegative
// counters. This distinguishes a constructor-created all-zero snapshot from an
// unconstructed opaque zero value supplied in RetentionState.
func (usage CacheUsage) Validate() error {
	if !usage.valid {
		return newValidationError(Required, "cache_usage", 0)
	}
	_, err := NewCacheUsage(
		usage.estimatedBytes,
		usage.chats,
		usage.messages,
		usage.bodies,
		usage.anchors,
	)
	return err
}

// EstimatedBytes returns the conservative cache estimate.
func (usage CacheUsage) EstimatedBytes() int64 {
	return usage.estimatedBytes
}

// Chats returns the retained chat count.
func (usage CacheUsage) Chats() int {
	return usage.chats
}

// Messages returns the retained message-row count.
func (usage CacheUsage) Messages() int {
	return usage.messages
}

// Bodies returns the retained message-body count.
func (usage CacheUsage) Bodies() int {
	return usage.bodies
}

// Anchors returns the retained history-anchor count.
func (usage CacheUsage) Anchors() int {
	return usage.anchors
}

// RetentionAction is a closed policy outcome.
type RetentionAction uint8

const (
	KeepBody RetentionAction = iota + 1
	KeepMetadata
	Discard
)

// RetentionReason is a closed, content-free explanation for a policy outcome.
type RetentionReason uint8

const (
	RetentionEligible RetentionReason = iota + 1
	RetentionTooOld
	RetentionMessageCountLimit
	RetentionCacheLimit
)

// RetentionDecision is an immutable validated policy outcome.
type RetentionDecision struct {
	action RetentionAction
	reason RetentionReason
}

// NewRetentionDecision validates a closed action and reason pair. Pairing is
// deliberately not restricted: later bounded callers may use Discard with an
// existing content-free reason without extending the enum.
func NewRetentionDecision(
	action RetentionAction,
	reason RetentionReason,
) (RetentionDecision, error) {
	if err := validateRetentionAction(action); err != nil {
		return RetentionDecision{}, err
	}
	if err := validateRetentionReason(reason); err != nil {
		return RetentionDecision{}, err
	}
	return RetentionDecision{action: action, reason: reason}, nil
}

// Action returns the retention outcome.
func (decision RetentionDecision) Action() RetentionAction {
	return decision.action
}

// Reason returns the content-free retention rationale.
func (decision RetentionDecision) Reason() RetentionReason {
	return decision.reason
}

// RetentionState contains the caller-supplied changing policy state.
type RetentionState struct {
	Now         time.Time
	NewerBodies int
	Usage       CacheUsage
	Origin      WriteOrigin
}

// RetentionSnapshot is an immutable bounded body-free prune input.
type RetentionSnapshot struct {
	chatID    ChatID
	summaries []MessageSummary
	anchor    MessageAnchor
	hasAnchor bool
}

// NewRetentionSnapshot validates and defensively copies at most 551 summaries
// and one optional anchor for the same chat.
func NewRetentionSnapshot(
	chatID ChatID,
	summaries []MessageSummary,
	anchor *MessageAnchor,
) (RetentionSnapshot, error) {
	ownedChatID, err := NewChatID(chatID.String())
	if err != nil {
		return RetentionSnapshot{}, err
	}
	if len(summaries) > MaxRetentionSnapshotSummaries {
		return RetentionSnapshot{}, newValidationError(
			SizeExceeded,
			"retention_snapshot_summaries",
			MaxRetentionSnapshotSummaries,
		)
	}

	var ownedSummaries []MessageSummary
	if len(summaries) > 0 {
		ownedSummaries = make([]MessageSummary, len(summaries))
		for index, summary := range summaries {
			owned, err := cloneMessageSummary(summary)
			if err != nil {
				return RetentionSnapshot{}, err
			}
			if owned.ChatID().String() != ownedChatID.String() {
				return RetentionSnapshot{}, newValidationError(
					InvalidValue,
					"summary_chat_id",
					0,
				)
			}
			ownedSummaries[index] = owned
		}
	}

	result := RetentionSnapshot{chatID: ownedChatID, summaries: ownedSummaries}
	if anchor != nil {
		ownedAnchor, err := NewMessageAnchor(
			anchor.ChatID(),
			anchor.MessageID(),
			anchor.SentAt(),
			anchor.FromMe(),
		)
		if err != nil {
			return RetentionSnapshot{}, err
		}
		if ownedAnchor.ChatID().String() != ownedChatID.String() {
			return RetentionSnapshot{}, newValidationError(
				InvalidValue,
				"anchor_chat_id",
				0,
			)
		}
		result.anchor = ownedAnchor
		result.hasAnchor = true
	}
	return result, nil
}

// ChatID returns the snapshot chat identifier.
func (snapshot RetentionSnapshot) ChatID() ChatID {
	return snapshot.chatID
}

// Len returns the summary count.
func (snapshot RetentionSnapshot) Len() int {
	return len(snapshot.summaries)
}

// At returns the summary at index and whether index was in bounds.
func (snapshot RetentionSnapshot) At(index int) (MessageSummary, bool) {
	if index < 0 || index >= len(snapshot.summaries) {
		return MessageSummary{}, false
	}
	return snapshot.summaries[index], true
}

// Anchor returns the optional immutable current paging anchor.
func (snapshot RetentionSnapshot) Anchor() (MessageAnchor, bool) {
	return snapshot.anchor, snapshot.hasAnchor
}

// PrunePlan is an immutable bounded set of message-row deletions and at most
// one replacement paging anchor.
type PrunePlan struct {
	chatID         ChatID
	deleteIDs      []MessageID
	replacement    MessageAnchor
	hasReplacement bool
}

// NewPrunePlan validates and defensively copies at most 551 message IDs and
// one optional replacement anchor for the same chat.
func NewPrunePlan(
	chatID ChatID,
	deleteIDs []MessageID,
	replacement *MessageAnchor,
) (PrunePlan, error) {
	ownedChatID, err := NewChatID(chatID.String())
	if err != nil {
		return PrunePlan{}, err
	}
	if len(deleteIDs) > MaxPrunePlanDeletes {
		return PrunePlan{}, newValidationError(
			SizeExceeded,
			"prune_plan_deletes",
			MaxPrunePlanDeletes,
		)
	}

	var ownedIDs []MessageID
	if len(deleteIDs) > 0 {
		ownedIDs = make([]MessageID, len(deleteIDs))
		for index, id := range deleteIDs {
			ownedID, err := NewMessageID(id.String())
			if err != nil {
				return PrunePlan{}, err
			}
			ownedIDs[index] = ownedID
		}
	}

	result := PrunePlan{chatID: ownedChatID, deleteIDs: ownedIDs}
	if replacement != nil {
		ownedAnchor, err := NewMessageAnchor(
			replacement.ChatID(),
			replacement.MessageID(),
			replacement.SentAt(),
			replacement.FromMe(),
		)
		if err != nil {
			return PrunePlan{}, err
		}
		if ownedAnchor.ChatID().String() != ownedChatID.String() {
			return PrunePlan{}, newValidationError(
				InvalidValue,
				"replacement_anchor_chat_id",
				0,
			)
		}
		result.replacement = ownedAnchor
		result.hasReplacement = true
	}
	return result, nil
}

// ChatID returns the plan chat identifier.
func (plan PrunePlan) ChatID() ChatID {
	return plan.chatID
}

// DeleteLen returns the number of message rows to delete.
func (plan PrunePlan) DeleteLen() int {
	return len(plan.deleteIDs)
}

// DeleteAt returns the message ID at index and whether index was in bounds.
func (plan PrunePlan) DeleteAt(index int) (MessageID, bool) {
	if index < 0 || index >= len(plan.deleteIDs) {
		return MessageID{}, false
	}
	return plan.deleteIDs[index], true
}

// ReplacementAnchor returns the optional body-free paging replacement.
func (plan PrunePlan) ReplacementAnchor() (MessageAnchor, bool) {
	return plan.replacement, plan.hasReplacement
}

// PruneResult is immutable bounded-operation accounting.
type PruneResult struct {
	deletedRows    int
	removedBodies  int
	anchorReplaced bool
}

// NewPruneResult validates nonnegative pruning counts.
func NewPruneResult(
	deletedRows int,
	removedBodies int,
	anchorReplaced bool,
) (PruneResult, error) {
	if deletedRows < 0 {
		return PruneResult{}, newValidationError(InvalidValue, "deleted_rows", 0)
	}
	if removedBodies < 0 {
		return PruneResult{}, newValidationError(InvalidValue, "removed_bodies", 0)
	}
	return PruneResult{
		deletedRows:    deletedRows,
		removedBodies:  removedBodies,
		anchorReplaced: anchorReplaced,
	}, nil
}

// DeletedRows returns the deleted message-row count.
func (result PruneResult) DeletedRows() int {
	return result.deletedRows
}

// RemovedBodies returns the removed retained-body count.
func (result PruneResult) RemovedBodies() int {
	return result.removedBodies
}

// AnchorReplaced reports whether the stored paging anchor changed.
func (result PruneResult) AnchorReplaced() bool {
	return result.anchorReplaced
}

func validateRetentionAction(action RetentionAction) *ValidationError {
	if action == 0 {
		return newValidationError(Required, "retention_action", 0)
	}
	switch action {
	case KeepBody, KeepMetadata, Discard:
		return nil
	default:
		return newValidationError(InvalidValue, "retention_action", 0)
	}
}

func validateRetentionReason(reason RetentionReason) *ValidationError {
	if reason == 0 {
		return newValidationError(Required, "retention_reason", 0)
	}
	switch reason {
	case RetentionEligible,
		RetentionTooOld,
		RetentionMessageCountLimit,
		RetentionCacheLimit:
		return nil
	default:
		return newValidationError(InvalidValue, "retention_reason", 0)
	}
}

func cloneMessageSummary(summary MessageSummary) (MessageSummary, error) {
	chatID, err := NewChatID(summary.ChatID().String())
	if err != nil {
		return MessageSummary{}, err
	}
	messageID, err := NewMessageID(summary.MessageID().String())
	if err != nil {
		return MessageSummary{}, err
	}
	if summary.SentAt().IsZero() {
		return MessageSummary{}, newValidationError(Required, "sent_at", 0)
	}
	if summary.BodyBytes() < 0 || summary.BodyBytes() > MaxRetainedTextBytes {
		return MessageSummary{}, newValidationError(
			InvalidValue,
			"body_bytes",
			MaxRetainedTextBytes,
		)
	}
	if !summary.BodyRetained() && summary.BodyBytes() != 0 {
		return MessageSummary{}, newValidationError(InvalidValue, "body_bytes", 0)
	}
	return MessageSummary{
		chatID:       chatID,
		messageID:    messageID,
		sentAt:       summary.SentAt(),
		fromMe:       summary.FromMe(),
		bodyRetained: summary.BodyRetained(),
		bodyBytes:    summary.BodyBytes(),
	}, nil
}
