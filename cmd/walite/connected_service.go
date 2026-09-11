package main

import (
	"context"
	"errors"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/syncpolicy"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

const connectedShutdownGrace = 2 * time.Second

// connectedApplicationService uses capabilities of the one connection-owned
// client. Chat data passes through the SQLite application cache, the writer,
// and LiveEvents; the WhatsApp session database remains separately owned.
type connectedApplicationService struct {
	core            *service.Core
	store           *store.SQLiteStore
	sender          wa.TextSender
	reactions       wa.ReactionSender
	readReceipts    wa.ReadReceiptSender
	media           wa.MediaDownloader
	history         wa.HistoryRequester
	display         displaySource
	source          service.EventSource
	policy          service.RetentionPolicy
	cacheUpdates    chan struct{}
	onDemandDone    chan struct{}
	reactionUpdates chan model.ReactionSummary
}

func newConnectedApplicationService(source service.EventSource, sender wa.TextSender, readReceipts wa.ReadReceiptSender, cache *store.SQLiteStore) (applicationService, error) {
	return newConnectedApplicationServiceWithCapabilities(source, sender, readReceipts, nil, nil, cache)
}

func newConnectedApplicationServiceWithMedia(source service.EventSource, sender wa.TextSender, readReceipts wa.ReadReceiptSender, media wa.MediaDownloader, cache *store.SQLiteStore) (applicationService, error) {
	return newConnectedApplicationServiceWithCapabilities(source, sender, readReceipts, media, nil, cache)
}

func newConnectedApplicationServiceWithCapabilities(source service.EventSource, sender wa.TextSender, readReceipts wa.ReadReceiptSender, media wa.MediaDownloader, history wa.HistoryRequester, cache *store.SQLiteStore) (applicationService, error) {
	if source == nil || sender == nil || readReceipts == nil || cache == nil {
		return nil, errors.New("connected event source rejected")
	}
	values := config.DefaultValues()
	if err := values.Validate(); err != nil {
		return nil, err
	}
	policy, err := syncpolicy.New(values.Retention)
	if err != nil {
		return nil, err
	}
	core, err := service.NewWithTextSender(service.Options{
		Realtime:               service.QueueOptions{Entries: values.Queues.Realtime.Entries, Bytes: values.Queues.Realtime.Bytes},
		History:                service.QueueOptions{Entries: values.Queues.History.Entries, Bytes: values.Queues.History.Bytes},
		LiveWrites:             service.QueueOptions{Entries: values.Queues.LiveWrites.Entries, Bytes: values.Queues.LiveWrites.Bytes},
		HistoryWrites:          service.QueueOptions{Entries: values.Queues.HistoryWrites.Entries, Bytes: values.Queues.HistoryWrites.Bytes},
		ViewUpdates:            service.QueueOptions{Entries: values.Queues.ViewUpdates.Entries, Bytes: values.Queues.ViewUpdates.Bytes},
		HistoryChunkRecords:    values.History.ChunkRecords,
		HistoryChunkBytes:      values.History.ChunkBytes,
		BatchMaxOperations:     values.Batch.MaxOperations,
		RetentionSnapshotLimit: values.Retention.MessagesPerChat + values.Batch.MaxOperations + 1,
		BatchWait:              values.Batch.MaxWait,
		LiveWriteBusy:          values.Queues.LiveWriteBusy,
		ShutdownGrace:          connectedShutdownGrace,
	}, source, cache, policy, service.NewSystemClock(), sender)
	if err != nil {
		return nil, err
	}
	display, _ := source.(displaySource)
	reactionSender, _ := sender.(wa.ReactionSender)
	return &connectedApplicationService{
		core: core, store: cache, sender: sender, readReceipts: readReceipts, media: media, history: history,
		reactions: reactionSender,
		display:   display, source: source, policy: policy,
		cacheUpdates: make(chan struct{}, 1), onDemandDone: make(chan struct{}, 1), reactionUpdates: make(chan model.ReactionSummary, wa.RealtimeSourceCapacity),
	}, nil
}

// LoadOlderMessages first consumes any already-cached rows before asking the
// linked primary device for exactly one bounded ON_DEMAND page. Completion is
// signaled only after the existing bootstrap path has committed that delivery.
func (application *connectedApplicationService) LoadOlderMessages(
	ctx context.Context,
	chatID model.ChatID,
	oldestID model.MessageID,
	oldestAt time.Time,
	oldestFromMe bool,
	count int,
) ([]model.Message, error) {
	if application == nil || application.store == nil || ctx == nil || count < 1 || count > wa.MaxOlderHistoryMessages {
		return nil, wa.ErrHistoryRequestRejected
	}
	cursor, err := model.NewCursor(oldestAt, oldestID)
	if err != nil {
		return nil, wa.ErrHistoryRequestRejected
	}
	page, _, err := application.store.Page(ctx, chatID, cursor, count)
	if err != nil || len(page) > 0 {
		return page, err
	}
	if application.history == nil {
		return nil, wa.ErrHistoryRequestUnavailable
	}
	for {
		select {
		case <-application.onDemandDone:
			continue
		default:
		}
		break
	}
	if err := application.history.RequestOlderHistory(ctx, chatID, oldestID, oldestAt, oldestFromMe, count); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-application.onDemandDone:
	}
	page, _, err = application.store.Page(ctx, chatID, cursor, count)
	return page, err
}

