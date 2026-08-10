package model

import (
	"reflect"
	"testing"
	"time"
)

func TestMessageSummaryContainsOnlyBoundedMetadata(t *testing.T) {
	t.Parallel()

	sentAt := time.Unix(400, 0).UTC()
	message := storeTestMessage(t, "message-a", "body bytes", true, sentAt)
	summary := NewMessageSummary(message)
	if summary.ChatID() != message.ChatID() ||
		summary.MessageID() != message.MessageID() ||
		!summary.SentAt().Equal(sentAt) ||
		!summary.FromMe() ||
		!summary.BodyRetained() ||
		summary.BodyBytes() != len("body bytes") {
		t.Fatalf(
			"MessageSummary = (%q, %q, %v, %t, %t, %d)",
			summary.ChatID().String(),
			summary.MessageID().String(),
			summary.SentAt(),
			summary.FromMe(),
			summary.BodyRetained(),
			summary.BodyBytes(),
		)
	}
	bodyless := NewMessageSummary(message.WithoutBody())
	if bodyless.BodyRetained() || bodyless.BodyBytes() != 0 {
		t.Fatalf("bodyless summary = retained %t bytes %d", bodyless.BodyRetained(), bodyless.BodyBytes())
	}

	typeOf := reflect.TypeOf(MessageSummary{})
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		if field.Type.Kind() == reflect.String {
			t.Fatalf("MessageSummary contains arbitrary string field %q", field.Name)
		}
	}
	for index := 0; index < typeOf.NumMethod(); index++ {
		method := typeOf.Method(index)
		for output := 0; output < method.Type.NumOut(); output++ {
			if method.Type.Out(output).Kind() == reflect.String {
				t.Fatalf("MessageSummary method %q exposes an arbitrary string", method.Name)
			}
		}
	}
}

func TestCacheUsageValidationAndAccessors(t *testing.T) {
	t.Parallel()

	zero, err := NewCacheUsage(0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("NewCacheUsage(zero): %v", err)
	}
	if err := zero.Validate(); err != nil {
		t.Fatalf("constructed zero CacheUsage.Validate(): %v", err)
	}
	if zero.EstimatedBytes() != 0 || zero.Chats() != 0 || zero.Messages() != 0 ||
		zero.Bodies() != 0 || zero.Anchors() != 0 {
		t.Fatalf("zero CacheUsage = %#v", zero)
	}

	usage, err := NewCacheUsage(1234, 1, 2, 3, 4)
	if err != nil {
		t.Fatalf("NewCacheUsage: %v", err)
	}
	if usage.EstimatedBytes() != 1234 || usage.Chats() != 1 ||
		usage.Messages() != 2 || usage.Bodies() != 3 || usage.Anchors() != 4 {
		t.Fatalf("CacheUsage accessors = (%d, %d, %d, %d, %d)", usage.EstimatedBytes(), usage.Chats(), usage.Messages(), usage.Bodies(), usage.Anchors())
	}

	tests := []struct {
		name  string
		field string
		make  func() error
	}{
		{name: "estimated bytes", field: "estimated_bytes", make: func() error { _, err := NewCacheUsage(-1, 0, 0, 0, 0); return err }},
		{name: "chats", field: "chats", make: func() error { _, err := NewCacheUsage(0, -1, 0, 0, 0); return err }},
		{name: "messages", field: "messages", make: func() error { _, err := NewCacheUsage(0, 0, -1, 0, 0); return err }},
		{name: "bodies", field: "bodies", make: func() error { _, err := NewCacheUsage(0, 0, 0, -1, 0); return err }},
		{name: "anchors", field: "anchors", make: func() error { _, err := NewCacheUsage(0, 0, 0, 0, -1); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireValidationError(t, test.make(), InvalidValue, test.field, 0)
		})
	}

	requireValidationError(t, (CacheUsage{}).Validate(), Required, "cache_usage", 0)
	const maxInt = int(^uint(0) >> 1)
	const maxInt64 = int64(^uint64(0) >> 1)
	maximum, err := NewCacheUsage(maxInt64, maxInt, maxInt, maxInt, maxInt)
	if err != nil {
		t.Fatalf("NewCacheUsage(maximum representable): %v", err)
	}
	if maximum.EstimatedBytes() != maxInt64 || maximum.Anchors() != maxInt {
		t.Fatal("CacheUsage narrowed or wrapped a representable maximum")
	}
}

