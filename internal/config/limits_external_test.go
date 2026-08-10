package config_test

import (
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const testKiB int64 = 1 << 10

func TestDefaultLimitsFitModelValues(t *testing.T) {
	t.Parallel()

	values := config.DefaultValues()
	if err := values.Validate(); err != nil {
		t.Fatalf("DefaultValues().Validate(): %v", err)
	}

	requireFits(t, "normalized event in realtime queue", model.MaxNormalizedEventBytes, values.Queues.Realtime.Bytes)
	requireFits(t, "normalized event in live-write queue", model.MaxNormalizedEventBytes, values.Queues.LiveWrites.Bytes)
	requireFits(t, "normalized update in view mailbox", model.MaxNormalizedUpdateBytes, values.Queues.ViewUpdates.Bytes)
	requireFits(t, "history chunk in history queue", model.MaxHistoryChunkBytes, values.Queues.History.Bytes)

	if values.History.ChunkRecords > model.MaxHistoryChunkRecords {
		t.Fatalf(
			"default history chunk records = %d, exceeds model maximum %d",
			values.History.ChunkRecords,
			model.MaxHistoryChunkRecords,
		)
	}
	if values.History.ChunkBytes > int64(model.MaxHistoryChunkBytes) {
		t.Fatalf(
			"default history chunk bytes = %d, exceeds model maximum %d",
			values.History.ChunkBytes,
			model.MaxHistoryChunkBytes,
		)
	}

	aboveModelRecords := config.DefaultValues()
	aboveModelRecords.History.ChunkRecords = model.MaxHistoryChunkRecords + 1
	if err := aboveModelRecords.Validate(); err == nil {
		t.Fatalf(
			"configuration accepted %d history records above model maximum %d",
			aboveModelRecords.History.ChunkRecords,
			model.MaxHistoryChunkRecords,
		)
	}

	aboveModelBytes := config.DefaultValues()
	aboveModelBytes.History.ChunkBytes = int64(model.MaxHistoryChunkBytes) + 1
	if err := aboveModelBytes.Validate(); err == nil {
		t.Fatalf(
			"configuration accepted %d history bytes above model maximum %d",
			aboveModelBytes.History.ChunkBytes,
			model.MaxHistoryChunkBytes,
		)
	}
}

func TestAcceptedExtremaFitModelValues(t *testing.T) {
	t.Parallel()

	const (
		minimumRealtimeBytes  = 512 * testKiB
		minimumHistoryBytes   = 128 * testKiB
		minimumLiveWriteBytes = 256 * testKiB
		minimumViewBytes      = 128 * testKiB
		maximumChunkRecords   = 32
		maximumChunkBytes     = 128 * testKiB
	)

	values := config.DefaultValues()
	values.Queues.Realtime.Bytes = minimumRealtimeBytes
	values.Queues.History.Bytes = minimumHistoryBytes
	values.Queues.LiveWrites.Bytes = minimumLiveWriteBytes
	values.Queues.ViewUpdates.Bytes = minimumViewBytes
	values.History.ChunkRecords = maximumChunkRecords
	values.History.ChunkBytes = maximumChunkBytes
	if err := values.Validate(); err != nil {
		t.Fatalf("valid boundary Values.Validate(): %v", err)
	}

	requireFits(t, "normalized event in minimum realtime queue", model.MaxNormalizedEventBytes, values.Queues.Realtime.Bytes)
	requireFits(t, "normalized event in minimum live-write queue", model.MaxNormalizedEventBytes, values.Queues.LiveWrites.Bytes)
	requireFits(t, "normalized update in minimum view mailbox", model.MaxNormalizedUpdateBytes, values.Queues.ViewUpdates.Bytes)
	requireFits(t, "history chunk in minimum history queue", model.MaxHistoryChunkBytes, values.Queues.History.Bytes)

	if values.History.ChunkRecords > model.MaxHistoryChunkRecords {
		t.Fatalf(
			"maximum accepted history chunk records = %d, exceeds model maximum %d",
			values.History.ChunkRecords,
			model.MaxHistoryChunkRecords,
		)
	}
	if values.History.ChunkBytes > int64(model.MaxHistoryChunkBytes) {
		t.Fatalf(
			"maximum accepted history chunk bytes = %d, exceeds model maximum %d",
			values.History.ChunkBytes,
			model.MaxHistoryChunkBytes,
		)
	}
}

func requireFits(t *testing.T, name string, value int, capacity int64) {
	t.Helper()
	if int64(value) > capacity {
		t.Fatalf("%s: model value %d exceeds config capacity %d", name, value, capacity)
	}
}
