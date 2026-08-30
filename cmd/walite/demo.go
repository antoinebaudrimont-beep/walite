package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/syncpolicy"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

const (
	demoShutdownGrace = 2 * time.Second
	demoHistoryCount  = 600
)

var demoStart = time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)

type demoScenario struct {
	core                   *service.Core
	store                  *store.Memory
	gated                  *gatedStore
	source                 *wa.FakeSource
	policy                 *syncpolicy.Policy
	historyStart           chan struct{}
	retentionSnapshotLimit int
	liveText               string
}

type gatedStore struct {
	delegate            *store.Memory
	firstHistoryEntered chan struct{}
	releaseFirstHistory chan struct{}
	firstHistory        sync.Once
}

var _ service.MessageStore = (*gatedStore)(nil)
var _ service.EventSource = (*wa.FakeSource)(nil)
var _ service.RetentionPolicy = (*syncpolicy.Policy)(nil)

func (gated *gatedStore) EnsureChat(ctx context.Context, chat model.Chat) error {
	return gated.delegate.EnsureChat(ctx, chat)
}

func (gated *gatedStore) Write(ctx context.Context, batch model.WriteBatch) error {
	wait := false
	if batch.Origin() == model.WriteHistory {
		gated.firstHistory.Do(func() { wait = true })
	}
	if wait {
		select {
		case gated.firstHistoryEntered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-gated.releaseFirstHistory:
		}
	}
	return gated.delegate.Write(ctx, batch)
}

func (gated *gatedStore) WriteRealtime(ctx context.Context, batch model.WriteBatch) (model.LiveEventBatch, error) {
	return gated.delegate.WriteRealtime(ctx, batch)
}

func (gated *gatedStore) Page(ctx context.Context, chat model.ChatID, cursor model.Cursor, limit int) ([]model.Message, model.Cursor, error) {
	return gated.delegate.Page(ctx, chat, cursor, limit)
}

func (gated *gatedStore) RetentionSnapshot(ctx context.Context, chat model.ChatID) (model.RetentionSnapshot, error) {
	return gated.delegate.RetentionSnapshot(ctx, chat)
}

func (gated *gatedStore) ApplyPrune(ctx context.Context, plan model.PrunePlan) (model.PruneResult, error) {
	return gated.delegate.ApplyPrune(ctx, plan)
}

func (gated *gatedStore) Usage(ctx context.Context) (model.CacheUsage, error) {
	return gated.delegate.Usage(ctx)
}

func newDemoScenario(values config.Values) (demoScenario, error) {
	if err := values.Validate(); err != nil {
		return demoScenario{}, err
	}
	retentionSnapshotLimit := values.Retention.MessagesPerChat + values.Batch.MaxOperations + 1
	memory, err := store.NewMemory(values.Retention.MaxChats)
	if err != nil {
		return demoScenario{}, err
	}
	policy, err := syncpolicy.New(values.Retention)
	if err != nil {
		return demoScenario{}, err
	}

	historyStart := make(chan struct{})
	gated := &gatedStore{delegate: memory, firstHistoryEntered: make(chan struct{}, 1), releaseFirstHistory: make(chan struct{})}
	chatID, err := model.NewChatID("demo-chat")
	if err != nil {
		return demoScenario{}, err
	}
	bulkJob, err := model.NewHistoryJob("demo-history-bulk", model.HistoryBulk)
	if err != nil {
		return demoScenario{}, err
	}
	bulkSpec, err := wa.NewHistorySpec(bulkJob, demoHistoryCount, chatID, demoStart, "Synthetic history message")
	if err != nil {
		return demoScenario{}, err
	}
	overflowJob, err := model.NewHistoryJob("demo-history-overflow", model.HistoryBulk)
	if err != nil {
		return demoScenario{}, err
	}
	overflowSpec, err := wa.NewHistorySpec(overflowJob, 1, chatID, demoStart, "Synthetic overflow history")
	if err != nil {
		return demoScenario{}, err
	}
	liveText := "Synthetic live message"
	liveMessage, err := model.NewMessage(model.MessageInput{ChatID: chatID.String(), MessageID: "demo-live-1", SentAt: demoStart.Add(time.Hour), Text: liveText})
	if err != nil {
		return demoScenario{}, err
	}
	liveEvent, err := model.NewEvent(liveMessage, demoStart.Add(time.Hour))
	if err != nil {
		return demoScenario{}, err
	}
	source, err := wa.NewFakeSource([]wa.ScriptStep{
		wa.NewBarrierStep(historyStart),
		wa.NewHistoryStep(bulkSpec, nil),
		wa.NewBarrierStep(gated.firstHistoryEntered),
		wa.NewHistoryStep(overflowSpec, nil),
		wa.NewRealtimeStep(liveEvent, nil),
	})
	if err != nil {
		return demoScenario{}, err
	}
	options := service.Options{
		Realtime:               service.QueueOptions{Entries: values.Queues.Realtime.Entries, Bytes: values.Queues.Realtime.Bytes},
		History:                service.QueueOptions{Entries: values.Queues.History.Entries, Bytes: values.Queues.History.Bytes},
		LiveWrites:             service.QueueOptions{Entries: values.Queues.LiveWrites.Entries, Bytes: values.Queues.LiveWrites.Bytes},
		HistoryWrites:          service.QueueOptions{Entries: values.Queues.HistoryWrites.Entries, Bytes: values.Queues.HistoryWrites.Bytes},
		ViewUpdates:            service.QueueOptions{Entries: values.Queues.ViewUpdates.Entries, Bytes: values.Queues.ViewUpdates.Bytes},
		HistoryChunkRecords:    values.History.ChunkRecords,
		HistoryChunkBytes:      values.History.ChunkBytes,
		BatchMaxOperations:     values.Batch.MaxOperations,
		RetentionSnapshotLimit: retentionSnapshotLimit,
		BatchWait:              values.Batch.MaxWait,
		LiveWriteBusy:          values.Queues.LiveWriteBusy,
		ShutdownGrace:          demoShutdownGrace,
	}
	clock := service.NewSystemClock()
	// The offline transport owns deterministic final message time independently
	// of service scheduling. Offline state is rebuilt for each process, so the
	// sequence starts after all seeded demo activity on every launch.
	nextOutgoingAt := demoStart.Add(2 * time.Hour)
	sender, err := wa.NewOfflineTextSender(func() time.Time {
		sentAt := nextOutgoingAt
		nextOutgoingAt = nextOutgoingAt.Add(time.Second)
		return sentAt
	})
	if err != nil {
		return demoScenario{}, err
	}
	core, err := service.NewWithTextSender(options, source, gated, policy, clock, sender)
	if err != nil {
		return demoScenario{}, err
	}
	return demoScenario{core: core, store: memory, gated: gated, source: source, policy: policy, historyStart: historyStart, retentionSnapshotLimit: retentionSnapshotLimit, liveText: liveText}, nil
}

