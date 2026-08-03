package config

import (
	"fmt"
	"time"
)

const (
	kibibyte int64 = 1 << 10
	mebibyte int64 = 1 << 20
	day            = 24 * time.Hour

	maxAggregateQueueBytes int64 = 13 * mebibyte / 2
	maxRetentionSummaries        = 551
	maxIntValue                  = int(^uint(0) >> 1)
	maxInt64Value                = int64(^uint64(0) >> 1)
)

// QueueLimit bounds one queue by both entries and variable bytes.
type QueueLimit struct {
	Entries int
	Bytes   int64
}

// Retention contains the dependency-free retention design values.
type Retention struct {
	MessagesPerChat int
	MaxAge          time.Duration
	CacheBytes      int64
	MaxChats        int
}

// Queues contains every configurable Milestone 1A queue bound.
type Queues struct {
	Realtime      QueueLimit
	History       QueueLimit
	Commands      QueueLimit
	LiveWrites    QueueLimit
	HistoryWrites QueueLimit
	ViewUpdates   QueueLimit
	LiveWriteBusy time.Duration
}

// History contains the fixed-chunk construction limits.
type History struct {
	ChunkRecords int
	ChunkBytes   int64
}

// Batch bounds store-writer batches by operations and elapsed time.
type Batch struct {
	MaxOperations int
	MaxWait       time.Duration
}

// Values is the complete dependency-free Milestone 1A configuration.
type Values struct {
	Retention Retention
	Queues    Queues
	History   History
	Batch     Batch
}

// Field identifies one configuration value or checked aggregate.
type Field string

const (
	FieldRetentionMessagesPerChat Field = "retention.messages_per_chat"
	FieldRetentionMaxAge          Field = "retention.max_age"
	FieldRetentionCacheBytes      Field = "retention.cache_bytes"
	FieldRetentionMaxChats        Field = "retention.max_chats"
	FieldRetentionSummaryRecords  Field = "retention.summary_records"

	FieldQueuesRealtimeEntries      Field = "queues.realtime.entries"
	FieldQueuesRealtimeBytes        Field = "queues.realtime.bytes"
	FieldQueuesHistoryEntries       Field = "queues.history.entries"
	FieldQueuesHistoryBytes         Field = "queues.history.bytes"
	FieldQueuesCommandsEntries      Field = "queues.commands.entries"
	FieldQueuesCommandsBytes        Field = "queues.commands.bytes"
	FieldQueuesLiveWritesEntries    Field = "queues.live_writes.entries"
	FieldQueuesLiveWritesBytes      Field = "queues.live_writes.bytes"
	FieldQueuesHistoryWritesEntries Field = "queues.history_writes.entries"
	FieldQueuesHistoryWritesBytes   Field = "queues.history_writes.bytes"
	FieldQueuesViewUpdatesEntries   Field = "queues.view_updates.entries"
	FieldQueuesViewUpdatesBytes     Field = "queues.view_updates.bytes"
	FieldQueuesLiveWriteBusy        Field = "queues.live_write_busy"
	FieldQueuesTotalBytes           Field = "queues.total_bytes"

	FieldHistoryChunkRecords Field = "history.chunk_records"
	FieldHistoryChunkBytes   Field = "history.chunk_bytes"

	FieldBatchMaxOperations Field = "batch.max_operations"
	FieldBatchMaxWait       Field = "batch.max_wait"
)

// ValidationRule identifies why a configuration value was rejected.
type ValidationRule string

const (
	Required          ValidationRule = "required"
	OutOfRange        ValidationRule = "out_of_range"
	Overflow          ValidationRule = "overflow"
	AggregateExceeded ValidationRule = "aggregate_exceeded"
)

// ValidationError exposes numeric validation metadata without requiring
// callers to match its rendered text. For Overflow, Value is the largest
// representable value of the aggregate's integer type.
type ValidationError struct {
	Field Field
	Rule  ValidationRule
	Value int64
	Min   int64
	Max   int64
}

// Error implements error.
func (err *ValidationError) Error() string {
	if err == nil {
		return "configuration validation failed"
	}
	return fmt.Sprintf(
		"configuration validation failed: field %s violates %s (value %d, min %d, max %d)",
		err.Field,
		err.Rule,
		err.Value,
		err.Min,
		err.Max,
	)
}

