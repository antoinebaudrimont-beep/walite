package wa

import (
	"sort"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func bootstrapCategory(syncType waHistorySync.HistorySync_HistorySyncType) (model.BootstrapCategory, bool) {
	switch syncType {
	case waHistorySync.HistorySync_INITIAL_BOOTSTRAP:
		return model.BootstrapInitial, true
	case waHistorySync.HistorySync_RECENT:
		return model.BootstrapRecent, true
	case waHistorySync.HistorySync_FULL:
		return model.BootstrapFull, true
	case waHistorySync.HistorySync_ON_DEMAND:
		return model.BootstrapOnDemand, true
	default:
		return 0, false
	}
}

// handleHistorySync converts and releases one upstream graph synchronously.
// Backpressure is bounded by the source's single-record channel.
func (client *whatsmeowConnectionClient) handleHistorySync(event *events.HistorySync) {
	if client == nil || client.client == nil || client.realtime == nil || event == nil || event.Data == nil {
		return
	}
	category, supported := bootstrapCategory(event.Data.GetSyncType())
	if !supported {
		return
	}
	now := time.Now().UTC()
	if client.now != nil {
		now = client.now().UTC()
	}
	for _, conversation := range event.Data.GetConversations() {
		record, ok := client.adaptBootstrapConversation(category, conversation, now)
		if ok && !client.realtime.admitBootstrap(record) {
			return
		}
	}
	complete, _ := model.NewBootstrapComplete(category)
	client.realtime.admitBootstrap(complete)
}

func (client *whatsmeowConnectionClient) adaptBootstrapConversation(category model.BootstrapCategory, conversation *waHistorySync.Conversation, receivedAt time.Time) (model.BootstrapRecord, bool) {
	if conversation == nil {
		return model.BootstrapRecord{}, false
	}
	jid, err := types.ParseJID(conversation.GetID())
	if err != nil {
		return model.BootstrapRecord{}, false
	}
	primary, err := model.NewChatID(jid.ToNonAD().String())
	if err != nil {
		return model.BootstrapRecord{}, false
	}
	alternate := bootstrapAlternate(primary, conversation)
	canonical, err := client.realtime.aliases.resolve(primary, alternate)
	if err != nil {
		return model.BootstrapRecord{}, false
	}
	// Both protobuf fields are complete Unix timestamps. HistorySync variants
	// may populate either (or both), so the newest authoritative activity wins.
	activitySeconds := max(conversation.GetLastMsgTimestamp(), conversation.GetConversationTimestamp())
	activity := time.Time{}
	if activitySeconds > 0 && activitySeconds <= uint64(^uint64(0)>>1) {
		activity = time.Unix(int64(activitySeconds), 0).UTC()
	}
	name := conversation.GetDisplayName()
	if name == "" {
		name = conversation.GetName()
	}
	if name == "" {
		name = readablePhoneFallback(canonical, primary, alternate)
	}
	chat, err := model.NewChat(model.ChatInput{
		ID: canonical.String(), DisplayName: name, IsGroup: jid.Server == types.GroupServer,
		LastMessageAt: activity, UnreadCount: conversation.GetUnreadCount(),
		Muted: conversation.GetMuteEndTime() > 0, Archived: conversation.GetArchived(), UpdatedAt: receivedAt,
	})
	if err != nil {
		return model.BootstrapRecord{}, false
	}

	messages := make([]model.Message, 0, model.MaxBootstrapMessagesPerChat)
	for _, historyMessage := range conversation.GetMessages() {
		parsed, parseErr := client.client.ParseWebMessage(jid, historyMessage.GetMessage())
		if parseErr != nil || parsed == nil {
			continue
		}
		owned := *parsed
		owned.SourceWebMsg = nil // history remains excluded from realtime admission
		event, recognized := adaptTextMessage(&owned, receivedAt, client.client.Store.GetJID(), client.client.Store.GetLID())
		if !recognized {
			continue
		}
		message := event.Message()
		if message.ChatID() != canonical {
			message, err = message.WithChatID(canonical)
			if err != nil {
				continue
			}
		}
		messages = retainNewestBootstrapMessage(messages, message)
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].SentAt().Equal(messages[j].SentAt()) {
			return messages[i].MessageID().String() < messages[j].MessageID().String()
		}
		return messages[i].SentAt().Before(messages[j].SentAt())
	})
	if client.realtime.display != nil {
		people := make([]model.ChatID, 0, len(messages))
		for _, message := range messages {
			if id := message.SenderID(); id.String() != "" {
				people = append(people, model.ChatID(id))
			}
		}
		client.realtime.display.requestPeople(people)
	}
	record, err := model.NewBootstrapRecord(category, chat, messages)
	record = record.WithUnreadAuthoritative(conversation.UnreadCount != nil)
	return record, err == nil
}

func bootstrapAlternate(primary model.ChatID, conversation *waHistorySync.Conversation) model.ChatID {
	for _, value := range []string{conversation.GetPnJID(), conversation.GetLidJID(), conversation.GetNewJID(), conversation.GetOldJID()} {
		jid, err := types.ParseJID(value)
		if err == nil {
			if alternate := authoritativeAlternate(primary, jid); alternate.String() != "" {
				return alternate
			}
		}
	}
	return model.ChatID{}
}

func retainNewestBootstrapMessage(messages []model.Message, candidate model.Message) []model.Message {
	for _, existing := range messages {
		if existing.MessageID() == candidate.MessageID() {
			return messages
		}
	}
	if len(messages) < model.MaxBootstrapMessagesPerChat {
		return append(messages, candidate)
	}
	oldest := 0
	for index := 1; index < len(messages); index++ {
		if bootstrapMessageBefore(messages[index], messages[oldest]) {
			oldest = index
		}
	}
	if bootstrapMessageBefore(candidate, messages[oldest]) {
		return messages
	}
	messages[oldest] = candidate
	return messages
}

func bootstrapMessageBefore(left, right model.Message) bool {
	if left.SentAt().Equal(right.SentAt()) {
		return left.MessageID().String() < right.MessageID().String()
	}
	return left.SentAt().Before(right.SentAt())
}