func runDemo(ctx context.Context, writer io.Writer, scenario demoScenario) error {
	if ctx == nil || writer == nil || scenario.core == nil || scenario.store == nil || scenario.gated == nil || scenario.source == nil || scenario.policy == nil || scenario.historyStart == nil {
		return errors.New("demo rejected")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- scenario.core.Run(runCtx) }()

	writeLine := func(format string, args ...any) error {
		_, err := fmt.Fprintf(writer, format+"\n", args...)
		return err
	}
	if err := writeLine("walite — offline synthetic demo"); err != nil {
		cancel()
		<-result
		return err
	}
	historyStarted := false
	historyReleased := false
	degradedPrinted := false
	for update := range scenario.core.Updates() {
		var err error
		switch update.Kind() {
		case model.UpdateReady:
			err = writeLine("ready")
			if err == nil && !historyStarted {
				close(scenario.historyStart)
				historyStarted = true
			}
		case model.UpdateLive:
			err = writeLine("live chat=%s message=%s text=%q", update.ChatID().String(), update.MessageID().String(), scenario.liveText)
			if err == nil && !historyReleased {
				close(scenario.gated.releaseFirstHistory)
				historyReleased = true
			}
		case model.UpdateDegraded:
			if !degradedPrinted {
				err = writeLine("degraded discarded=%d", update.Discarded())
				degradedPrinted = err == nil
			}
		case model.UpdateHistory:
			err = writeLine("history accepted=%d discarded=%d", update.Accepted(), update.Discarded())
		}
		if err != nil {
			cancel()
			<-result
			return err
		}
	}
	runErr := <-result
	if runErr != nil {
		return runErr
	}
	status := scenario.source.Status()
	if status.Degraded() && !degradedPrinted {
		if err := writeLine("degraded discarded=%d", sourceDropTotal(status)); err != nil {
			return err
		}
	}
	usage, err := scenario.store.Usage(context.Background())
	if err != nil {
		return err
	}
	if err := writeLine("summary chats=%d messages=%d bodies=%d anchors=%d bytes=%d", usage.Chats(), usage.Messages(), usage.Bodies(), usage.Anchors(), usage.EstimatedBytes()); err != nil {
		return err
	}
	return writeLine("stopped")
}

func sourceDropTotal(status model.SourceStatus) uint64 {
	values := [...]uint64{status.MetadataDropped(), status.OnDemandDropped(), status.BulkDropped(), status.UnknownDropped()}
	var total uint64
	for _, value := range values {
		if ^uint64(0)-total < value {
			return ^uint64(0)
		}
		total += value
	}
	return total
}
