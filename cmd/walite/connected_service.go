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
// client. Chat data still passes through Memory, the writer and LiveEvents.
type connectedApplicationService struct {
	core    *service.Core
	store   *store.Memory
	sender  wa.TextSender
	display displaySource
}

func newConnectedApplicationService(source service.EventSource, sender wa.TextSender) (applicationService, error) {
	if source == nil || sender == nil {
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
	}, source, memory, policy, service.NewSystemClock(), sender)
	if err != nil {
		return nil, err
	}
	display, _ := source.(displaySource)
	return &connectedApplicationService{core: core, store: memory, sender: sender, display: display}, nil
}

func (application *connectedApplicationService) DisplayUpdates() <-chan model.DisplayMetadata {
	if application.display == nil {
		return nil
	}
	return application.display.DisplayUpdates()
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