func TestRetentionDecisionEnums(t *testing.T) {
	t.Parallel()

	actions := []RetentionAction{KeepBody, KeepMetadata, Discard}
	reasons := []RetentionReason{
		RetentionEligible,
		RetentionTooOld,
		RetentionMessageCountLimit,
		RetentionCacheLimit,
	}
	for _, action := range actions {
		for _, reason := range reasons {
			decision, err := NewRetentionDecision(action, reason)
			if err != nil {
				t.Fatalf("NewRetentionDecision(%d, %d): %v", action, reason, err)
			}
			if decision.Action() != action || decision.Reason() != reason {
				t.Fatalf("decision = (%d, %d), want (%d, %d)", decision.Action(), decision.Reason(), action, reason)
			}
		}
	}
	requireValidationError(
		t,
		retentionDecisionError(0, RetentionEligible),
		Required,
		"retention_action",
		0,
	)
	requireValidationError(
		t,
		retentionDecisionError(RetentionAction(255), RetentionEligible),
		InvalidValue,
		"retention_action",
		0,
	)
	requireValidationError(
		t,
		retentionDecisionError(KeepBody, 0),
		Required,
		"retention_reason",
		0,
	)
	requireValidationError(
		t,
		retentionDecisionError(KeepBody, RetentionReason(255)),
		InvalidValue,
		"retention_reason",
		0,
	)
}

func TestRetentionSnapshotEmptyAndOptionalAnchor(t *testing.T) {
	t.Parallel()

	chatID := retentionTestChatID(t, "chat-a")
	empty, err := NewRetentionSnapshot(chatID, nil, nil)
	if err != nil {
		t.Fatalf("NewRetentionSnapshot(empty): %v", err)
	}
	if empty.ChatID() != chatID || empty.Len() != 0 {
		t.Fatalf("empty snapshot = chat %q len %d", empty.ChatID().String(), empty.Len())
	}
	if _, ok := empty.At(0); ok {
		t.Fatal("empty snapshot At(0) reported a value")
	}
	if _, ok := empty.Anchor(); ok {
		t.Fatal("empty snapshot reported an anchor")
	}

	anchor := retentionTestAnchor(t, "chat-a", "anchor-a", time.Unix(10, 0), true)
	withAnchor, err := NewRetentionSnapshot(chatID, nil, &anchor)
	if err != nil {
		t.Fatalf("NewRetentionSnapshot(anchor): %v", err)
	}
	anchor.messageID = retentionTestMessageID(t, "changed")
	got, ok := withAnchor.Anchor()
	if !ok || got.MessageID().String() != "anchor-a" || !got.FromMe() {
		t.Fatalf("snapshot anchor = (%q, %t, %t)", got.MessageID().String(), got.FromMe(), ok)
	}
	got.messageID = retentionTestMessageID(t, "returned-change")
	again, ok := withAnchor.Anchor()
	if !ok || again.MessageID().String() != "anchor-a" {
		t.Fatal("mutating returned anchor changed snapshot")
	}
}

func TestRetentionSnapshotBoundsAndDefensiveCopy(t *testing.T) {
	t.Parallel()

	chatID := retentionTestChatID(t, "chat-a")
	summaries := make([]MessageSummary, MaxRetentionSnapshotSummaries)
	for index := range summaries {
		summaries[index] = retentionTestSummary(
			t,
			"message-"+threeDigit(index),
			time.Unix(int64(index+1), 0),
			true,
		)
	}
	first := summaries[0]
	snapshot, err := NewRetentionSnapshot(chatID, summaries, nil)
	if err != nil {
		t.Fatalf("NewRetentionSnapshot(551): %v", err)
	}
	if snapshot.Len() != MaxRetentionSnapshotSummaries {
		t.Fatalf("snapshot.Len() = %d, want %d", snapshot.Len(), MaxRetentionSnapshotSummaries)
	}
	summaries[0] = summaries[1]
	got, ok := snapshot.At(0)
	if !ok || got.MessageID() != first.MessageID() {
		t.Fatalf("source mutation changed snapshot: (%q, %t)", got.MessageID().String(), ok)
	}
	got.messageID = retentionTestMessageID(t, "returned-change")
	again, _ := snapshot.At(0)
	if again.MessageID() != first.MessageID() {
		t.Fatal("mutating At result changed snapshot")
	}
	for _, index := range []int{-1, MaxRetentionSnapshotSummaries} {
		if _, ok := snapshot.At(index); ok {
			t.Errorf("RetentionSnapshot.At(%d) reported a value", index)
		}
	}

	tooMany := append(summaries, summaries[0])
	_, err = NewRetentionSnapshot(chatID, tooMany, nil)
	requireValidationError(
		t,
		err,
		SizeExceeded,
		"retention_snapshot_summaries",
		MaxRetentionSnapshotSummaries,
	)
}

