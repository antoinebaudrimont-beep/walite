// Package store provides bounded persistence implementations.
package store

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const (
	maxPageMessages      = 100
	chatEnvelopeBytes    = 64
	messageEnvelopeBytes = 64
	anchorEnvelopeBytes  = 32
)

type failure uint8

const (
	rejected failure = iota + 1
	capacity
)

// Error is a fixed, content-free store failure.
type Error struct{ kind failure }

func (err *Error) Error() string {
	if err != nil && err.kind == capacity {
		return "store capacity exceeded"
	}
	return "store operation rejected"
}

// Memory is a deterministic in-memory store. The outer map is bounded by
// maxChats and every message map by model.MaxRetentionSnapshotSummaries.
type Memory struct {
	mu       sync.RWMutex
	maxChats int
	chats    map[string]*memoryChat
}

type memoryChat struct {
	chat      model.Chat
	messages  map[string]model.Message
	anchor    model.MessageAnchor
	hasAnchor bool
}

func NewMemory(maxChats int) (*Memory, error) {
	if maxChats <= 0 {
		return nil, &Error{kind: rejected}
	}
	return &Memory{maxChats: maxChats, chats: make(map[string]*memoryChat)}, nil
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return &Error{kind: rejected}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func normalizeChat(chat model.Chat) (model.Chat, error) {
	contactID := ""
	if chat.HasContact() {
		contactID = chat.ContactID().String()
	}
	return model.NewChat(model.ChatInput{
		ID:            chat.ID().String(),
		ContactID:     contactID,
		DisplayName:   chat.DisplayName(),
		IsGroup:       chat.IsGroup(),
		LastMessageAt: chat.LastMessageAt(),
		UnreadCount:   chat.UnreadCount(),
		Muted:         chat.Muted(),
		Archived:      chat.Archived(),
		Placeholder:   chat.Placeholder(),
		UpdatedAt:     chat.UpdatedAt(),
		IngestSeq:     chat.IngestSeq(),
	})
}

func (memory *Memory) EnsureChat(ctx context.Context, chat model.Chat) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	normalized, err := normalizeChat(chat)
	if err != nil {
		return &Error{kind: rejected}
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	key := normalized.ID().String()
	if current, ok := memory.chats[key]; ok {
		if !(normalized.Placeholder() && !current.chat.Placeholder()) {
			current.chat = normalized
		}
		return nil
	}
	if len(memory.chats) >= memory.maxChats {
		return &Error{kind: capacity}
	}
	memory.chats[key] = &memoryChat{chat: normalized, messages: make(map[string]model.Message, model.MaxRetentionSnapshotSummaries)}
	return nil
}

func (memory *Memory) Write(ctx context.Context, batch model.WriteBatch) error {
	_, err := memory.writeBatch(ctx, batch, false)
	return err
}

// WriteRealtime atomically persists one realtime batch and returns only newly
// inserted messages with authoritative chat metadata in request order.
func (memory *Memory) WriteRealtime(ctx context.Context, batch model.WriteBatch) (model.LiveEventBatch, error) {
	if batch.Origin() != model.WriteRealtime {
		return model.LiveEventBatch{}, &Error{kind: rejected}
	}
	return memory.writeBatch(ctx, batch, true)
}

func (memory *Memory) writeBatch(ctx context.Context, batch model.WriteBatch, emitLive bool) (model.LiveEventBatch, error) {
	if err := checkContext(ctx); err != nil {
		return model.LiveEventBatch{}, err
	}
	validated := make([]model.Message, batch.Len())
	for i := range validated {
		message, ok := batch.At(i)
		if !ok {
			return model.LiveEventBatch{}, &Error{kind: rejected}
		}
		validated[i] = message
	}
	rebuilt, err := model.NewWriteBatch(batch.Origin(), validated)
	if err != nil || rebuilt.Len() != batch.Len() {
		return model.LiveEventBatch{}, &Error{kind: rejected}
	}
	if rebuilt.Len() == 0 {
		return model.NewLiveEventBatch(nil)
	}

	memory.mu.Lock()
	defer memory.mu.Unlock()
	newChats := make(map[string]struct{}, rebuilt.Len())
	newIDs := make(map[string]map[string]struct{}, rebuilt.Len())
	staged := make(map[string]map[string]model.Message, rebuilt.Len())
	var inserted [model.MaxWriteBatchMessages]bool
	for i := 0; i < rebuilt.Len(); i++ {
		message, _ := rebuilt.At(i)
		chatKey, messageKey := message.ChatID().String(), message.MessageID().String()
		chat, exists := memory.chats[chatKey]
		if !exists {
			newChats[chatKey] = struct{}{}
		}
		prior, exists := model.Message{}, false
		if byID := staged[chatKey]; byID != nil {
			prior, exists = byID[messageKey]
		}
		if !exists && chat != nil {
			prior, exists = chat.messages[messageKey]
		}
		if exists {
			if !prior.SentAt().Equal(message.SentAt()) || prior.FromMe() != message.FromMe() {
				return model.LiveEventBatch{}, &Error{kind: rejected}
			}
			if prior.BodyRetained() && !message.BodyRetained() {
				message = prior
			}
			// A duplicate echo may omit quote metadata. It must not downgrade a
			// committed reply or replace its authoritative same-chat reference.
			if prior.Quote() != (model.TextQuote{}) {
				message = message.WithQuote(prior.Quote())
			}
		} else {
			inserted[i] = true
			if newIDs[chatKey] == nil {
				newIDs[chatKey] = make(map[string]struct{}, rebuilt.Len())
			}
			newIDs[chatKey][messageKey] = struct{}{}
		}
		if staged[chatKey] == nil {
			staged[chatKey] = make(map[string]model.Message, rebuilt.Len())
		}
		staged[chatKey][messageKey] = message
	}
	if len(memory.chats)+len(newChats) > memory.maxChats {
		return model.LiveEventBatch{}, &Error{kind: capacity}
	}
	for chatKey, ids := range newIDs {
		count := 0
		if chat := memory.chats[chatKey]; chat != nil {
			count = len(chat.messages)
		}
		if count+len(ids) > model.MaxRetentionSnapshotSummaries {
			return model.LiveEventBatch{}, &Error{kind: capacity}
		}
	}
	stagedChats := make(map[string]model.Chat, len(newChats)+len(staged))
	var liveEvents [model.MaxWriteBatchMessages]model.LiveEvent
	liveCount := 0
	for index := 0; index < rebuilt.Len(); index++ {
		if !inserted[index] {
			continue
		}
		message, _ := rebuilt.At(index)
		chatKey := message.ChatID().String()
		chat, ok := stagedChats[chatKey]
		if !ok {
			if current := memory.chats[chatKey]; current != nil {
				chat = current.chat
			} else {
				chat, err = model.NewChat(model.ChatInput{ID: chatKey, Placeholder: true})
				if err != nil {
					return model.LiveEventBatch{}, &Error{kind: rejected}
				}
			}
		}
		chat, err = advanceChatForMessage(chat, message, rebuilt.Origin() == model.WriteRealtime)
		if err != nil {
			return model.LiveEventBatch{}, &Error{kind: rejected}
		}
		stagedChats[chatKey] = chat
		if emitLive {
			committed := staged[chatKey][message.MessageID().String()]
			event, eventErr := model.NewLiveMessageCommitted(committed, chat.UnreadCount(), chat.LastMessageAt())
			if eventErr != nil {
				return model.LiveEventBatch{}, &Error{kind: rejected}
			}
			liveEvents[liveCount] = event
			liveCount++
		}
	}
	committedBatch, err := model.NewLiveEventBatch(liveEvents[:liveCount])
	if err != nil {
		return model.LiveEventBatch{}, &Error{kind: rejected}
	}
	for chatKey := range newChats {
		memory.chats[chatKey] = &memoryChat{messages: make(map[string]model.Message, model.MaxRetentionSnapshotSummaries)}
	}
	for chatKey, chat := range stagedChats {
		memory.chats[chatKey].chat = chat
	}
	for chatKey, messages := range staged {
		for id, message := range messages {
			memory.chats[chatKey].messages[id] = message
		}
	}
	return committedBatch, nil
}

func advanceChatForMessage(chat model.Chat, message model.Message, countUnread bool) (model.Chat, error) {
	lastMessageAt := chat.LastMessageAt()
	if lastMessageAt.IsZero() || message.SentAt().After(lastMessageAt) {
		lastMessageAt = message.SentAt()
	}
	unreadCount := chat.UnreadCount()
	if countUnread && !message.FromMe() && unreadCount < ^uint32(0) {
		unreadCount++
	}
	contactID := ""
	if chat.HasContact() {
		contactID = chat.ContactID().String()
	}
	return model.NewChat(model.ChatInput{
		ID:            chat.ID().String(),
		ContactID:     contactID,
		DisplayName:   chat.DisplayName(),
		IsGroup:       chat.IsGroup(),
		LastMessageAt: lastMessageAt,
		UnreadCount:   unreadCount,
		Muted:         chat.Muted(),
		Archived:      chat.Archived(),
		Placeholder:   chat.Placeholder(),
		UpdatedAt:     chat.UpdatedAt(),
		IngestSeq:     chat.IngestSeq(),
	})
}

func validateChatID(id model.ChatID) (model.ChatID, error) { return model.NewChatID(id.String()) }

func validateCursor(cursor model.Cursor) error {
	if cursor.IsZero() {
		return nil
	}
	_, err := model.NewCursor(cursor.SentAt(), cursor.MessageID())
	return err
}

func newer(left, right model.Message) bool {
	if left.SentAt().Equal(right.SentAt()) {
		return left.MessageID().String() > right.MessageID().String()
	}
	return left.SentAt().After(right.SentAt())
}

func olderThan(message model.Message, cursor model.Cursor) bool {
	if message.SentAt().Equal(cursor.SentAt()) {
		return message.MessageID().String() < cursor.MessageID().String()
	}
	return message.SentAt().Before(cursor.SentAt())
}

func (memory *Memory) Page(ctx context.Context, id model.ChatID, cursor model.Cursor, limit int) ([]model.Message, model.Cursor, error) {
	if err := checkContext(ctx); err != nil {
		return nil, model.NoCursor(), err
	}
	ownedID, err := validateChatID(id)
	if err != nil || validateCursor(cursor) != nil || limit <= 0 {
		return nil, model.NoCursor(), &Error{kind: rejected}
	}
	if limit > maxPageMessages {
		limit = maxPageMessages
	}
	memory.mu.RLock()
	defer memory.mu.RUnlock()
	chat := memory.chats[ownedID.String()]
	if chat == nil {
		return []model.Message{}, model.NoCursor(), nil
	}
	ordered := make([]model.Message, 0, len(chat.messages))
	for _, message := range chat.messages {
		if cursor.IsZero() || olderThan(message, cursor) {
			ordered = append(ordered, message)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return newer(ordered[i], ordered[j]) })
	more := len(ordered) > limit
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	next := model.NoCursor()
	if more && len(ordered) > 0 {
		next, _ = model.NewCursor(ordered[len(ordered)-1].SentAt(), ordered[len(ordered)-1].MessageID())
	}
	result := append([]model.Message(nil), ordered...)
	return result, next, nil
}

func (memory *Memory) RetentionSnapshot(ctx context.Context, id model.ChatID) (model.RetentionSnapshot, error) {
	if err := checkContext(ctx); err != nil {
		return model.RetentionSnapshot{}, err
	}
	ownedID, err := validateChatID(id)
	if err != nil {
		return model.RetentionSnapshot{}, &Error{kind: rejected}
	}
	memory.mu.RLock()
	defer memory.mu.RUnlock()
	chat := memory.chats[ownedID.String()]
	if chat == nil {
		return model.NewRetentionSnapshot(ownedID, nil, nil)
	}
	ordered := make([]model.Message, 0, len(chat.messages))
	for _, message := range chat.messages {
		ordered = append(ordered, message)
	}
	sort.Slice(ordered, func(i, j int) bool { return newer(ordered[i], ordered[j]) })
	summaries := make([]model.MessageSummary, len(ordered))
	for i, message := range ordered {
		summaries[i] = model.NewMessageSummary(message)
	}
	if chat.hasAnchor {
		anchor := chat.anchor
		return model.NewRetentionSnapshot(ownedID, summaries, &anchor)
	}
	return model.NewRetentionSnapshot(ownedID, summaries, nil)
}

func (memory *Memory) ApplyPrune(ctx context.Context, plan model.PrunePlan) (model.PruneResult, error) {
	if err := checkContext(ctx); err != nil {
		return model.PruneResult{}, err
	}
	ids := make([]model.MessageID, plan.DeleteLen())
	for i := range ids {
		id, ok := plan.DeleteAt(i)
		if !ok {
			return model.PruneResult{}, &Error{kind: rejected}
		}
		ids[i] = id
	}
	var replacement *model.MessageAnchor
	if anchor, ok := plan.ReplacementAnchor(); ok {
		replacement = &anchor
	}
	rebuilt, err := model.NewPrunePlan(plan.ChatID(), ids, replacement)
	if err != nil {
		return model.PruneResult{}, &Error{kind: rejected}
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	chat := memory.chats[rebuilt.ChatID().String()]
	if chat == nil {
		return model.NewPruneResult(0, 0, false)
	}
	deleted, bodies := 0, 0
	seen := make(map[string]struct{}, rebuilt.DeleteLen())
	for i := 0; i < rebuilt.DeleteLen(); i++ {
		id, _ := rebuilt.DeleteAt(i)
		key := id.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if message, ok := chat.messages[key]; ok {
			deleted++
			if message.BodyRetained() {
				bodies++
			}
			delete(chat.messages, key)
		}
	}
	changed := false
	if anchor, ok := rebuilt.ReplacementAnchor(); ok {
		changed = !chat.hasAnchor || !sameAnchor(chat.anchor, anchor)
		chat.anchor, chat.hasAnchor = anchor, true
	}
	return model.NewPruneResult(deleted, bodies, changed)
}

func sameAnchor(a, b model.MessageAnchor) bool {
	return a.ChatID().String() == b.ChatID().String() && a.MessageID().String() == b.MessageID().String() && a.SentAt().Equal(b.SentAt()) && a.FromMe() == b.FromMe()
}

// Usage uses fixed envelopes plus exact identifier, name and Message.ByteSize bytes.
func (memory *Memory) Usage(ctx context.Context) (model.CacheUsage, error) {
	if err := checkContext(ctx); err != nil {
		return model.CacheUsage{}, err
	}
	memory.mu.RLock()
	defer memory.mu.RUnlock()
	var bytes int64
	messages, bodies, anchors := 0, 0, 0
	for _, chat := range memory.chats {
		bytes = addSaturated(bytes, int64(chatEnvelopeBytes+len(chat.chat.ID().String())+len(chat.chat.DisplayName())))
		for _, message := range chat.messages {
			messages++
			if message.BodyRetained() {
				bodies++
			}
			bytes = addSaturated(bytes, int64(messageEnvelopeBytes+message.ByteSize()))
		}
		if chat.hasAnchor {
			anchors++
			bytes = addSaturated(bytes, int64(anchorEnvelopeBytes+len(chat.anchor.ChatID().String())+len(chat.anchor.MessageID().String())))
		}
	}
	return model.NewCacheUsage(bytes, len(memory.chats), messages, bodies, anchors)
}

func addSaturated(left, right int64) int64 {
	if right < 0 || left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}
