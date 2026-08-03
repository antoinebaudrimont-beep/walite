package config

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultValues(t *testing.T) {
	t.Parallel()

	want := Values{
		Retention: Retention{
			MessagesPerChat: 100,
			MaxAge:          90 * day,
			CacheBytes:      250 * mebibyte,
			MaxChats:        10_000,
		},
		Queues: Queues{
			Realtime:      QueueLimit{Entries: 128, Bytes: 2 * mebibyte},
			History:       QueueLimit{Entries: 4, Bytes: 512 * kibibyte},
			Commands:      QueueLimit{Entries: 32, Bytes: 512 * kibibyte},
			LiveWrites:    QueueLimit{Entries: 128, Bytes: mebibyte},
			HistoryWrites: QueueLimit{Entries: 256, Bytes: 2 * mebibyte},
			ViewUpdates:   QueueLimit{Entries: 64, Bytes: 512 * kibibyte},
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

	got := DefaultValues()
	if got != want {
		t.Fatalf("DefaultValues() = %#v, want %#v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("DefaultValues().Validate(): %v", err)
	}
	total, err := queueBytesTotal(got.Queues)
	if err != nil {
		t.Fatalf("queueBytesTotal(defaults): %v", err)
	}
	if total != maxAggregateQueueBytes {
		t.Fatalf(
			"default queue bytes = %d, want exactly %d",
			total,
			maxAggregateQueueBytes,
		)
	}
}

func TestRetentionValidate(t *testing.T) {
	t.Parallel()

	retention := DefaultValues().Retention
	if err := retention.Validate(); err != nil {
		t.Fatalf("default Retention.Validate(): %v", err)
	}

	for _, boundary := range configurationBoundaries()[:4] {
		boundary := boundary
		t.Run(boundary.name+"/lower", func(t *testing.T) {
			values := boundaryValues()
			boundary.set(&values, boundary.lower)
			if err := values.Retention.Validate(); err != nil {
				t.Fatalf("Retention.Validate() at lower boundary: %v", err)
			}
		})
		t.Run(boundary.name+"/upper", func(t *testing.T) {
			values := boundaryValues()
			boundary.set(&values, boundary.upper)
			if err := values.Retention.Validate(); err != nil {
				t.Fatalf("Retention.Validate() at upper boundary: %v", err)
			}
		})
		for _, test := range []struct {
			name  string
			value int64
			rule  ValidationRule
		}{
			{name: "below", value: boundary.lower - 1, rule: OutOfRange},
			{name: "above", value: boundary.upper + 1, rule: OutOfRange},
			{name: "zero", value: 0, rule: Required},
			{name: "negative", value: -1, rule: OutOfRange},
		} {
			test := test
			t.Run(boundary.name+"/"+test.name, func(t *testing.T) {
				values := boundaryValues()
				boundary.set(&values, test.value)
				rule := test.rule
				if test.name == "below" && test.value == 0 {
					rule = Required
				}
				requireValidationError(
					t,
					values.Retention.Validate(),
					boundary.field,
					rule,
					test.value,
					boundary.lower,
					boundary.upper,
				)
			})
		}
	}
}

func TestEveryExactBoundary(t *testing.T) {
	t.Parallel()

	for _, boundary := range configurationBoundaries() {
		boundary := boundary
		t.Run(boundary.name+"/lower", func(t *testing.T) {
			values := boundaryValues()
			boundary.set(&values, boundary.lower)
			if err := values.Validate(); err != nil {
				t.Fatalf("Validate() at exact lower boundary %d: %v", boundary.lower, err)
			}
		})
		t.Run(boundary.name+"/upper", func(t *testing.T) {
			values := boundaryValues()
			boundary.set(&values, boundary.upper)
			if err := values.Validate(); err != nil {
				t.Fatalf("Validate() at exact upper boundary %d: %v", boundary.upper, err)
			}
		})
	}
}

func TestOneBelowAndAboveEveryBoundary(t *testing.T) {
	t.Parallel()

	for _, boundary := range configurationBoundaries() {
		boundary := boundary
		t.Run(boundary.name+"/below", func(t *testing.T) {
			values := boundaryValues()
			value := boundary.lower - 1
			boundary.set(&values, value)
			rule := OutOfRange
			if value == 0 {
				rule = Required
			}
			requireValidationError(
				t,
				values.Validate(),
				boundary.field,
				rule,
				value,
				boundary.lower,
				boundary.upper,
			)
		})
		t.Run(boundary.name+"/above", func(t *testing.T) {
			values := boundaryValues()
			value := boundary.upper + 1
			boundary.set(&values, value)
			requireValidationError(
				t,
				values.Validate(),
				boundary.field,
				OutOfRange,
				value,
				boundary.lower,
				boundary.upper,
			)
		})
	}
}

func TestZeroAndNegativeValues(t *testing.T) {
	t.Parallel()

	for _, boundary := range configurationBoundaries() {
		boundary := boundary
		t.Run(boundary.name+"/zero", func(t *testing.T) {
			values := boundaryValues()
			boundary.set(&values, 0)
			requireValidationError(
				t,
				values.Validate(),
				boundary.field,
				Required,
				0,
				boundary.lower,
				boundary.upper,
			)
		})
		t.Run(boundary.name+"/negative", func(t *testing.T) {
			values := boundaryValues()
			boundary.set(&values, -1)
			requireValidationError(
				t,
				values.Validate(),
				boundary.field,
				OutOfRange,
				-1,
				boundary.lower,
				boundary.upper,
			)
		})
	}
}

func TestViewUpdateEntriesMustRemainFixed(t *testing.T) {
	t.Parallel()

	for _, entries := range []int{63, 65} {
		values := boundaryValues()
		values.Queues.ViewUpdates.Entries = entries
		requireValidationError(
			t,
			values.Validate(),
			FieldQueuesViewUpdatesEntries,
			OutOfRange,
			int64(entries),
			64,
			64,
		)
	}
}

func TestAggregateQueueByteBudget(t *testing.T) {
	t.Parallel()

	t.Run("exactly 6.5 MiB", func(t *testing.T) {
		values := DefaultValues()
		total, err := queueBytesTotal(values.Queues)
		if err != nil {
			t.Fatalf("queueBytesTotal: %v", err)
		}
		if total != maxAggregateQueueBytes {
			t.Fatalf("queue total = %d, want %d", total, maxAggregateQueueBytes)
		}
		if err := values.Validate(); err != nil {
			t.Fatalf("Validate() at exact aggregate limit: %v", err)
		}
	})

	t.Run("above 6.5 MiB", func(t *testing.T) {
		values := DefaultValues()
		values.Queues.Commands.Bytes++
		requireValidationError(
			t,
			values.Validate(),
			FieldQueuesTotalBytes,
			AggregateExceeded,
			maxAggregateQueueBytes+1,
			1,
			maxAggregateQueueBytes,
		)
	})

	t.Run("integer overflow", func(t *testing.T) {
		tests := []struct {
			name string
			set  func(*Values)
		}{
			{
				name: "first budget",
				set: func(values *Values) {
					values.Queues.Realtime.Bytes = maxInt64Value
				},
			},
			{
				name: "later budget",
				set: func(values *Values) {
					values.Queues.History.Bytes = maxInt64Value
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				values := boundaryValues()
				test.set(&values)
				requireValidationError(
					t,
					values.Validate(),
					FieldQueuesTotalBytes,
					Overflow,
					maxInt64Value,
					1,
					maxAggregateQueueBytes,
				)
			})
		}
	})
}

func TestHistoryChunkMustFitHistoryQueue(t *testing.T) {
	t.Parallel()

	values := boundaryValues()
	values.Queues.History.Bytes = 64 * kibibyte
	values.History.ChunkBytes = 64*kibibyte + 1
	requireValidationError(
		t,
		values.Validate(),
		FieldQueuesHistoryBytes,
		OutOfRange,
		64*kibibyte,
		128*kibibyte,
		2*mebibyte,
	)

	requireValidationError(
		t,
		validateHistoryChunkCapacity(64*kibibyte+1, 64*kibibyte),
		FieldHistoryChunkBytes,
		AggregateExceeded,
		64*kibibyte+1,
		64*kibibyte,
		64*kibibyte,
	)
}

func TestBatchOperationsMustFitBothWriteQueues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		liveEntries    int
		historyEntries int
	}{
		{name: "live writes", liveEntries: 16, historyEntries: 512},
		{name: "history writes", liveEntries: 256, historyEntries: 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := boundaryValues()
			values.Queues.LiveWrites.Entries = test.liveEntries
			values.Queues.HistoryWrites.Entries = test.historyEntries
			values.Batch.MaxOperations = 17
			requireValidationError(
				t,
				values.Validate(),
				FieldBatchMaxOperations,
				AggregateExceeded,
				17,
				1,
				16,
			)
		})
	}
}