func (application *connectedApplicationService) MediaMessage(ctx context.Context, chatID model.ChatID, messageID model.MessageID) (model.Message, error) {
	if application == nil || application.store == nil {
		return model.Message{}, errors.New("media unavailable")
	}
	return application.store.Message(ctx, chatID, messageID)
}

func (application *connectedApplicationService) DownloadMedia(ctx context.Context, media model.Media, file wa.MediaFile) error {
	if application == nil || application.media == nil {
		return wa.ErrMediaUnavailable
	}
	return application.media.DownloadMedia(ctx, media, file)
}

func (application *connectedApplicationService) MarkRead(ctx context.Context, request model.ReadReceiptRequest) error {
	if application == nil || application.readReceipts == nil {
		return wa.ErrReadReceiptUnavailable
	}
	return application.readReceipts.MarkRead(ctx, request)
}

func (application *connectedApplicationService) DisplayUpdates() <-chan model.DisplayMetadata {
	if application.display == nil {
		return nil
	}
	return application.display.DisplayUpdates()
}

func (application *connectedApplicationService) RequestDisplayMetadata(ids []model.ChatID) {
	if application != nil {
		if requester, ok := application.display.(displayMetadataRequester); ok {
			requester.RequestDisplayMetadata(ids)
		}
	}
}

func (application *connectedApplicationService) ApplyDisplayMetadata(ctx context.Context, metadata model.DisplayMetadata) (model.DisplayMetadata, error) {
	return application.store.ApplyDisplayMetadata(ctx, metadata)
}

func (application *connectedApplicationService) ValidateText(request service.SendTextRequest) error {
	if quote := request.Reply(); quote.MessageID().String() != "" {
		return application.sender.ValidateText(request.ChatID(), request.Text(), quote)
	}
	return application.sender.ValidateText(request.ChatID(), request.Text())
}

func (application *connectedApplicationService) SendText(ctx context.Context, request service.SendTextRequest) error {
	return application.core.SendText(ctx, request)
}

func (application *connectedApplicationService) SendMedia(ctx context.Context, request service.SendMediaRequest) error {
	return application.core.SendMedia(ctx, request)
}

func (application *connectedApplicationService) ValidateReaction(request model.SendReactionRequest) error {
	if application == nil || application.reactions == nil {
		return wa.ErrReactionUnavailable
	}
	return application.reactions.ValidateReaction(request)
}

func (application *connectedApplicationService) SendReaction(ctx context.Context, request model.SendReactionRequest) error {
	if err := application.ValidateReaction(request); err != nil {
		return err
	}
	reaction, err := application.reactions.SendReaction(ctx, request)
	if err != nil {
		return err
	}
	summary, err := application.store.ApplyReaction(context.Background(), reaction)
	if err != nil {
		return wa.ErrReactionUncertain
	}
	application.publishReaction(ctx, summary)
	return nil
}

func (application *connectedApplicationService) ReactionUpdates() <-chan model.ReactionSummary {
	if application == nil {
		return nil
	}
	return application.reactionUpdates
}

func (application *connectedApplicationService) ReactionSummary(ctx context.Context, chat model.ChatID, target model.MessageID) (model.ReactionSummary, error) {
	return application.store.ReactionSummary(ctx, chat, target)
}

func (application *connectedApplicationService) publishReaction(ctx context.Context, summary model.ReactionSummary) {
	select {
	case application.reactionUpdates <- summary:
	case <-ctx.Done():
	}
}

func (application *connectedApplicationService) MarkChatLocallyRead(ctx context.Context, id model.ChatID, through time.Time) error {
	return application.store.MarkChatLocallyRead(ctx, id, through)
}

func (application *connectedApplicationService) Run(ctx context.Context) error {
	defer close(application.cacheUpdates)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	bootstrapDone := make(chan error, 1)
	if source, ok := application.source.(interface {
		BootstrapRecords() <-chan model.BootstrapRecord
	}); ok && source.BootstrapRecords() != nil {
		go func() {
			err := application.runBootstrap(runCtx, source.BootstrapRecords())
			if err != nil {
				cancel()
			}
			bootstrapDone <- err
		}()
	} else {
		bootstrapDone <- nil
	}
	reactionDone := make(chan error, 1)
	go func() { reactionDone <- application.runReactions(runCtx) }()
	coreErr := application.core.Run(runCtx)
	cancel()
	bootstrapErr := <-bootstrapDone
	reactionErr := <-reactionDone
	if bootstrapErr != nil && !errors.Is(bootstrapErr, context.Canceled) {
		return errors.Join(coreErr, bootstrapErr)
	}
	if reactionErr != nil && !errors.Is(reactionErr, context.Canceled) {
		return errors.Join(coreErr, reactionErr)
	}
	return coreErr
}

