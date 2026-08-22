package service

import (
	"context"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/syncpolicy"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

const benchmarkHistoryRecords = 100_000

func BenchmarkHistory100000(b *testing.B) {
	values := config.DefaultValues()
	var peakHistoryEntries, peakHistoryWriteEntries int
	var peakHistoryBytes, peakHistoryWriteBytes int64
	var retained int
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		memory, _ := store.NewMemory(values.Retention.MaxChats)
		policy, _ := syncpolicy.New(values.Retention)
		chat, _ := model.NewChatID("benchmark-history-chat")
		job, _ := model.NewHistoryJob("benchmark-history-job", model.HistoryBulk)
		spec, _ := wa.NewHistorySpec(job, benchmarkHistoryRecords, chat, time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC), "Synthetic benchmark history")
		source, _ := wa.NewFakeSource([]wa.ScriptStep{wa.NewHistoryStep(spec, nil)})
		core, err := New(benchmarkCoreOptions(values), source, memory, policy, NewSystemClock())
		if err != nil {
			b.Fatal(err)
		}
		samplingDone := make(chan struct{})
		sampled := make(chan struct{})
		go func() {
			defer close(sampled)
			for {
				historyStats := core.historyQ.Stats()
				historyWriteStats := core.historyWriteQ.Stats()
				if historyStats.Entries > peakHistoryEntries {
					peakHistoryEntries = historyStats.Entries
				}
				if historyStats.UsedBytes > peakHistoryBytes {
					peakHistoryBytes = historyStats.UsedBytes
				}
				if historyWriteStats.Entries > peakHistoryWriteEntries {
					peakHistoryWriteEntries = historyWriteStats.Entries
				}
				if historyWriteStats.UsedBytes > peakHistoryWriteBytes {
					peakHistoryWriteBytes = historyWriteStats.UsedBytes
				}
				select {
				case <-samplingDone:
					return
				case <-core.historyQ.stateChanges():
				case <-core.historyWriteQ.stateChanges():
				}
			}
		}()
		b.StartTimer()
		if err := core.Run(context.Background()); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		close(samplingDone)
		<-sampled
		for range core.Updates() {
		}
		usage, err := memory.Usage(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		retained = usage.Messages()
		b.StartTimer()
	}
	b.StopTimer()
	b.ReportMetric(float64(benchmarkHistoryRecords*b.N)/b.Elapsed().Seconds(), "records/s")
	b.ReportMetric(float64(retained), "retained-records")
	b.ReportMetric(float64(peakHistoryEntries), "peak-history-entries")
	b.ReportMetric(float64(peakHistoryBytes), "peak-history-bytes")
	b.ReportMetric(float64(peakHistoryWriteEntries), "peak-history-write-entries")
	b.ReportMetric(float64(peakHistoryWriteBytes), "peak-history-write-bytes")
}

func benchmarkCoreOptions(values config.Values) Options {
	return Options{
		Realtime: QueueOptions{Entries: values.Queues.Realtime.Entries, Bytes: values.Queues.Realtime.Bytes}, History: QueueOptions{Entries: values.Queues.History.Entries, Bytes: values.Queues.History.Bytes},
		LiveWrites: QueueOptions{Entries: values.Queues.LiveWrites.Entries, Bytes: values.Queues.LiveWrites.Bytes}, HistoryWrites: QueueOptions{Entries: values.Queues.HistoryWrites.Entries, Bytes: values.Queues.HistoryWrites.Bytes},
		ViewUpdates: QueueOptions{Entries: values.Queues.ViewUpdates.Entries, Bytes: values.Queues.ViewUpdates.Bytes}, HistoryChunkRecords: values.History.ChunkRecords, HistoryChunkBytes: values.History.ChunkBytes,
		BatchMaxOperations: values.Batch.MaxOperations, RetentionSnapshotLimit: values.Retention.MessagesPerChat + values.Batch.MaxOperations + 1, BatchWait: values.Batch.MaxWait, LiveWriteBusy: values.Queues.LiveWriteBusy, ShutdownGrace: 2 * time.Second,
	}
}
