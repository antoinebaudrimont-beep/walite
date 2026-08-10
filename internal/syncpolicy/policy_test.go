package syncpolicy

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

var policyTestNow = time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)

func TestNewValidatesRetentionConfiguration(t *testing.T) {
	t.Parallel()

	defaults := config.DefaultValues().Retention
	policy, err := New(defaults)
	if err != nil {
		t.Fatalf("New(defaults): %v", err)
	}
	if policy.messagesPerChat != defaults.MessagesPerChat ||
		policy.maxAge != defaults.MaxAge || policy.cacheBytes != defaults.CacheBytes {
		t.Fatalf("Policy = %#v, does not preserve retention limits", policy)
	}

	tests := []struct {
		name  string
		field config.Field
		set   func(*config.Retention)
	}{
		{name: "messages", field: config.FieldRetentionMessagesPerChat, set: func(value *config.Retention) { value.MessagesPerChat = 0 }},
		{name: "age", field: config.FieldRetentionMaxAge, set: func(value *config.Retention) { value.MaxAge = 0 }},
		{name: "cache", field: config.FieldRetentionCacheBytes, set: func(value *config.Retention) { value.CacheBytes = 0 }},
		{name: "chats", field: config.FieldRetentionMaxChats, set: func(value *config.Retention) { value.MaxChats = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			retention := defaults
			test.set(&retention)
			_, err := New(retention)
			var validationErr *config.ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("New error type = %T, want *config.ValidationError", err)
			}
			if validationErr.Field != test.field || validationErr.Rule != config.Required {
				t.Fatalf(
					"config ValidationError = (%q, %q), want (%q, %q)",
					validationErr.Field,
					validationErr.Rule,
					test.field,
					config.Required,
				)
			}
		})
	}
}

func TestDecideExactSemanticsAndPrecedence(t *testing.T) {
	t.Parallel()

	retention := config.DefaultValues().Retention
	policy := mustPolicy(t, retention)
	cutoff := policyTestNow.Add(-retention.MaxAge)
	belowLimit := retention.MessagesPerChat - 1
	belowCache := retention.CacheBytes - 1

	tests := []struct {
		name        string
		sentAt      time.Time
		newerBodies int
		usageBytes  int64
		origin      model.WriteOrigin
		wantAction  model.RetentionAction
		wantReason  model.RetentionReason
	}{
		{
			name:   "eligible",
			sentAt: policyTestNow, newerBodies: belowLimit, usageBytes: belowCache,
			origin: model.WriteHistory, wantAction: model.KeepBody,
			wantReason: model.RetentionEligible,
		},
		{
			name:   "one nanosecond older",
			sentAt: cutoff.Add(-time.Nanosecond), newerBodies: 0,
			origin: model.WriteRealtime, wantAction: model.KeepMetadata,
			wantReason: model.RetentionTooOld,
		},
		{
			name:   "exact cutoff",
			sentAt: cutoff, newerBodies: belowLimit, usageBytes: belowCache,
			origin: model.WriteHistory, wantAction: model.KeepBody,
			wantReason: model.RetentionEligible,
		},
		{
			name:   "count at limit",
			sentAt: policyTestNow, newerBodies: retention.MessagesPerChat,
			origin: model.WriteRealtime, wantAction: model.KeepMetadata,
			wantReason: model.RetentionMessageCountLimit,
		},
		{
			name:   "one below count",
			sentAt: policyTestNow, newerBodies: belowLimit,
			origin: model.WriteRealtime, wantAction: model.KeepBody,
			wantReason: model.RetentionEligible,
		},
		{
			name:   "history at cache limit",
			sentAt: policyTestNow, newerBodies: 0, usageBytes: retention.CacheBytes,
			origin: model.WriteHistory, wantAction: model.KeepMetadata,
			wantReason: model.RetentionCacheLimit,
		},
		{
			name:   "history below cache limit",
			sentAt: policyTestNow, newerBodies: 0, usageBytes: belowCache,
			origin: model.WriteHistory, wantAction: model.KeepBody,
			wantReason: model.RetentionEligible,
		},
		{
			name:   "realtime at cache limit",
			sentAt: policyTestNow, newerBodies: 0, usageBytes: retention.CacheBytes,
			origin: model.WriteRealtime, wantAction: model.KeepBody,
			wantReason: model.RetentionEligible,
		},
		{
			name:        "age precedes count and cache",
			sentAt:      cutoff.Add(-time.Nanosecond),
			newerBodies: retention.MessagesPerChat, usageBytes: retention.CacheBytes,
			origin: model.WriteHistory, wantAction: model.KeepMetadata,
			wantReason: model.RetentionTooOld,
		},
		{
			name:   "count precedes cache",
			sentAt: policyTestNow, newerBodies: retention.MessagesPerChat,
			usageBytes: retention.CacheBytes, origin: model.WriteHistory,
			wantAction: model.KeepMetadata,
			wantReason: model.RetentionMessageCountLimit,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := policyMessage(t, "message-a", test.sentAt, "body", false)
			state := policyState(t, test.newerBodies, test.usageBytes, test.origin)
			decision, err := policy.Decide(message, state)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if decision.Action() != test.wantAction || decision.Reason() != test.wantReason {
				t.Fatalf("decision = (%d, %d), want (%d, %d)", decision.Action(), decision.Reason(), test.wantAction, test.wantReason)
			}
		})
	}
}

