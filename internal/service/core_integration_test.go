package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/syncpolicy"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

const integrationHistoryRecords = 100_000

var integrationStart = time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)

type integrationGatedStore struct {
	delegate *store.Memory
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

var _ service.MessageStore = (*integrationGatedStore)(nil)

func (gated *integrationGatedStore) EnsureChat(ctx context.Context, chat model.Chat) error {
	return gated.delegate.EnsureChat(ctx, chat)
}
func (gated *integrationGatedStore) Write(ctx context.Context, batch model.WriteBatch) error {
	wait := false
	if batch.Origin() == model.WriteHistory {
		gated.once.Do(func() { wait = true })
	}
	if wait {
		gated.entered <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-gated.release:
		}
	}
	return gated.delegate.Write(ctx, batch)
}
func (gated *integrationGatedStore) WriteRealtime(ctx context.Context, batch model.WriteBatch) (model.LiveEventBatch, error) {
	return gated.delegate.WriteRealtime(ctx, batch)
}
func (gated *integrationGatedStore) Page(ctx context.Context, chat model.ChatID, cursor model.Cursor, limit int) ([]model.Message, model.Cursor, error) {
	return gated.delegate.Page(ctx, chat, cursor, limit)
}
func (gated *integrationGatedStore) RetentionSnapshot(ctx context.Context, chat model.ChatID) (model.RetentionSnapshot, error) {
	return gated.delegate.RetentionSnapshot(ctx, chat)
}
func (gated *integrationGatedStore) ApplyPrune(ctx context.Context, plan model.PrunePlan) (model.PruneResult, error) {
	return gated.delegate.ApplyPrune(ctx, plan)
}
func (gated *integrationGatedStore) Usage(ctx context.Context) (model.CacheUsage, error) {
	return gated.delegate.Usage(ctx)
}

func TestCoreLazy100000HistoryLivePrecedenceAndRetention(t *testing.T) {
	values := config.DefaultValues()
	memory, _ := store.NewMemory(values.Retention.MaxChats)
	gated := &integrationGatedStore{delegate: memory, entered: make(chan struct{}, 1), release: make(chan struct{})}
	chat, _ := model.NewChatID("integration-chat")
	job, _ := model.NewHistoryJob("integration-history-100000", model.HistoryBulk)
	spec, err := wa.NewHistorySpec(job, integrationHistoryRecords, chat, integrationStart, "Synthetic integration history")
	if err != nil {
		t.Fatal(err)
	}
	liveMessage, _ := model.NewMessage(model.MessageInput{ChatID: chat.String(), MessageID: "integration-live", SentAt: integrationStart.Add(48 * time.Hour), Text: "Synthetic integration live"})
	liveEvent, _ := model.NewEvent(liveMessage, integrationStart.Add(48*time.Hour))
	source, err := wa.NewFakeSource([]wa.ScriptStep{
		wa.NewHistoryStep(spec, nil),
		wa.NewBarrierStep(gated.entered),
		wa.NewRealtimeStep(liveEvent, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := syncpolicy.New(values.Retention)
	core, err := service.New(integrationOptions(values), source, gated, policy, service.NewSystemClock())
	if err != nil {
		t.Fatal(err)
	}
	if cap(core.LiveEvents()) != service.LiveEventCapacity || service.LiveEventCapacity != 64 {
		t.Fatalf("live event capacity=%d constant=%d", cap(core.LiveEvents()), service.LiveEventCapacity)
	}
	done := make(chan error, 1)
	go func() { done <- core.Run(context.Background()) }()
	ready, live := false, false
	for update := range core.Updates() {
		if !ready {
			if update.Kind() != model.UpdateReady {
				t.Fatalf("first update=%v", update.Kind())
			}
			ready = true
		}
		if update.Kind() == model.UpdateLive {
			live = true
			select {
			case <-gated.release:
				t.Fatal("history completed gate released before live")
			default:
			}
			close(gated.release)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !ready || !live {
		t.Fatalf("ready=%t live=%t", ready, live)
	}
	committed, ok := <-core.LiveEvents()
	if !ok || committed.Message().MessageID().String() != liveMessage.MessageID().String() || committed.Message().Text() != liveMessage.Text() {
		t.Fatalf("committed live event=%+v open=%t", committed, ok)
	}
	if _, ok := <-core.LiveEvents(); ok {
		t.Fatal("live event channel remained open after service completion")
	}
	usage, err := memory.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if usage.Chats() != 1 || usage.Bodies() > values.Retention.MessagesPerChat || usage.Messages() >= integrationHistoryRecords/100 || usage.Anchors() > 1 {
		t.Fatalf("usage chats=%d messages=%d bodies=%d anchors=%d bytes=%d", usage.Chats(), usage.Messages(), usage.Bodies(), usage.Anchors(), usage.EstimatedBytes())
	}
	t.Logf("lazy_records=%d live_before_release=%t chats=%d messages=%d bodies=%d anchors=%d bytes=%d", integrationHistoryRecords, live, usage.Chats(), usage.Messages(), usage.Bodies(), usage.Anchors(), usage.EstimatedBytes())
}

func TestCoreRepeatedOwnedLifecycle(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		values := config.DefaultValues()
		memory, _ := store.NewMemory(values.Retention.MaxChats)
		chat, _ := model.NewChatID("repeat-chat")
		job, _ := model.NewHistoryJob("repeat-history", model.HistoryBulk)
		spec, _ := wa.NewHistorySpec(job, 32, chat, integrationStart, "Synthetic repeat history")
		source, _ := wa.NewFakeSource([]wa.ScriptStep{wa.NewHistoryStep(spec, nil)})
		policy, _ := syncpolicy.New(values.Retention)
		core, err := service.New(integrationOptions(values), source, memory, policy, service.NewSystemClock())
		if err != nil {
			t.Fatal(err)
		}
		if err := core.Run(context.Background()); err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		for range core.Updates() {
		}
		if source.Status().BulkDropped() != 0 {
			t.Fatalf("iteration %d leaked source token", iteration)
		}
	}
}

func integrationOptions(values config.Values) service.Options {
	return service.Options{
		Realtime: service.QueueOptions{Entries: values.Queues.Realtime.Entries, Bytes: values.Queues.Realtime.Bytes}, History: service.QueueOptions{Entries: values.Queues.History.Entries, Bytes: values.Queues.History.Bytes},
		LiveWrites: service.QueueOptions{Entries: values.Queues.LiveWrites.Entries, Bytes: values.Queues.LiveWrites.Bytes}, HistoryWrites: service.QueueOptions{Entries: values.Queues.HistoryWrites.Entries, Bytes: values.Queues.HistoryWrites.Bytes},
		ViewUpdates: service.QueueOptions{Entries: values.Queues.ViewUpdates.Entries, Bytes: values.Queues.ViewUpdates.Bytes}, HistoryChunkRecords: values.History.ChunkRecords, HistoryChunkBytes: values.History.ChunkBytes,
		BatchMaxOperations: values.Batch.MaxOperations, RetentionSnapshotLimit: values.Retention.MessagesPerChat + values.Batch.MaxOperations + 1, BatchWait: values.Batch.MaxWait, LiveWriteBusy: values.Queues.LiveWriteBusy, ShutdownGrace: 2 * time.Second,
	}
}