// DefaultValues returns a new value containing the Milestone 1A defaults.
func DefaultValues() Values {
	return Values{
		Retention: Retention{
			MessagesPerChat: 100,
			MaxAge:          90 * day,
			CacheBytes:      250 * mebibyte,
			MaxChats:        10_000,
		},
		Queues: Queues{
			Realtime: QueueLimit{
				Entries: 128,
				Bytes:   2 * mebibyte,
			},
			History: QueueLimit{
				Entries: 4,
				Bytes:   512 * kibibyte,
			},
			Commands: QueueLimit{
				Entries: 32,
				Bytes:   512 * kibibyte,
			},
			LiveWrites: QueueLimit{
				Entries: 128,
				Bytes:   mebibyte,
			},
			HistoryWrites: QueueLimit{
				Entries: 256,
				Bytes:   2 * mebibyte,
			},
			ViewUpdates: QueueLimit{
				Entries: 64,
				Bytes:   512 * kibibyte,
			},
			LiveWriteBusy: 25 * time.Millisecond,
		},
		History: History{
			ChunkRecords: 32,
			ChunkBytes:   128 * kibibyte,
		},
		Batch: Batch{
			MaxOperations: 50,
			MaxWait:       25 * time.Millisecond,
		},
	}
}

// Validate checks the retention values independently of the other settings.
func (retention Retention) Validate() error {
	limits := [...]numericLimit{
		{FieldRetentionMessagesPerChat, int64(retention.MessagesPerChat), 20, 500},
		{FieldRetentionMaxAge, int64(retention.MaxAge), int64(day), int64(365 * day)},
		{FieldRetentionCacheBytes, retention.CacheBytes, 32 * mebibyte, 2048 * mebibyte},
		{FieldRetentionMaxChats, int64(retention.MaxChats), 100, 10_000},
	}
	return validateRanges(limits[:])
}

// Validate checks every individual range and cross-field bound without
// mutating the supplied value.
func (values Values) Validate() error {
	limits := limitsFor(values)
	if err := validatePositive(limits[:]); err != nil {
		return err
	}

	summaryRecords, err := retentionSummaryRecords(
		values.Retention.MessagesPerChat,
		values.Batch.MaxOperations,
	)
	if err != nil {
		return err
	}

	totalQueueBytes, err := queueBytesTotal(values.Queues)
	if err != nil {
		return err
	}

	if err := values.Retention.Validate(); err != nil {
		return err
	}
	if err := validateRanges(limits[4:]); err != nil {
		return err
	}
	if err := validateRetentionSummaryRecords(summaryRecords); err != nil {
		return err
	}
	if totalQueueBytes > maxAggregateQueueBytes {
		return newValidationError(
			FieldQueuesTotalBytes,
			AggregateExceeded,
			totalQueueBytes,
			1,
			maxAggregateQueueBytes,
		)
	}

	if err := validateHistoryChunkCapacity(
		values.History.ChunkBytes,
		values.Queues.History.Bytes,
	); err != nil {
		return err
	}

	writeEntryLimit := values.Queues.LiveWrites.Entries
	if values.Queues.HistoryWrites.Entries < writeEntryLimit {
		writeEntryLimit = values.Queues.HistoryWrites.Entries
	}
	if values.Batch.MaxOperations > writeEntryLimit {
		return newValidationError(
			FieldBatchMaxOperations,
			AggregateExceeded,
			int64(values.Batch.MaxOperations),
			1,
			int64(writeEntryLimit),
		)
	}

	return nil
}

type numericLimit struct {
	field Field
	value int64
	min   int64
	max   int64
}

type valueLimits [21]numericLimit

func limitsFor(values Values) valueLimits {
	return valueLimits{
		{FieldRetentionMessagesPerChat, int64(values.Retention.MessagesPerChat), 20, 500},
		{FieldRetentionMaxAge, int64(values.Retention.MaxAge), int64(day), int64(365 * day)},
		{FieldRetentionCacheBytes, values.Retention.CacheBytes, 32 * mebibyte, 2048 * mebibyte},
		{FieldRetentionMaxChats, int64(values.Retention.MaxChats), 100, 10_000},
		{FieldQueuesRealtimeEntries, int64(values.Queues.Realtime.Entries), 32, 512},
		{FieldQueuesRealtimeBytes, values.Queues.Realtime.Bytes, 512 * kibibyte, 4 * mebibyte},
		{FieldQueuesHistoryEntries, int64(values.Queues.History.Entries), 1, 16},
		{FieldQueuesHistoryBytes, values.Queues.History.Bytes, 128 * kibibyte, 2 * mebibyte},
		{FieldQueuesCommandsEntries, int64(values.Queues.Commands.Entries), 1, 64},
		{FieldQueuesCommandsBytes, values.Queues.Commands.Bytes, 64 * kibibyte, mebibyte},
		{FieldQueuesLiveWritesEntries, int64(values.Queues.LiveWrites.Entries), 16, 256},
		{FieldQueuesLiveWritesBytes, values.Queues.LiveWrites.Bytes, 256 * kibibyte, 2 * mebibyte},
		{FieldQueuesHistoryWritesEntries, int64(values.Queues.HistoryWrites.Entries), 16, 512},
		{FieldQueuesHistoryWritesBytes, values.Queues.HistoryWrites.Bytes, 512 * kibibyte, 4 * mebibyte},
		{FieldQueuesViewUpdatesEntries, int64(values.Queues.ViewUpdates.Entries), 64, 64},
		{FieldQueuesViewUpdatesBytes, values.Queues.ViewUpdates.Bytes, 128 * kibibyte, mebibyte},
		{FieldQueuesLiveWriteBusy, int64(values.Queues.LiveWriteBusy), int64(5 * time.Millisecond), int64(100 * time.Millisecond)},
		{FieldHistoryChunkRecords, int64(values.History.ChunkRecords), 1, 32},
		{FieldHistoryChunkBytes, values.History.ChunkBytes, 64 * kibibyte, 128 * kibibyte},
		{FieldBatchMaxOperations, int64(values.Batch.MaxOperations), 1, 50},
		{FieldBatchMaxWait, int64(values.Batch.MaxWait), int64(5 * time.Millisecond), int64(100 * time.Millisecond)},
	}
}