func (application *connectedApplicationService) runReactions(ctx context.Context) error {
	source, ok := application.source.(interface{ ReactionEvents() <-chan model.Reaction })
	if !ok || source.ReactionEvents() == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case reaction, open := <-source.ReactionEvents():
			if !open {
				return nil
			}
			summary, err := application.store.ApplyReaction(ctx, reaction)
			if err != nil {
				return err
			}
			application.publishReaction(ctx, summary)
		}
	}
}

func (application *connectedApplicationService) runBootstrap(ctx context.Context, records <-chan model.BootstrapRecord) error {
	knownChats := make(map[string]struct{}, model.MaxChatSummaries)
	known, err := application.store.ListChats(ctx, model.MaxChatSummaries)
	if err != nil {
		return err
	}
	for _, chat := range known {
		knownChats[chat.ID().String()] = struct{}{}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case record, ok := <-records:
			if !ok {
				return nil
			}
			if record.Complete() {
				if _, err := application.store.EnforceCacheBudget(ctx, time.Now().UTC()); err != nil && !errors.Is(err, store.ErrCachePressure) {
					return err
				}
				select {
				case application.cacheUpdates <- struct{}{}:
				default:
				}
				if record.Category() == model.BootstrapOnDemand {
					select {
					case <-application.onDemandDone:
					default:
					}
					select {
					case application.onDemandDone <- struct{}{}:
					default:
					}
				}
				continue
			}
			chatID := record.Chat().ID().String()
			if _, exists := knownChats[chatID]; !exists {
				if len(knownChats) == model.MaxChatSummaries {
					continue
				}
				knownChats[chatID] = struct{}{}
			}
			chat := record.Chat()
			cached, cacheErr := application.store.Chat(ctx, chat.ID())
			switch {
			case cacheErr == nil:
				// Missing unread metadata is never authoritative. An equal/older
				// history activity snapshot is also stale relative to cached local
				// read state; only genuinely newer activity may replace unread.
				if !record.UnreadAuthoritative() || !chat.LastMessageAt().After(cached.LastMessageAt()) {
					chat, err = chatWithUnread(chat, cached.UnreadCount())
					if err != nil {
						return err
					}
				}
			case errors.Is(cacheErr, store.ErrChatNotFound):
			default:
				return cacheErr
			}
			if err := application.store.EnsureChat(ctx, chat); err != nil {
				return err
			}
			if record.Len() > 0 {
				messages := make([]model.Message, record.Len())
				for index := range messages {
					message, ok := record.At(index)
					if !ok {
						return errors.New("bootstrap record corrupted")
					}
					messages[index] = message
				}
				batch, err := model.NewWriteBatch(model.WriteHistory, messages)
				if err != nil {
					return err
				}
				if err := application.store.Write(ctx, batch); err != nil {
					return err
				}
			}
			for index := 0; index < record.ReactionLen(); index++ {
				reaction, ok := record.ReactionAt(index)
				if !ok {
					return errors.New("bootstrap reaction record corrupted")
				}
				if _, err := application.store.ApplyReaction(ctx, reaction); err != nil {
					return err
				}
			}
			if application.policy != nil {
				snapshot, err := application.store.RetentionSnapshot(ctx, chat.ID())
				if err != nil {
					return err
				}
				usage, err := application.store.Usage(ctx)
				if err != nil {
					return err
				}
				plan, err := application.policy.PlanPrune(snapshot, model.RetentionState{Now: time.Now().UTC(), Usage: usage, Origin: model.WriteHistory})
				if err != nil {
					return err
				}
				if _, err := application.store.ApplyPrune(ctx, plan); err != nil {
					return err
				}
			}
		}
	}
}

func chatWithUnread(chat model.Chat, unread uint32) (model.Chat, error) {
	contactID := ""
	if chat.HasContact() {
		contactID = chat.ContactID().String()
	}
	return model.NewChat(model.ChatInput{
		ID: chat.ID().String(), ContactID: contactID, DisplayName: chat.DisplayName(), IsGroup: chat.IsGroup(),
		LastMessageAt: chat.LastMessageAt(), UnreadCount: unread, Muted: chat.Muted(), Archived: chat.Archived(),
		Placeholder: chat.Placeholder(), UpdatedAt: chat.UpdatedAt(), IngestSeq: chat.IngestSeq(),
	})
}

func (application *connectedApplicationService) CacheUpdates() <-chan struct{} {
	return application.cacheUpdates
}

func (application *connectedApplicationService) Updates() <-chan model.Update {
	return application.core.Updates()
}

func (application *connectedApplicationService) LiveEvents() <-chan model.LiveEvent {
	return application.core.LiveEvents()
}

func (application *connectedApplicationService) InitialChats(ctx context.Context, limit int) ([]model.Chat, error) {
	if ctx == nil || ctx.Err() != nil || limit <= 0 {
		return nil, errors.New("initial chats rejected")
	}
	return application.store.ListChats(ctx, limit)
}

func (application *connectedApplicationService) InitialMessages(ctx context.Context, chatID model.ChatID, limit int) ([]model.Message, error) {
	messages, _, err := application.store.Page(ctx, chatID, model.NoCursor(), limit)
	return messages, err
}
