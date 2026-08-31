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
)

const connectedShutdownGrace = 2 * time.Second

// connectedApplicationService is receive-only in Milestone 4A. In particular,
// it deliberately does not implement applicationTextSender.
type connectedApplicationService struct {
	core  *service.Core
	store *store.Memory
}

func newConnectedApplicationService(source service.EventSource) (applicationService, error) {
	if source == nil {
		return nil, errors.New("connected event source rejected")
	}
	values := config.DefaultValues()
	if err := values.Validate(); err != nil {
		return nil, err
	}
	memory, err := store.NewMemory(values.Retention.MaxChats)
	if err != nil {
		return nil, err
	}
	policy, err := syncpolicy.New(values.Retention)
	if err != nil {
		return nil, err
	}
	core, err := service.New(service.Options{
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
	}, source, memory, policy, service.NewSystemClock())
	if err != nil {
		return nil, err
	}
	return &connectedApplicationService{core: core, store: memory}, nil
}

func (application *connectedApplicationService) Run(ctx context.Context) error {
	return application.core.Run(ctx)
}

func (application *connectedApplicationService) Updates() <-chan model.Update {
	return application.core.Updates()
}

func (application *connectedApplicationService) LiveEvents() <-chan model.LiveEvent {
	return application.core.LiveEvents()
}

func (*connectedApplicationService) InitialChats(ctx context.Context, limit int) ([]model.Chat, error) {
	if ctx == nil || ctx.Err() != nil || limit <= 0 {
		return nil, errors.New("initial chats rejected")
	}
	return nil, nil
}

func (application *connectedApplicationService) InitialMessages(ctx context.Context, chatID model.ChatID, limit int) ([]model.Message, error) {
	messages, _, err := application.store.Page(ctx, chatID, model.NoCursor(), limit)
	return messages, err
}