func TestRetentionSnapshotRejectsInvalidRelationships(t *testing.T) {
	t.Parallel()

	chatID := retentionTestChatID(t, "chat-a")
	otherSummary := storeTestMessage(t, "message-a", "body", false, time.Unix(1, 0))
	otherSummary.chatID = retentionTestChatID(t, "chat-b")
	_, err := NewRetentionSnapshot(chatID, []MessageSummary{NewMessageSummary(otherSummary)}, nil)
	requireValidationError(t, err, InvalidValue, "summary_chat_id", 0)

	_, err = NewRetentionSnapshot(chatID, []MessageSummary{{}}, nil)
	requireValidationError(t, err, Required, "chat_id", 0)

	otherAnchor := retentionTestAnchor(t, "chat-b", "anchor-a", time.Unix(1, 0), false)
	_, err = NewRetentionSnapshot(chatID, nil, &otherAnchor)
	requireValidationError(t, err, InvalidValue, "anchor_chat_id", 0)

	_, err = NewRetentionSnapshot(ChatID{}, nil, nil)
	requireValidationError(t, err, Required, "chat_id", 0)
}

func TestRetentionSnapshotRejectsMalformedSummaryAccounting(t *testing.T) {
	t.Parallel()

	chatID := retentionTestChatID(t, "chat-a")
	valid := retentionTestSummary(t, "message-a", time.Unix(1, 0), true)
	tests := []struct {
		name   string
		mutate func(*MessageSummary)
		limit  int
	}{
		{
			name:   "negative body bytes",
			mutate: func(summary *MessageSummary) { summary.bodyBytes = -1 },
			limit:  MaxRetainedTextBytes,
		},
		{
			name:   "excessive body bytes",
			mutate: func(summary *MessageSummary) { summary.bodyBytes = MaxRetainedTextBytes + 1 },
			limit:  MaxRetainedTextBytes,
		},
		{
			name: "bodyless bytes",
			mutate: func(summary *MessageSummary) {
				summary.bodyRetained = false
				summary.bodyBytes = 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := valid
			test.mutate(&summary)
			_, err := NewRetentionSnapshot(chatID, []MessageSummary{summary}, nil)
			requireValidationError(t, err, InvalidValue, "body_bytes", test.limit)
		})
	}
}

func TestPrunePlanBoundsCopyAndReplacement(t *testing.T) {
	t.Parallel()

	chatID := retentionTestChatID(t, "chat-a")
	ids := make([]MessageID, MaxPrunePlanDeletes)
	for index := range ids {
		ids[index] = retentionTestMessageID(t, "message-"+threeDigit(index))
	}
	first := ids[0]
	replacement := retentionTestAnchor(t, "chat-a", "anchor-a", time.Unix(5, 0), false)
	plan, err := NewPrunePlan(chatID, ids, &replacement)
	if err != nil {
		t.Fatalf("NewPrunePlan(551): %v", err)
	}
	if plan.ChatID() != chatID || plan.DeleteLen() != MaxPrunePlanDeletes {
		t.Fatalf("plan = chat %q deletes %d", plan.ChatID().String(), plan.DeleteLen())
	}
	ids[0] = ids[1]
	got, ok := plan.DeleteAt(0)
	if !ok || got != first {
		t.Fatalf("source mutation changed plan: (%q, %t)", got.String(), ok)
	}
	for _, index := range []int{-1, MaxPrunePlanDeletes} {
		if _, ok := plan.DeleteAt(index); ok {
			t.Errorf("PrunePlan.DeleteAt(%d) reported a value", index)
		}
	}
	replacement.messageID = retentionTestMessageID(t, "changed")
	anchor, ok := plan.ReplacementAnchor()
	if !ok || anchor.MessageID().String() != "anchor-a" {
		t.Fatalf("replacement = (%q, %t)", anchor.MessageID().String(), ok)
	}
	anchor.messageID = retentionTestMessageID(t, "returned-change")
	again, ok := plan.ReplacementAnchor()
	if !ok || again.MessageID().String() != "anchor-a" {
		t.Fatal("mutating returned replacement changed plan")
	}

	tooMany := append(ids, ids[0])
	_, err = NewPrunePlan(chatID, tooMany, nil)
	requireValidationError(t, err, SizeExceeded, "prune_plan_deletes", MaxPrunePlanDeletes)
	_, err = NewPrunePlan(chatID, []MessageID{{}}, nil)
	requireValidationError(t, err, Required, "message_id", 0)
	other := retentionTestAnchor(t, "chat-b", "anchor-a", time.Unix(1, 0), false)
	_, err = NewPrunePlan(chatID, nil, &other)
	requireValidationError(t, err, InvalidValue, "replacement_anchor_chat_id", 0)
}