func TestRetentionSummaryCalculation(t *testing.T) {
	t.Parallel()

	t.Run("exactly 551", func(t *testing.T) {
		values := DefaultValues()
		values.Retention.MessagesPerChat = 500
		values.Batch.MaxOperations = 50
		got, err := retentionSummaryRecords(
			values.Retention.MessagesPerChat,
			values.Batch.MaxOperations,
		)
		if err != nil {
			t.Fatalf("retentionSummaryRecords: %v", err)
		}
		if got != maxRetentionSummaries {
			t.Fatalf("summary records = %d, want %d", got, maxRetentionSummaries)
		}
		if err := values.Validate(); err != nil {
			t.Fatalf("Validate() at exact summary limit: %v", err)
		}
	})

	t.Run("above 551", func(t *testing.T) {
		values := DefaultValues()
		values.Retention.MessagesPerChat = 501
		values.Batch.MaxOperations = 50
		requireValidationError(
			t,
			values.Validate(),
			FieldRetentionMessagesPerChat,
			OutOfRange,
			501,
			20,
			500,
		)

		summaryRecords, err := retentionSummaryRecords(501, 50)
		if err != nil {
			t.Fatalf("retentionSummaryRecords: %v", err)
		}
		requireValidationError(
			t,
			validateRetentionSummaryRecords(summaryRecords),
			FieldRetentionSummaryRecords,
			AggregateExceeded,
			552,
			1,
			maxRetentionSummaries,
		)
	})

	t.Run("integer overflow", func(t *testing.T) {
		tests := []struct {
			name     string
			messages int
			batch    int
		}{
			{name: "messages operand", messages: maxIntValue, batch: 1},
			{name: "batch operand", messages: 20, batch: maxIntValue},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				values := boundaryValues()
				values.Retention.MessagesPerChat = test.messages
				values.Batch.MaxOperations = test.batch
				requireValidationError(
					t,
					values.Validate(),
					FieldRetentionSummaryRecords,
					Overflow,
					int64(maxIntValue),
					1,
					maxRetentionSummaries,
				)
			})
		}
	})
}

func TestValidationDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values Values
	}{
		{name: "valid", values: DefaultValues()},
		{
			name: "invalid",
			values: func() Values {
				values := DefaultValues()
				values.Queues.Commands.Bytes++
				return values
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.values
			_ = test.values.Validate()
			if test.values != before {
				t.Fatalf("Validate() mutated input: got %#v, want %#v", test.values, before)
			}
		})
	}
}

func TestDefaultValuesAreIndependent(t *testing.T) {
	t.Parallel()

	first := DefaultValues()
	second := DefaultValues()
	wantSecond := second

	first.Retention.MessagesPerChat = 20
	first.Queues.Realtime.Bytes = 512 * kibibyte
	first.History.ChunkRecords = 1
	first.Batch.MaxWait = 5 * time.Millisecond

	if second != wantSecond {
		t.Fatalf("mutating one DefaultValues result changed another: got %#v, want %#v", second, wantSecond)
	}
}

type configurationBoundary struct {
	name         string
	field        Field
	lower, upper int64
	set          func(*Values, int64)
}

func configurationBoundaries() []configurationBoundary {
	return []configurationBoundary{
		{
			name: "retention messages per chat", field: FieldRetentionMessagesPerChat,
			lower: 20, upper: 500,
			set: func(values *Values, value int64) {
				values.Retention.MessagesPerChat = int(value)
			},
		},
		{
			name: "retention max age", field: FieldRetentionMaxAge,
			lower: int64(day), upper: int64(365 * day),
			set: func(values *Values, value int64) {
				values.Retention.MaxAge = time.Duration(value)
			},
		},
		{
			name: "retention cache bytes", field: FieldRetentionCacheBytes,
			lower: 32 * mebibyte, upper: 2048 * mebibyte,
			set: func(values *Values, value int64) {
				values.Retention.CacheBytes = value
			},
		},
		{
			name: "retention max chats", field: FieldRetentionMaxChats,
			lower: 100, upper: 10_000,
			set: func(values *Values, value int64) {
				values.Retention.MaxChats = int(value)
			},
		},
		{
			name: "realtime entries", field: FieldQueuesRealtimeEntries,
			lower: 32, upper: 512,
			set: func(values *Values, value int64) {
				values.Queues.Realtime.Entries = int(value)
			},
		},
		{
			name: "realtime bytes", field: FieldQueuesRealtimeBytes,
			lower: 512 * kibibyte, upper: 4 * mebibyte,
			set: func(values *Values, value int64) {
				values.Queues.Realtime.Bytes = value
			},
		},
		{
			name: "history entries", field: FieldQueuesHistoryEntries,
			lower: 1, upper: 16,
			set: func(values *Values, value int64) {
				values.Queues.History.Entries = int(value)
			},
		},
		{
			name: "history bytes", field: FieldQueuesHistoryBytes,
			lower: 128 * kibibyte, upper: 2 * mebibyte,
			set: func(values *Values, value int64) {
				values.Queues.History.Bytes = value
			},
		},
		{
			name: "commands entries", field: FieldQueuesCommandsEntries,
			lower: 1, upper: 64,
			set: func(values *Values, value int64) {
				values.Queues.Commands.Entries = int(value)
			},
		},
		{
			name: "commands bytes", field: FieldQueuesCommandsBytes,
			lower: 64 * kibibyte, upper: mebibyte,
			set: func(values *Values, value int64) {
				values.Queues.Commands.Bytes = value
			},
		},
		{
			name: "live writes entries", field: FieldQueuesLiveWritesEntries,
			lower: 16, upper: 256,
			set: func(values *Values, value int64) {
				values.Queues.LiveWrites.Entries = int(value)
			},
		},
		{
			name: "live writes bytes", field: FieldQueuesLiveWritesBytes,
			lower: 256 * kibibyte, upper: 2 * mebibyte,
			set: func(values *Values, value int64) {
				values.Queues.LiveWrites.Bytes = value
			},
		},
		{
			name: "history writes entries", field: FieldQueuesHistoryWritesEntries,
			lower: 16, upper: 512,
			set: func(values *Values, value int64) {
				values.Queues.HistoryWrites.Entries = int(value)
			},
		},
		{
			name: "history writes bytes", field: FieldQueuesHistoryWritesBytes,
			lower: 512 * kibibyte, upper: 4 * mebibyte,
			set: func(values *Values, value int64) {
				values.Queues.HistoryWrites.Bytes = value
			},
		},
		{
			name: "view updates entries", field: FieldQueuesViewUpdatesEntries,
			lower: 64, upper: 64,
			set: func(values *Values, value int64) {
				values.Queues.ViewUpdates.Entries = int(value)
			},
		},
		{
			name: "view updates bytes", field: FieldQueuesViewUpdatesBytes,
			lower: 128 * kibibyte, upper: mebibyte,
			set: func(values *Values, value int64) {
				values.Queues.ViewUpdates.Bytes = value
			},
		},
		{
			name: "live write busy", field: FieldQueuesLiveWriteBusy,
			lower: int64(5 * time.Millisecond), upper: int64(100 * time.Millisecond),
			set: func(values *Values, value int64) {
				values.Queues.LiveWriteBusy = time.Duration(value)
			},
		},
		{
			name: "history chunk records", field: FieldHistoryChunkRecords,
			lower: 1, upper: 32,
			set: func(values *Values, value int64) {
				values.History.ChunkRecords = int(value)
			},
		},
		{
			name: "history chunk bytes", field: FieldHistoryChunkBytes,
			lower: 64 * kibibyte, upper: 128 * kibibyte,
			set: func(values *Values, value int64) {
				values.History.ChunkBytes = value
			},
		},
		{
			name: "batch max operations", field: FieldBatchMaxOperations,
			lower: 1, upper: 50,
			set: func(values *Values, value int64) {
				values.Batch.MaxOperations = int(value)
			},
		},
		{
			name: "batch max wait", field: FieldBatchMaxWait,
			lower: int64(5 * time.Millisecond), upper: int64(100 * time.Millisecond),
			set: func(values *Values, value int64) {
				values.Batch.MaxWait = time.Duration(value)
			},
		},
	}
}

