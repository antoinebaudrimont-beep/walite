package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

var testTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestNewMemoryBounds(t *testing.T) {
	for _, value := range []int{0, -1} {
		if _, err := NewMemory(value); err == nil {
			t.Fatalf("NewMemory(%d) succeeded", value)
		}
	}
	if _, err := NewMemory(1); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureChatCapacityAndPlaceholderProtection(t *testing.T) {
	store, _ := NewMemory(1)
	real := mustChat(t, "chat-a", "Alice", false)
	if err := store.EnsureChat(context.Background(), real); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureChat(context.Background(), mustChat(t, "chat-a", "", true)); err != nil {
		t.Fatal(err)
	}
	if got := store.chats["chat-a"].chat.DisplayName(); got != "Alice" {
		t.Fatalf("name = %q", got)
	}
	if err := store.EnsureChat(context.Background(), mustChat(t, "chat-b", "Bob", false)); err == nil {
		t.Fatal("capacity write succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.EnsureChat(ctx, real); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if err := store.EnsureChat(context.Background(), model.Chat{}); err == nil {
		t.Fatal("malformed chat succeeded")
	}
}

func TestWriteUpsertBodyAndConflict(t *testing.T) {
	store, _ := NewMemory(2)
	body := mustMessage(t, "chat-a", "id", testTime, false, "body")
	write(t, store, body)
	write(t, store, body.WithoutBody())
	page, _, err := store.Page(context.Background(), body.ChatID(), model.NoCursor(), 10)
	if err != nil || len(page) != 1 || page[0].Text() != "body" {
		t.Fatalf("page = %#v, %v", page, err)
	}
	metadata := mustMessage(t, "chat-a", "meta", testTime.Add(-time.Second), false, "").WithoutBody()
	write(t, store, metadata)
	write(t, store, mustMessage(t, "chat-a", "meta", testTime.Add(-time.Second), false, "upgraded"))
	conflict := mustMessage(t, "chat-a", "id", testTime.Add(time.Second), false, "changed")
	batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{conflict})
	if err := store.Write(context.Background(), batch); err == nil {
		t.Fatal("timestamp conflict succeeded")
	}
}

func TestWriteBoundAndTransactionalCapacity(t *testing.T) {
	store, _ := NewMemory(2)
	for start := 0; start < model.MaxRetentionSnapshotSummaries; start += model.MaxWriteBatchMessages {
		end := start + model.MaxWriteBatchMessages
		if end > model.MaxRetentionSnapshotSummaries {
			end = model.MaxRetentionSnapshotSummaries
		}
		messages := make([]model.Message, 0, end-start)
		for i := start; i < end; i++ {
			messages = append(messages, mustMessage(t, "chat-a", messageID(i), testTime.Add(-time.Duration(i)*time.Second), false, "x"))
		}
		batch, _ := model.NewWriteBatch(model.WriteHistory, messages)
		if err := store.Write(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	extra := mustMessage(t, "chat-a", "extra", testTime.Add(time.Hour), false, "x")
	other := mustMessage(t, "chat-b", "other", testTime, false, "x")
	batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{extra, other})
	if err := store.Write(context.Background(), batch); err == nil {
		t.Fatal("overflow succeeded")
	}
	usage, _ := store.Usage(context.Background())
	if usage.Chats() != 1 || usage.Messages() != 551 {
		t.Fatalf("usage = %d/%d", usage.Chats(), usage.Messages())
	}
	write(t, store, mustMessage(t, "chat-a", messageID(0), testTime, false, "replacement"))
}

func TestPageOrderingCursorAndDefensiveSlice(t *testing.T) {
	store, _ := NewMemory(1)
	chat := mustID(t, "chat-a")
	write(t, store,
		mustMessage(t, "chat-a", "b", testTime, false, "b"),
		mustMessage(t, "chat-a", "a", testTime, false, "a"),
		mustMessage(t, "chat-a", "c", testTime.Add(-time.Second), false, "c"))
	first, next, err := store.Page(context.Background(), chat, model.NoCursor(), 1)
	if err != nil || len(first) != 1 || first[0].MessageID().String() != "b" || next.IsZero() {
		t.Fatalf("first page invalid: %#v %#v %v", first, next, err)
	}
	second, next, _ := store.Page(context.Background(), chat, next, 1000)
	if len(second) != 2 || second[0].MessageID().String() != "a" || !next.IsZero() {
		t.Fatalf("second page invalid: %#v", second)
	}
	first[0] = model.Message{}
	again, _, _ := store.Page(context.Background(), chat, model.NoCursor(), 1)
	if again[0].MessageID().String() != "b" {
		t.Fatal("caller mutated store")
	}
	synthetic, _ := model.NewCursor(testTime, mustMessageID(t, "ab"))
	rows, _, _ := store.Page(context.Background(), chat, synthetic, 10)
	if len(rows) != 2 {
		t.Fatalf("synthetic boundary returned %d", len(rows))
	}
	if _, _, err := store.Page(context.Background(), chat, model.NoCursor(), 0); err == nil {
		t.Fatal("zero limit succeeded")
	}
}

func TestSnapshotPruneAnchorAndUsage(t *testing.T) {
	store, _ := NewMemory(1)
	first := mustMessage(t, "chat-a", "b", testTime, false, "body")
	second := mustMessage(t, "chat-a", "a", testTime, true, "").WithoutBody()
	write(t, store, first, second)
	anchor, _ := model.NewMessageAnchor(first.ChatID(), first.MessageID(), first.SentAt(), first.FromMe())
	plan, _ := model.NewPrunePlan(first.ChatID(), []model.MessageID{first.MessageID(), first.MessageID(), second.MessageID()}, &anchor)
	result, err := store.ApplyPrune(context.Background(), plan)
	if err != nil || result.DeletedRows() != 2 || result.RemovedBodies() != 1 || !result.AnchorReplaced() {
		t.Fatalf("result = %#v %v", result, err)
	}
	repeated, _ := store.ApplyPrune(context.Background(), plan)
	if repeated.DeletedRows() != 0 || repeated.AnchorReplaced() {
		t.Fatalf("repeat = %#v", repeated)
	}
	snapshot, _ := store.RetentionSnapshot(context.Background(), first.ChatID())
	if snapshot.Len() != 0 {
		t.Fatalf("snapshot len = %d", snapshot.Len())
	}
	if _, ok := snapshot.Anchor(); !ok {
		t.Fatal("anchor absent")
	}
	usage, _ := store.Usage(context.Background())
	wantBytes := int64(chatEnvelopeBytes + len("chat-a") + anchorEnvelopeBytes + len("chat-a") + len("b"))
	if usage.Chats() != 1 || usage.Messages() != 0 || usage.Anchors() != 1 || usage.EstimatedBytes() != wantBytes {
		t.Fatalf("usage = %+v bytes=%d", usage, usage.EstimatedBytes())
	}
}

func TestConcurrentAccess(t *testing.T) {
	store, _ := NewMemory(4)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			message := mustMessage(t, "chat-a", "same", testTime, false, "body")
			batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{message})
			_ = store.Write(context.Background(), batch)
			_, _, _ = store.Page(context.Background(), message.ChatID(), model.NoCursor(), 10)
			_, _ = store.Usage(context.Background())
		}()
	}
	group.Wait()
	usage, _ := store.Usage(context.Background())
	if usage.Messages() != 1 {
		t.Fatalf("messages = %d", usage.Messages())
	}
}