func TestDecideRejectsMalformedMessageBeforeState(t *testing.T) {
	t.Parallel()

	policy := mustPolicy(t, config.DefaultValues().Retention)
	_, err := policy.Decide(model.Message{}, model.RetentionState{})
	requireModelValidationError(t, err, model.Required, "chat_id", 0)

	var nilPolicy *Policy
	_, err = nilPolicy.Decide(model.Message{}, model.RetentionState{})
	requireModelValidationError(t, err, model.Required, "chat_id", 0)
}

func TestPolicyRejectsInvalidRetentionState(t *testing.T) {
	t.Parallel()

	policy := mustPolicy(t, config.DefaultValues().Retention)
	message := policyMessage(t, "message-a", policyTestNow, "body", false)
	validUsage := policyUsage(t, 0)
	tests := []struct {
		name  string
		state model.RetentionState
		code  model.ValidationCode
		field string
	}{
		{
			name:  "zero Now",
			state: model.RetentionState{Usage: validUsage, Origin: model.WriteRealtime},
			code:  model.Required, field: "retention_now",
		},
		{
			name:  "negative newer bodies",
			state: model.RetentionState{Now: policyTestNow, NewerBodies: -1, Usage: validUsage, Origin: model.WriteRealtime},
			code:  model.InvalidValue, field: "newer_bodies",
		},
		{
			name:  "unconstructed cache usage",
			state: model.RetentionState{Now: policyTestNow, Origin: model.WriteRealtime},
			code:  model.Required, field: "cache_usage",
		},
		{
			name:  "zero origin",
			state: model.RetentionState{Now: policyTestNow, Usage: validUsage},
			code:  model.Required, field: "write_origin",
		},
		{
			name:  "invalid origin",
			state: model.RetentionState{Now: policyTestNow, Usage: validUsage, Origin: model.WriteOrigin(255)},
			code:  model.InvalidValue, field: "write_origin",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := policy.Decide(message, test.state)
			requireModelValidationError(t, err, test.code, test.field, 0)
		})
	}

	var nilPolicy *Policy
	_, err := nilPolicy.Decide(message, policyState(t, 0, 0, model.WriteRealtime))
	requireModelValidationError(t, err, model.Required, "retention_policy", 0)
}

func TestPlanPruneDeterministicCountAndTieBreak(t *testing.T) {
	t.Parallel()

	retention := config.DefaultValues().Retention
	retention.MessagesPerChat = 20
	policy := mustPolicy(t, retention)
	chatID := policyChatID(t)
	sentAt := policyTestNow.Add(-time.Hour)

	// Deliberately noncanonical input order. All timestamps are equal, so the
	// message-ID descending tie-break alone determines the retained 20.
	order := []int{5, 0, 21, 3, 18, 1, 20, 7, 16, 2, 19, 4, 17, 6, 15, 8, 14, 9, 13, 10, 12, 11}
	summaries := make([]model.MessageSummary, 0, len(order))
	for _, index := range order {
		summaries = append(summaries, policySummary(
			t,
			fmt.Sprintf("message-%02d", index),
			sentAt,
			"body",
			index == 0,
			true,
		))
	}
	snapshot := policySnapshot(t, chatID, summaries, nil)
	before := snapshotIDs(t, snapshot)
	state := policyState(t, 0, 0, model.WriteRealtime)

	first, err := policy.PlanPrune(snapshot, state)
	if err != nil {
		t.Fatalf("PlanPrune: %v", err)
	}
	if got := planIDs(t, first); !equalStrings(got, []string{"message-01", "message-00"}) {
		t.Fatalf("delete IDs = %v, want [message-01 message-00]", got)
	}
	anchor, ok := first.ReplacementAnchor()
	if !ok || anchor.MessageID().String() != "message-00" || !anchor.FromMe() {
		t.Fatalf("replacement = (%q, %t, %t)", anchor.MessageID().String(), anchor.FromMe(), ok)
	}
	if after := snapshotIDs(t, snapshot); !equalStrings(after, before) {
		t.Fatalf("PlanPrune mutated snapshot: before %v after %v", before, after)
	}

	second, err := policy.PlanPrune(snapshot, state)
	if err != nil {
		t.Fatalf("second PlanPrune: %v", err)
	}
	assertPlansEqual(t, first, second)
}