func TestPruneResultValidation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		deleted, removed int
		replaced         bool
	}{
		{},
		{deleted: 7, removed: 5, replaced: true},
	} {
		result, err := NewPruneResult(test.deleted, test.removed, test.replaced)
		if err != nil {
			t.Fatalf("NewPruneResult: %v", err)
		}
		if result.DeletedRows() != test.deleted || result.RemovedBodies() != test.removed ||
			result.AnchorReplaced() != test.replaced {
			t.Fatalf("PruneResult = (%d, %d, %t)", result.DeletedRows(), result.RemovedBodies(), result.AnchorReplaced())
		}
	}
	requireValidationError(
		t,
		pruneResultError(-1, 0),
		InvalidValue,
		"deleted_rows",
		0,
	)
	requireValidationError(
		t,
		pruneResultError(0, -1),
		InvalidValue,
		"removed_bodies",
		0,
	)
}

func TestNewModelValuesExposeNoMutableFields(t *testing.T) {
	t.Parallel()

	values := []any{
		Chat{}, Cursor{}, MessageAnchor{}, WriteBatch{}, MessageSummary{},
		CacheUsage{}, RetentionDecision{}, RetentionSnapshot{}, PrunePlan{},
		PruneResult{},
	}
	for _, value := range values {
		typeOf := reflect.TypeOf(value)
		for index := 0; index < typeOf.NumField(); index++ {
			if typeOf.Field(index).IsExported() {
				t.Errorf("%s exposes mutable field %q", typeOf.Name(), typeOf.Field(index).Name)
			}
		}
		for index := 0; index < typeOf.NumMethod(); index++ {
			method := typeOf.Method(index)
			for output := 0; output < method.Type.NumOut(); output++ {
				kind := method.Type.Out(output).Kind()
				if kind == reflect.Slice || kind == reflect.Map || kind == reflect.Pointer {
					t.Errorf("%s.%s exposes mutable %s output", typeOf.Name(), method.Name, kind)
				}
			}
		}
	}
}

func retentionDecisionError(action RetentionAction, reason RetentionReason) error {
	_, err := NewRetentionDecision(action, reason)
	return err
}

func pruneResultError(deleted, removed int) error {
	_, err := NewPruneResult(deleted, removed, false)
	return err
}

func retentionTestChatID(t *testing.T, value string) ChatID {
	t.Helper()
	id, err := NewChatID(value)
	if err != nil {
		t.Fatalf("NewChatID(%q): %v", value, err)
	}
	return id
}

func retentionTestMessageID(t *testing.T, value string) MessageID {
	t.Helper()
	id, err := NewMessageID(value)
	if err != nil {
		t.Fatalf("NewMessageID(%q): %v", value, err)
	}
	return id
}

func retentionTestAnchor(
	t *testing.T,
	chatID string,
	messageID string,
	sentAt time.Time,
	fromMe bool,
) MessageAnchor {
	t.Helper()
	anchor, err := NewMessageAnchor(
		retentionTestChatID(t, chatID),
		retentionTestMessageID(t, messageID),
		sentAt,
		fromMe,
	)
	if err != nil {
		t.Fatalf("NewMessageAnchor: %v", err)
	}
	return anchor
}

func retentionTestSummary(
	t *testing.T,
	messageID string,
	sentAt time.Time,
	bodyRetained bool,
) MessageSummary {
	t.Helper()
	message := storeTestMessage(t, messageID, "body", false, sentAt)
	if !bodyRetained {
		message = message.WithoutBody()
	}
	return NewMessageSummary(message)
}

func threeDigit(value int) string {
	return string([]byte{
		'0' + byte(value/100),
		'0' + byte(value/10%10),
		'0' + byte(value%10),
	})
}