func TestEnsureChatUpgradeExactCapacityAndPrivateErrors(t *testing.T) {
	store, _ := NewMemory(2)
	placeholder := mustChat(t, "chat-private-marker", "", true)
	if err := store.EnsureChat(context.Background(), placeholder); err != nil {
		t.Fatal(err)
	}
	real := mustChat(t, "chat-private-marker", "name-private-marker", false)
	if err := store.EnsureChat(context.Background(), real); err != nil {
		t.Fatal(err)
	}
	stored := store.chats[real.ID().String()].chat
	if stored.Placeholder() || stored.DisplayName() != "name-private-marker" {
		t.Fatalf("placeholder was not upgraded: placeholder=%t name=%q", stored.Placeholder(), stored.DisplayName())
	}
	if err := store.EnsureChat(context.Background(), placeholder); err != nil {
		t.Fatal(err)
	}
	stored = store.chats[real.ID().String()].chat
	if stored.Placeholder() || stored.DisplayName() != "name-private-marker" {
		t.Fatal("real metadata was downgraded")
	}
	if err := store.EnsureChat(context.Background(), mustChat(t, "chat-second", "second", false)); err != nil {
		t.Fatalf("exact maxChats failed: %v", err)
	}
	err := store.EnsureChat(context.Background(), mustChat(t, "chat-overflow-marker", "overflow-name-marker", false))
	requirePrivateFree(t, err, "chat-overflow-marker", "overflow-name-marker")
	err = store.EnsureChat(context.Background(), model.Chat{})
	requirePrivateFree(t, err, "chat-private-marker", "name-private-marker")
}