func TestPlanPruneStrictAgeBoundary(t *testing.T) {
	t.Parallel()

	retention := config.DefaultValues().Retention
	retention.MessagesPerChat = 20
	policy := mustPolicy(t, retention)
	cutoff := policyTestNow.Add(-retention.MaxAge)
	chatID := policyChatID(t)
	summaries := []model.MessageSummary{
		policySummary(t, "message-exact", cutoff, "body", false, true),
		policySummary(t, "message-old", cutoff.Add(-time.Nanosecond), "body", true, true),
		policySummary(t, "message-new", cutoff.Add(time.Nanosecond), "body", false, true),
	}
	plan, err := policy.PlanPrune(
		policySnapshot(t, chatID, summaries, nil),
		policyState(t, 0, 0, model.WriteHistory),
	)
	if err != nil {
		t.Fatalf("PlanPrune: %v", err)
	}
	if got := planIDs(t, plan); !equalStrings(got, []string{"message-old"}) {
		t.Fatalf("strict-age delete IDs = %v, want [message-old]", got)
	}
	anchor, ok := plan.ReplacementAnchor()
	if !ok || anchor.MessageID().String() != "message-old" || !anchor.FromMe() {
		t.Fatalf("age replacement = (%q, %t, %t)", anchor.MessageID().String(), anchor.FromMe(), ok)
	}
}

func TestPlanPruneCondensesBodylessRowsWithoutCountingThem(t *testing.T) {
	t.Parallel()

	retention := config.DefaultValues().Retention
	retention.MessagesPerChat = 20
	policy := mustPolicy(t, retention)
	chatID := policyChatID(t)
	summaries := []model.MessageSummary{
		policySummary(t, "metadata-only", policyTestNow.Add(time.Second), "body", false, false),
	}
	for index := 0; index < retention.MessagesPerChat; index++ {
		// Empty text remains a retained body and therefore still consumes one
		// body slot; BodyRetained, not BodyBytes, defines the count.
		summaries = append(summaries, policySummary(
			t,
			fmt.Sprintf("message-%02d", index),
			policyTestNow,
			"",
			false,
			true,
		))
	}
	plan, err := policy.PlanPrune(
		policySnapshot(t, chatID, summaries, nil),
		policyState(t, 0, 0, model.WriteRealtime),
	)
	if err != nil {
		t.Fatalf("PlanPrune: %v", err)
	}
	if got := planIDs(t, plan); !equalStrings(got, []string{"metadata-only"}) {
		t.Fatalf("bodyless delete IDs = %v, want [metadata-only]", got)
	}
}

func TestPlanPruneReplacementRespectsExistingOlderAnchor(t *testing.T) {
	t.Parallel()

	retention := config.DefaultValues().Retention
	policy := mustPolicy(t, retention)
	chatID := policyChatID(t)
	cutoff := policyTestNow.Add(-retention.MaxAge)
	candidateTime := cutoff.Add(-time.Second)
	summary := policySummary(t, "message-b", candidateTime, "body", true, true)
	state := policyState(t, 0, 0, model.WriteHistory)

	tests := []struct {
		name            string
		existingID      string
		existingTime    time.Time
		wantReplacement bool
	}{
		{name: "older time", existingID: "anchor-a", existingTime: candidateTime.Add(-time.Second)},
		{name: "equal key", existingID: "message-b", existingTime: candidateTime},
		{name: "equal time smaller ID", existingID: "message-a", existingTime: candidateTime},
		{name: "newer time", existingID: "anchor-a", existingTime: candidateTime.Add(time.Second), wantReplacement: true},
		{name: "equal time larger ID", existingID: "message-c", existingTime: candidateTime, wantReplacement: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := policyAnchor(t, chatID, test.existingID, test.existingTime, false)
			plan, err := policy.PlanPrune(
				policySnapshot(t, chatID, []model.MessageSummary{summary}, &existing),
				state,
			)
			if err != nil {
				t.Fatalf("PlanPrune: %v", err)
			}
			anchor, ok := plan.ReplacementAnchor()
			if ok != test.wantReplacement {
				t.Fatalf("ReplacementAnchor found = %t, want %t", ok, test.wantReplacement)
			}
			if ok && anchor.MessageID().String() != "message-b" {
				t.Fatalf("replacement ID = %q, want message-b", anchor.MessageID().String())
			}
		})
	}
}

