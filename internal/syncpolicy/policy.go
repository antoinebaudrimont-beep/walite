// Package syncpolicy supplies deterministic, dependency-free retention
// decisions over bounded model values.
package syncpolicy

import (
	"sort"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// Policy contains only validated static retention limits.
type Policy struct {
	messagesPerChat int
	maxAge          time.Duration
	cacheBytes      int64
}

// New validates and stores the pure retention configuration.
func New(retention config.Retention) (*Policy, error) {
	if err := retention.Validate(); err != nil {
		return nil, err
	}
	return &Policy{
		messagesPerChat: retention.MessagesPerChat,
		maxAge:          retention.MaxAge,
		cacheBytes:      retention.CacheBytes,
	}, nil
}

// Decide applies age, count, and history-first cache shedding in that exact
// order. Now and all changing state come from the caller.
func (policy *Policy) Decide(
	message model.Message,
	state model.RetentionState,
) (model.RetentionDecision, error) {
	if err := validateMessageIdentity(message); err != nil {
		return model.RetentionDecision{}, err
	}
	if err := policy.validate(); err != nil {
		return model.RetentionDecision{}, err
	}
	if err := validateState(state); err != nil {
		return model.RetentionDecision{}, err
	}

	cutoff := state.Now.Add(-policy.maxAge)
	if message.SentAt().Before(cutoff) {
		return model.NewRetentionDecision(model.KeepMetadata, model.RetentionTooOld)
	}
	if state.NewerBodies >= policy.messagesPerChat {
		return model.NewRetentionDecision(
			model.KeepMetadata,
			model.RetentionMessageCountLimit,
		)
	}
	if state.Origin == model.WriteHistory &&
		state.Usage.EstimatedBytes() >= policy.cacheBytes {
		return model.NewRetentionDecision(model.KeepMetadata, model.RetentionCacheLimit)
	}
	return model.NewRetentionDecision(model.KeepBody, model.RetentionEligible)
}

// PlanPrune deterministically removes bodyless rows, rows strictly older than
// the age cutoff, and retained-body rows after the newest configured count.
// It returns at most one older body-free paging anchor.
func (policy *Policy) PlanPrune(
	snapshot model.RetentionSnapshot,
	state model.RetentionState,
) (model.PrunePlan, error) {
	if err := policy.validate(); err != nil {
		return model.PrunePlan{}, err
	}
	if err := validateState(state); err != nil {
		return model.PrunePlan{}, err
	}
	if err := validateSnapshot(snapshot); err != nil {
		return model.PrunePlan{}, err
	}

	var ordered [model.MaxRetentionSnapshotSummaries]model.MessageSummary
	for index := 0; index < snapshot.Len(); index++ {
		summary, ok := snapshot.At(index)
		if !ok {
			return model.PrunePlan{}, validationError(
				model.InvalidValue,
				"retention_snapshot",
				0,
			)
		}
		ordered[index] = summary
	}
	work := ordered[:snapshot.Len()]
	sort.Slice(work, func(left, right int) bool {
		return summaryNewer(work[left], work[right])
	})

	cutoff := state.Now.Add(-policy.maxAge)
	keptBodies := 0
	deleteCount := 0
	var deleteIDs [model.MaxPrunePlanDeletes]model.MessageID
	var oldestDeleted model.MessageSummary
	hasDeleted := false
	for _, summary := range work {
		remove := !summary.BodyRetained() || summary.SentAt().Before(cutoff)
		if !remove {
			if keptBodies < policy.messagesPerChat {
				keptBodies++
			} else {
				remove = true
			}
		}
		if !remove {
			continue
		}

		deleteIDs[deleteCount] = summary.MessageID()
		deleteCount++
		if !hasDeleted || summaryOlder(summary, oldestDeleted) {
			oldestDeleted = summary
			hasDeleted = true
		}
	}

	var replacement *model.MessageAnchor
	if hasDeleted {
		candidate, err := model.NewMessageAnchor(
			oldestDeleted.ChatID(),
			oldestDeleted.MessageID(),
			oldestDeleted.SentAt(),
			oldestDeleted.FromMe(),
		)
		if err != nil {
			return model.PrunePlan{}, err
		}
		existing, hasExisting := snapshot.Anchor()
		if !hasExisting || anchorOlder(candidate, existing) {
			replacement = &candidate
		}
	}

	return model.NewPrunePlan(
		snapshot.ChatID(),
		deleteIDs[:deleteCount],
		replacement,
	)
}

func (policy *Policy) validate() error {
	if policy == nil || policy.messagesPerChat <= 0 || policy.maxAge <= 0 ||
		policy.cacheBytes <= 0 {
		return validationError(model.Required, "retention_policy", 0)
	}
	return nil
}

func validateMessageIdentity(message model.Message) error {
	if _, err := model.NewChatID(message.ChatID().String()); err != nil {
		return err
	}
	if _, err := model.NewMessageID(message.MessageID().String()); err != nil {
		return err
	}
	if message.SentAt().IsZero() {
		return validationError(model.Required, "sent_at", 0)
	}
	return nil
}

func validateState(state model.RetentionState) error {
	if state.Now.IsZero() {
		return validationError(model.Required, "retention_now", 0)
	}
	if state.NewerBodies < 0 {
		return validationError(model.InvalidValue, "newer_bodies", 0)
	}
	if err := state.Usage.Validate(); err != nil {
		return err
	}
	switch state.Origin {
	case model.WriteRealtime, model.WriteHistory:
		return nil
	case 0:
		return validationError(model.Required, "write_origin", 0)
	default:
		return validationError(model.InvalidValue, "write_origin", 0)
	}
}

func validateSnapshot(snapshot model.RetentionSnapshot) error {
	chatID, err := model.NewChatID(snapshot.ChatID().String())
	if err != nil {
		return err
	}
	if snapshot.Len() < 0 || snapshot.Len() > model.MaxRetentionSnapshotSummaries {
		return validationError(
			model.SizeExceeded,
			"retention_snapshot_summaries",
			model.MaxRetentionSnapshotSummaries,
		)
	}
	for index := 0; index < snapshot.Len(); index++ {
		summary, ok := snapshot.At(index)
		if !ok {
			return validationError(model.InvalidValue, "retention_snapshot", 0)
		}
		if _, err := model.NewChatID(summary.ChatID().String()); err != nil {
			return err
		}
		if summary.ChatID().String() != chatID.String() {
			return validationError(model.InvalidValue, "summary_chat_id", 0)
		}
		if _, err := model.NewMessageID(summary.MessageID().String()); err != nil {
			return err
		}
		if summary.SentAt().IsZero() {
			return validationError(model.Required, "sent_at", 0)
		}
		if summary.BodyBytes() < 0 ||
			summary.BodyBytes() > model.MaxRetainedTextBytes ||
			(!summary.BodyRetained() && summary.BodyBytes() != 0) {
			return validationError(
				model.InvalidValue,
				"body_bytes",
				model.MaxRetainedTextBytes,
			)
		}
	}
	if anchor, ok := snapshot.Anchor(); ok {
		owned, err := model.NewMessageAnchor(
			anchor.ChatID(),
			anchor.MessageID(),
			anchor.SentAt(),
			anchor.FromMe(),
		)
		if err != nil {
			return err
		}
		if owned.ChatID().String() != chatID.String() {
			return validationError(model.InvalidValue, "anchor_chat_id", 0)
		}
	}
	return nil
}

func summaryNewer(left, right model.MessageSummary) bool {
	if left.SentAt().Equal(right.SentAt()) {
		return left.MessageID().String() > right.MessageID().String()
	}
	return left.SentAt().After(right.SentAt())
}

func summaryOlder(left, right model.MessageSummary) bool {
	if left.SentAt().Equal(right.SentAt()) {
		return left.MessageID().String() < right.MessageID().String()
	}
	return left.SentAt().Before(right.SentAt())
}

func anchorOlder(left, right model.MessageAnchor) bool {
	if left.SentAt().Equal(right.SentAt()) {
		return left.MessageID().String() < right.MessageID().String()
	}
	return left.SentAt().Before(right.SentAt())
}

func validationError(
	code model.ValidationCode,
	field string,
	limit int,
) *model.ValidationError {
	return &model.ValidationError{Code: code, Field: field, Limit: limit}
}