func TestWriteEmptyPlaceholderMultiChatCancellationAndMalformed(t *testing.T) {
	store, _ := NewMemory(2)
	empty, err := model.NewWriteBatch(model.WriteRealtime, nil)
	if err != nil || store.Write(context.Background(), empty) != nil {
		t.Fatalf("empty batch failed: %v", err)
	}
	usage, _ := store.Usage(context.Background())
	if usage.Chats() != 0 || usage.Messages() != 0 {
		t.Fatalf("empty batch changed usage: chats=%d messages=%d", usage.Chats(), usage.Messages())
	}
	one := mustMessage(t, "auto-chat", "auto-message", testTime, false, "one")
	two := mustMessage(t, "other-chat", "other-message", testTime, true, "two")
	batch, _ := model.NewWriteBatch(model.WriteHistory, []model.Message{one, two})
	if err := store.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"auto-chat", "other-chat"} {
		chat := store.chats[id]
		if chat == nil || !chat.chat.Placeholder() || chat.chat.DisplayName() != "" {
			t.Fatalf("chat %q is not a bounded placeholder", id)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Write(ctx, batch); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Write error = %v", err)
	}
	if err := store.Write(context.Background(), model.WriteBatch{}); err == nil {
		t.Fatal("zero batch succeeded")
	}
}

func TestWriteIdempotencyUpgradeDirectionAndTransactionalRejection(t *testing.T) {
	store, _ := NewMemory(2)
	retained := mustMessage(t, "full-chat", "message-private-marker", testTime, false, "body-private-marker")
	write(t, store, retained)
	write(t, store, retained)
	write(t, store, retained.WithoutBody())
	usage, _ := store.Usage(context.Background())
	if usage.Messages() != 1 || usage.Bodies() != 1 {
		t.Fatalf("duplicate/bodyless usage = messages %d bodies %d", usage.Messages(), usage.Bodies())
	}
	bodyless := mustMessage(t, "full-chat", "upgrade", testTime.Add(-time.Second), false, "discarded").WithoutBody()
	write(t, store, bodyless)
	write(t, store, mustMessage(t, "full-chat", "upgrade", testTime.Add(-time.Second), false, "resulting-body"))
	rows, _, _ := store.Page(context.Background(), retained.ChatID(), model.NoCursor(), 10)
	found := false
	for _, row := range rows {
		if row.MessageID().String() == "upgrade" {
			found = row.BodyRetained() && row.Text() == "resulting-body"
		}
	}
	if !found {
		t.Fatal("retained-body duplicate did not upgrade bodyless row")
	}
	direction := mustMessage(t, "full-chat", "message-private-marker", testTime, true, "body-private-marker")
	batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{direction})
	err := store.Write(context.Background(), batch)
	requirePrivateFree(t, err, "full-chat", "message-private-marker", "body-private-marker")

	fillChat(t, store, "full-chat", 2, model.MaxRetentionSnapshotSummaries)
	other := mustMessage(t, "other-chat", "must-not-commit", testTime.Add(time.Hour), false, "must-not-commit-body")
	overflow := mustMessage(t, "full-chat", "overflow-message-marker", testTime.Add(2*time.Hour), false, "overflow-body-marker")
	batch, _ = model.NewWriteBatch(model.WriteRealtime, []model.Message{other, overflow})
	err = store.Write(context.Background(), batch)
	requirePrivateFree(t, err, "other-chat", "must-not-commit", "must-not-commit-body", "overflow-message-marker", "overflow-body-marker")
	unknownRows, _, _ := store.Page(context.Background(), other.ChatID(), model.NoCursor(), 10)
	if len(unknownRows) != 0 {
		t.Fatal("transactional rejection committed mutation for another chat")
	}
	overwrite := mustMessage(t, "full-chat", "message-private-marker", testTime, false, "replacement-at-capacity")
	write(t, store, overwrite)
	rows, _, _ = store.Page(context.Background(), retained.ChatID(), model.NoCursor(), 100)
	if len(rows) != 100 {
		t.Fatalf("clamped full page len = %d", len(rows))
	}
}