func TestPlanPruneEmptySnapshotDoesNotEchoAnchor(t *testing.T) {
	t.Parallel()

	policy := mustPolicy(t, config.DefaultValues().Retention)
	chatID := policyChatID(t)
	existing := policyAnchor(t, chatID, "anchor-a", policyTestNow.Add(-time.Hour), false)
	plan, err := policy.PlanPrune(
		policySnapshot(t, chatID, nil, &existing),
		policyState(t, 0, 0, model.WriteRealtime),
	)
	if err != nil {
		t.Fatalf("PlanPrune(empty): %v", err)
	}
	if plan.DeleteLen() != 0 {
		t.Fatalf("empty plan deletes = %d, want 0", plan.DeleteLen())
	}
	if _, ok := plan.ReplacementAnchor(); ok {
		t.Fatal("empty plan echoed an existing anchor as a replacement")
	}
}

func TestPlanPruneMaximumBound(t *testing.T) {
	t.Parallel()

	retention := config.DefaultValues().Retention
	policy := mustPolicy(t, retention)
	chatID := policyChatID(t)
	summaries := make([]model.MessageSummary, model.MaxRetentionSnapshotSummaries)
	cutoff := policyTestNow.Add(-retention.MaxAge)
	for index := range summaries {
		summaries[index] = policySummary(
			t,
			fmt.Sprintf("message-%03d", index),
			cutoff.Add(-time.Duration(index+1)*time.Second),
			"body",
			index == len(summaries)-1,
			true,
		)
	}
	plan, err := policy.PlanPrune(
		policySnapshot(t, chatID, summaries, nil),
		policyState(t, 0, retention.CacheBytes, model.WriteHistory),
	)
	if err != nil {
		t.Fatalf("PlanPrune(551): %v", err)
	}
	if plan.DeleteLen() != model.MaxPrunePlanDeletes {
		t.Fatalf("delete count = %d, want %d", plan.DeleteLen(), model.MaxPrunePlanDeletes)
	}
	first, _ := plan.DeleteAt(0)
	last, _ := plan.DeleteAt(plan.DeleteLen() - 1)
	if first.String() != "message-000" || last.String() != "message-550" {
		t.Fatalf("bounded delete order = first %q last %q", first.String(), last.String())
	}
	anchor, ok := plan.ReplacementAnchor()
	if !ok || anchor.MessageID().String() != "message-550" || !anchor.FromMe() {
		t.Fatalf("maximum replacement = (%q, %t, %t)", anchor.MessageID().String(), anchor.FromMe(), ok)
	}
}

func TestPlanPruneRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	policy := mustPolicy(t, config.DefaultValues().Retention)
	state := policyState(t, 0, 0, model.WriteRealtime)
	_, err := policy.PlanPrune(model.RetentionSnapshot{}, state)
	requireModelValidationError(t, err, model.Required, "chat_id", 0)

	valid := policySnapshot(t, policyChatID(t), nil, nil)
	invalidStates := []struct {
		state model.RetentionState
		code  model.ValidationCode
		field string
	}{
		{state: model.RetentionState{Usage: policyUsage(t, 0), Origin: model.WriteRealtime}, code: model.Required, field: "retention_now"},
		{state: model.RetentionState{Now: policyTestNow, NewerBodies: -1, Usage: policyUsage(t, 0), Origin: model.WriteRealtime}, code: model.InvalidValue, field: "newer_bodies"},
		{state: model.RetentionState{Now: policyTestNow, Origin: model.WriteRealtime}, code: model.Required, field: "cache_usage"},
		{state: model.RetentionState{Now: policyTestNow, Usage: policyUsage(t, 0), Origin: model.WriteOrigin(255)}, code: model.InvalidValue, field: "write_origin"},
	}
	for _, test := range invalidStates {
		_, err := policy.PlanPrune(valid, test.state)
		requireModelValidationError(t, err, test.code, test.field, 0)
	}
}

func mustPolicy(t *testing.T, retention config.Retention) *Policy {
	t.Helper()
	policy, err := New(retention)
	if err != nil {
		t.Fatalf("New policy: %v", err)
	}
	return policy
}

func policyMessage(
	t *testing.T,
	messageID string,
	sentAt time.Time,
	text string,
	fromMe bool,
) model.Message {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{
		ChatID:    "chat-a",
		MessageID: messageID,
		SentAt:    sentAt,
		Text:      text,
		FromMe:    fromMe,
	})
	if err != nil {
		t.Fatalf("NewMessage(%q): %v", messageID, err)
	}
	return message
}