func boundaryValues() Values {
	values := DefaultValues()
	values.Retention.MessagesPerChat = 20
	values.Queues.Realtime.Bytes = 512 * kibibyte
	values.Queues.History.Bytes = 512 * kibibyte
	values.Queues.Commands.Bytes = 64 * kibibyte
	values.Queues.LiveWrites.Bytes = 256 * kibibyte
	values.Queues.HistoryWrites.Bytes = 512 * kibibyte
	values.Queues.ViewUpdates.Bytes = 128 * kibibyte
	values.History.ChunkBytes = 64 * kibibyte
	values.Batch.MaxOperations = 1
	return values
}

func requireValidationError(
	t *testing.T,
	err error,
	wantField Field,
	wantRule ValidationRule,
	wantValue int64,
	wantMin int64,
	wantMax int64,
) *ValidationError {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want *ValidationError")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if validationErr.Field != wantField {
		t.Errorf("ValidationError.Field = %q, want %q", validationErr.Field, wantField)
	}
	if validationErr.Rule != wantRule {
		t.Errorf("ValidationError.Rule = %q, want %q", validationErr.Rule, wantRule)
	}
	if validationErr.Value != wantValue {
		t.Errorf("ValidationError.Value = %d, want %d", validationErr.Value, wantValue)
	}
	if validationErr.Min != wantMin {
		t.Errorf("ValidationError.Min = %d, want %d", validationErr.Min, wantMin)
	}
	if validationErr.Max != wantMax {
		t.Errorf("ValidationError.Max = %d, want %d", validationErr.Max, wantMax)
	}
	return validationErr
}