func TestPageUnknownLimitsOrderingAndCancellation(t *testing.T) {
	store, _ := NewMemory(1)
	unknown := mustID(t, "unknown-chat")
	rows, next, err := store.Page(context.Background(), unknown, model.NoCursor(), 10)
	if err != nil || rows == nil || len(rows) != 0 || !next.IsZero() {
		t.Fatalf("unknown page = %#v cursor=%#v err=%v", rows, next, err)
	}
	for _, limit := range []int{0, -1} {
		if _, _, err := store.Page(context.Background(), unknown, model.NoCursor(), limit); err == nil {
			t.Fatalf("limit %d succeeded", limit)
		}
	}
	fillChat(t, store, "page-chat", 0, 105)
	chat := mustID(t, "page-chat")
	rows, next, err = store.Page(context.Background(), chat, model.NoCursor(), 100)
	if err != nil || len(rows) != 100 || next.IsZero() {
		t.Fatalf("limit 100 = len %d cursor zero %t err %v", len(rows), next.IsZero(), err)
	}
	clamped, clampedNext, _ := store.Page(context.Background(), chat, model.NoCursor(), 101)
	if len(clamped) != 100 || clampedNext.IsZero() {
		t.Fatalf("clamped page = len %d cursor zero %t", len(clamped), clampedNext.IsZero())
	}
	last, lastNext, _ := store.Page(context.Background(), chat, next, 100)
	if len(last) != 5 || !lastNext.IsZero() {
		t.Fatalf("last page = len %d cursor zero %t", len(last), lastNext.IsZero())
	}
	for index := 1; index < len(rows); index++ {
		if !newer(rows[index-1], rows[index]) {
			t.Fatalf("noncanonical order at %d", index)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.Page(ctx, chat, model.NoCursor(), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Page error = %v", err)
	}
}

func TestPageWalkExactlyOnceAndEqualTimestampTieBreak(t *testing.T) {
	store, _ := NewMemory(1)
	messages := []model.Message{
		mustMessage(t, "walk-chat", "tie-c", testTime.Add(time.Hour), false, "c"),
		mustMessage(t, "walk-chat", "tie-a", testTime.Add(time.Hour), false, "a"),
		mustMessage(t, "walk-chat", "tie-b", testTime.Add(time.Hour), false, "b"),
	}
	for index := 0; index < 20; index++ {
		messages = append(messages, mustMessage(t, "walk-chat", messageID(index), testTime.Add(-time.Duration(index)*time.Second), false, "x"))
	}
	write(t, store, messages...)
	chat := mustID(t, "walk-chat")
	seen := make(map[string]bool, len(messages))
	cursor := model.NoCursor()
	var order []string
	for {
		rows, next, err := store.Page(context.Background(), chat, cursor, 4)
		if err != nil || len(rows) == 0 {
			t.Fatalf("walk page = len %d err %v", len(rows), err)
		}
		for _, row := range rows {
			id := row.MessageID().String()
			if seen[id] {
				t.Fatalf("duplicate row %q", id)
			}
			seen[id] = true
			order = append(order, id)
		}
		if next.IsZero() {
			break
		}
		cursor = next
	}
	if len(seen) != len(messages) || strings.Join(order[:3], ",") != "tie-c,tie-b,tie-a" {
		t.Fatalf("walk count/order = %d %v", len(seen), order[:3])
	}
}

func TestRetentionSnapshotUnknownOrderingBodyFreeBoundAndCancellation(t *testing.T) {
	store, _ := NewMemory(1)
	unknown := mustID(t, "snapshot-unknown")
	snapshot, err := store.RetentionSnapshot(context.Background(), unknown)
	if err != nil || snapshot.ChatID().String() != unknown.String() || snapshot.Len() != 0 {
		t.Fatalf("unknown snapshot = chat %q len %d err %v", snapshot.ChatID().String(), snapshot.Len(), err)
	}
	fillChat(t, store, "snapshot-chat", 0, model.MaxRetentionSnapshotSummaries)
	chat := mustID(t, "snapshot-chat")
	snapshot, err = store.RetentionSnapshot(context.Background(), chat)
	if err != nil || snapshot.Len() != model.MaxRetentionSnapshotSummaries {
		t.Fatalf("snapshot len = %d err %v", snapshot.Len(), err)
	}
	for index := 0; index < snapshot.Len(); index++ {
		summary, ok := snapshot.At(index)
		if !ok || summary.BodyBytes() != 1 || !summary.BodyRetained() {
			t.Fatalf("summary %d leaked/omitted body metadata", index)
		}
		if index > 0 {
			prior, _ := snapshot.At(index - 1)
			if prior.SentAt().Before(summary.SentAt()) {
				t.Fatalf("snapshot not descending at %d", index)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.RetentionSnapshot(ctx, chat); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled snapshot error = %v", err)
	}
}

func TestApplyPruneNoopMalformedCancellationAndAccounting(t *testing.T) {
	store, _ := NewMemory(2)
	retained := mustMessage(t, "prune-chat", "retained", testTime, false, "body")
	bodyless := mustMessage(t, "prune-chat", "bodyless", testTime.Add(-time.Second), false, "discarded").WithoutBody()
	write(t, store, retained, bodyless)
	missing := mustMessageID(t, "missing")
	plan, _ := model.NewPrunePlan(retained.ChatID(), []model.MessageID{missing, bodyless.MessageID()}, nil)
	result, err := store.ApplyPrune(context.Background(), plan)
	if err != nil || result.DeletedRows() != 1 || result.RemovedBodies() != 0 {
		t.Fatalf("bodyless prune = rows %d bodies %d err %v", result.DeletedRows(), result.RemovedBodies(), err)
	}
	unknownID := mustID(t, "unknown-prune-chat")
	unknownPlan, _ := model.NewPrunePlan(unknownID, []model.MessageID{missing}, nil)
	result, err = store.ApplyPrune(context.Background(), unknownPlan)
	if err != nil || result.DeletedRows() != 0 || result.RemovedBodies() != 0 || result.AnchorReplaced() {
		t.Fatalf("unknown prune = %#v err %v", result, err)
	}
	if _, err := store.ApplyPrune(context.Background(), model.PrunePlan{}); err == nil {
		t.Fatal("malformed plan succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ApplyPrune(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prune error = %v", err)
	}
}

func TestUsageEmptyFormulaUpsertPruneSaturationAndCancellation(t *testing.T) {
	store, _ := NewMemory(1)
	usage, err := store.Usage(context.Background())
	if err != nil || usage.EstimatedBytes() != 0 || usage.Chats() != 0 || usage.Messages() != 0 || usage.Bodies() != 0 || usage.Anchors() != 0 {
		t.Fatalf("empty usage = bytes %d chats %d messages %d bodies %d anchors %d err %v", usage.EstimatedBytes(), usage.Chats(), usage.Messages(), usage.Bodies(), usage.Anchors(), err)
	}
	chat := mustChat(t, "usage-chat", "usage-name", false)
	if err := store.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	message := mustMessage(t, "usage-chat", "usage-message", testTime, false, "usage-body")
	write(t, store, message)
	write(t, store, message)
	anchor, _ := model.NewMessageAnchor(message.ChatID(), message.MessageID(), message.SentAt(), message.FromMe())
	plan, _ := model.NewPrunePlan(message.ChatID(), nil, &anchor)
	if _, err := store.ApplyPrune(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	usage, _ = store.Usage(context.Background())
	want := int64(chatEnvelopeBytes + len("usage-chat") + len("usage-name") + messageEnvelopeBytes + message.ByteSize() + anchorEnvelopeBytes + len("usage-chat") + len("usage-message"))
	if usage.EstimatedBytes() != want || usage.Chats() != 1 || usage.Messages() != 1 || usage.Bodies() != 1 || usage.Anchors() != 1 {
		t.Fatalf("usage formula/counts = bytes %d/%d chats %d messages %d bodies %d anchors %d", usage.EstimatedBytes(), want, usage.Chats(), usage.Messages(), usage.Bodies(), usage.Anchors())
	}
	deletePlan, _ := model.NewPrunePlan(message.ChatID(), []model.MessageID{message.MessageID()}, nil)
	if _, err := store.ApplyPrune(context.Background(), deletePlan); err != nil {
		t.Fatal(err)
	}
	pruned, _ := store.Usage(context.Background())
	if pruned.Messages() != 0 || pruned.Bodies() != 0 || pruned.EstimatedBytes() >= usage.EstimatedBytes() {
		t.Fatalf("pruned usage = messages %d bodies %d bytes %d", pruned.Messages(), pruned.Bodies(), pruned.EstimatedBytes())
	}
	if got := addSaturated(10, 20); got != 30 {
		t.Fatalf("normal saturated addition = %d", got)
	}
	if got := addSaturated(math.MaxInt64-1, 2); got != math.MaxInt64 {
		t.Fatalf("overflow addition = %d", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Usage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled usage error = %v", err)
	}
}

func TestConcurrentReadersAndDistinctChatWrites(t *testing.T) {
	store, _ := NewMemory(8)
	write(t, store, mustMessage(t, "seed-chat", "seed", testTime, false, "seed"))
	start := make(chan struct{})
	var group sync.WaitGroup
	errorsSeen := make(chan error, 32)
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, _, err := store.Page(context.Background(), mustID(t, "seed-chat"), model.NoCursor(), 10)
			if err == nil {
				_, err = store.RetentionSnapshot(context.Background(), mustID(t, "seed-chat"))
			}
			if err == nil {
				_, err = store.Usage(context.Background())
			}
			if err != nil {
				errorsSeen <- err
			}
		}()
	}
	for index := 0; index < 7; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			message := mustMessage(t, "distinct-"+messageID(index), "one", testTime, false, "body")
			batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{message})
			if err := store.Write(context.Background(), batch); err != nil {
				errorsSeen <- err
			}
		}()
	}
	close(start)
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Errorf("concurrent operation: %v", err)
	}
	usage, _ := store.Usage(context.Background())
	if usage.Chats() != 8 || usage.Messages() != 8 || usage.Bodies() != 8 {
		t.Fatalf("final distinct invariants = chats %d messages %d bodies %d", usage.Chats(), usage.Messages(), usage.Bodies())
	}
}