func policyUsage(t *testing.T, estimatedBytes int64) model.CacheUsage {
	t.Helper()
	usage, err := model.NewCacheUsage(estimatedBytes, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("NewCacheUsage: %v", err)
	}
	return usage
}

func policyState(
	t *testing.T,
	newerBodies int,
	estimatedBytes int64,
	origin model.WriteOrigin,
) model.RetentionState {
	t.Helper()
	return model.RetentionState{
		Now:         policyTestNow,
		NewerBodies: newerBodies,
		Usage:       policyUsage(t, estimatedBytes),
		Origin:      origin,
	}
}

func policyChatID(t *testing.T) model.ChatID {
	t.Helper()
	chatID, err := model.NewChatID("chat-a")
	if err != nil {
		t.Fatalf("NewChatID: %v", err)
	}
	return chatID
}

func policySummary(
	t *testing.T,
	messageID string,
	sentAt time.Time,
	text string,
	fromMe bool,
	bodyRetained bool,
) model.MessageSummary {
	t.Helper()
	message := policyMessage(t, messageID, sentAt, text, fromMe)
	if !bodyRetained {
		message = message.WithoutBody()
	}
	return model.NewMessageSummary(message)
}

func policySnapshot(
	t *testing.T,
	chatID model.ChatID,
	summaries []model.MessageSummary,
	anchor *model.MessageAnchor,
) model.RetentionSnapshot {
	t.Helper()
	snapshot, err := model.NewRetentionSnapshot(chatID, summaries, anchor)
	if err != nil {
		t.Fatalf("NewRetentionSnapshot: %v", err)
	}
	return snapshot
}

func policyAnchor(
	t *testing.T,
	chatID model.ChatID,
	messageID string,
	sentAt time.Time,
	fromMe bool,
) model.MessageAnchor {
	t.Helper()
	id, err := model.NewMessageID(messageID)
	if err != nil {
		t.Fatalf("NewMessageID(%q): %v", messageID, err)
	}
	anchor, err := model.NewMessageAnchor(chatID, id, sentAt, fromMe)
	if err != nil {
		t.Fatalf("NewMessageAnchor: %v", err)
	}
	return anchor
}

func planIDs(t *testing.T, plan model.PrunePlan) []string {
	t.Helper()
	ids := make([]string, plan.DeleteLen())
	for index := range ids {
		id, ok := plan.DeleteAt(index)
		if !ok {
			t.Fatalf("PrunePlan.DeleteAt(%d) failed", index)
		}
		ids[index] = id.String()
	}
	return ids
}

func snapshotIDs(t *testing.T, snapshot model.RetentionSnapshot) []string {
	t.Helper()
	ids := make([]string, snapshot.Len())
	for index := range ids {
		summary, ok := snapshot.At(index)
		if !ok {
			t.Fatalf("RetentionSnapshot.At(%d) failed", index)
		}
		ids[index] = summary.MessageID().String()
	}
	return ids
}

func assertPlansEqual(t *testing.T, left, right model.PrunePlan) {
	t.Helper()
	if left.ChatID() != right.ChatID() || !equalStrings(planIDs(t, left), planIDs(t, right)) {
		t.Fatalf("plans differ: %v versus %v", planIDs(t, left), planIDs(t, right))
	}
	leftAnchor, leftOK := left.ReplacementAnchor()
	rightAnchor, rightOK := right.ReplacementAnchor()
	if leftOK != rightOK {
		t.Fatalf("replacement presence differs: %t versus %t", leftOK, rightOK)
	}
	if leftOK && (leftAnchor.ChatID() != rightAnchor.ChatID() ||
		leftAnchor.MessageID() != rightAnchor.MessageID() ||
		!leftAnchor.SentAt().Equal(rightAnchor.SentAt()) ||
		leftAnchor.FromMe() != rightAnchor.FromMe()) {
		t.Fatalf("replacement anchors differ: %#v versus %#v", leftAnchor, rightAnchor)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func requireModelValidationError(
	t *testing.T,
	err error,
	wantCode model.ValidationCode,
	wantField string,
	wantLimit int,
) *model.ValidationError {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want *model.ValidationError")
	}
	var validationErr *model.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *model.ValidationError", err)
	}
	if validationErr.Code != wantCode || validationErr.Field != wantField ||
		validationErr.Limit != wantLimit {
		t.Fatalf(
			"ValidationError = (%q, %q, %d), want (%q, %q, %d)",
			validationErr.Code,
			validationErr.Field,
			validationErr.Limit,
			wantCode,
			wantField,
			wantLimit,
		)
	}
	return validationErr
}