func validatePositive(limits []numericLimit) error {
	for _, limit := range limits {
		if limit.value == 0 {
			return newValidationError(
				limit.field,
				Required,
				limit.value,
				limit.min,
				limit.max,
			)
		}
		if limit.value < 0 {
			return newValidationError(
				limit.field,
				OutOfRange,
				limit.value,
				limit.min,
				limit.max,
			)
		}
	}
	return nil
}

func validateRanges(limits []numericLimit) error {
	if err := validatePositive(limits); err != nil {
		return err
	}
	for _, limit := range limits {
		if limit.value < limit.min || limit.value > limit.max {
			return newValidationError(
				limit.field,
				OutOfRange,
				limit.value,
				limit.min,
				limit.max,
			)
		}
	}
	return nil
}

func retentionSummaryRecords(messagesPerChat, batchOperations int) (int, error) {
	total, ok := checkedAddInt(messagesPerChat, batchOperations)
	if ok {
		total, ok = checkedAddInt(total, 1)
	}
	if !ok {
		return 0, newValidationError(
			FieldRetentionSummaryRecords,
			Overflow,
			int64(maxIntValue),
			1,
			maxRetentionSummaries,
		)
	}
	return total, nil
}

func validateRetentionSummaryRecords(summaryRecords int) error {
	if summaryRecords <= maxRetentionSummaries {
		return nil
	}
	return newValidationError(
		FieldRetentionSummaryRecords,
		AggregateExceeded,
		int64(summaryRecords),
		1,
		maxRetentionSummaries,
	)
}

func validateHistoryChunkCapacity(chunkBytes, queueBytes int64) error {
	if chunkBytes <= queueBytes {
		return nil
	}
	return newValidationError(
		FieldHistoryChunkBytes,
		AggregateExceeded,
		chunkBytes,
		64*kibibyte,
		queueBytes,
	)
}

func queueBytesTotal(queues Queues) (int64, error) {
	budgets := [...]int64{
		queues.Realtime.Bytes,
		queues.History.Bytes,
		queues.Commands.Bytes,
		queues.LiveWrites.Bytes,
		queues.HistoryWrites.Bytes,
		queues.ViewUpdates.Bytes,
	}

	var total int64
	for _, budget := range budgets {
		next, ok := checkedAddInt64(total, budget)
		if !ok {
			return 0, newValidationError(
				FieldQueuesTotalBytes,
				Overflow,
				maxInt64Value,
				1,
				maxAggregateQueueBytes,
			)
		}
		total = next
	}
	return total, nil
}

func checkedAddInt(left, right int) (int, bool) {
	const minIntValue = -maxIntValue - 1

	if right > 0 && left > maxIntValue-right {
		return 0, false
	}
	if right < 0 && left < minIntValue-right {
		return 0, false
	}
	return left + right, true
}

func checkedAddInt64(left, right int64) (int64, bool) {
	const minInt64Value = -maxInt64Value - 1

	if right > 0 && left > maxInt64Value-right {
		return 0, false
	}
	if right < 0 && left < minInt64Value-right {
		return 0, false
	}
	return left + right, true
}

func newValidationError(
	field Field,
	rule ValidationRule,
	value int64,
	minValue int64,
	maxValue int64,
) *ValidationError {
	return &ValidationError{
		Field: field,
		Rule:  rule,
		Value: value,
		Min:   minValue,
		Max:   maxValue,
	}
}