func TestConcurrentReadWhilePruningDeterministicFinalState(t *testing.T) {
	store, _ := NewMemory(1)
	fillChat(t, store, "prune-race", 0, 50)
	chat := mustID(t, "prune-race")
	deleteIDs := make([]model.MessageID, 25)
	for index := range deleteIDs {
		deleteIDs[index] = mustMessageID(t, messageID(index))
	}
	plan, _ := model.NewPrunePlan(chat, deleteIDs, nil)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := 0; index < 12; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for iteration := 0; iteration < 20; iteration++ {
				_, _, _ = store.Page(context.Background(), chat, model.NoCursor(), 100)
				_, _ = store.RetentionSnapshot(context.Background(), chat)
				_, _ = store.Usage(context.Background())
			}
		}()
	}
	group.Add(1)
	go func() { defer group.Done(); <-start; _, _ = store.ApplyPrune(context.Background(), plan) }()
	close(start)
	group.Wait()
	usage, _ := store.Usage(context.Background())
	rows, next, _ := store.Page(context.Background(), chat, model.NoCursor(), 100)
	if usage.Messages() != 25 || usage.Bodies() != 25 || len(rows) != 25 || !next.IsZero() {
		t.Fatalf("final prune invariants = messages %d bodies %d rows %d cursor zero %t", usage.Messages(), usage.Bodies(), len(rows), next.IsZero())
	}
}

func requirePrivateFree(t *testing.T, err error, markers ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	for _, marker := range markers {
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("error disclosed marker %q: %v", marker, err)
		}
	}
}

func fillChat(t *testing.T, store *Memory, chat string, start, end int) {
	t.Helper()
	for offset := start; offset < end; offset += model.MaxWriteBatchMessages {
		batchEnd := offset + model.MaxWriteBatchMessages
		if batchEnd > end {
			batchEnd = end
		}
		messages := make([]model.Message, 0, batchEnd-offset)
		for index := offset; index < batchEnd; index++ {
			messages = append(messages, mustMessage(t, chat, messageID(index), testTime.Add(-time.Duration(index)*time.Second), false, "x"))
		}
		write(t, store, messages...)
	}
}

func write(t *testing.T, store *Memory, messages ...model.Message) {
	t.Helper()
	batch, err := model.NewWriteBatch(model.WriteRealtime, messages)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
}
func mustChat(t *testing.T, id, name string, placeholder bool) model.Chat {
	t.Helper()
	value, err := model.NewChat(model.ChatInput{ID: id, DisplayName: name, Placeholder: placeholder})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func mustMessage(t *testing.T, chat, id string, sent time.Time, fromMe bool, text string) model.Message {
	t.Helper()
	value, err := model.NewMessage(model.MessageInput{ChatID: chat, MessageID: id, SentAt: sent, FromMe: fromMe, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func mustID(t *testing.T, value string) model.ChatID {
	t.Helper()
	id, err := model.NewChatID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustMessageID(t *testing.T, value string) model.MessageID {
	t.Helper()
	id, err := model.NewMessageID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func messageID(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "id-0"
	}
	result := ""
	for value > 0 {
		result = string(digits[value%10]) + result
		value /= 10
	}
	return "id-" + result
}
