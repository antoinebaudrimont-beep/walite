package model

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxHistoryJobIDBytes is the hard byte limit for a fake history job ID.
	MaxHistoryJobIDBytes = 128
	// MaxHistoryChunkRecords is the hard record limit for one history chunk.
	MaxHistoryChunkRecords = 32
	// MaxHistoryChunkBytes is the hard conservative charge for one history chunk.
	MaxHistoryChunkBytes = 128 * 1024

	historyChunkRecordBytes = 32
)

// InvalidValue identifies a value outside a closed model enumeration.
const InvalidValue ValidationCode = "invalid_value"

// HistoryJobID is a validated, immutable fake history job identifier.
type HistoryJobID struct {
	value string
}

// String returns the identifier exactly as supplied to NewHistoryJob.
func (id HistoryJobID) String() string {
	return id.value
}

// HistoryClass is a closed application-level history category.
type HistoryClass uint8

const (
	HistoryMetadata HistoryClass = iota + 1
	HistoryOnDemand
	HistoryBulk
	HistoryUnknown
)

// HistoryJob is an immutable descriptor for one bounded, lazily traversed job.
type HistoryJob struct {
	id    HistoryJobID
	class HistoryClass
}

// NewHistoryJob validates and takes bounded ownership of a history job ID and
// its application-level class.
func NewHistoryJob(id string, class HistoryClass) (HistoryJob, error) {
	if err := validateHistoryJobID(id); err != nil {
		return HistoryJob{}, err
	}
	if err := validateHistoryClass(class); err != nil {
		return HistoryJob{}, err
	}

	return HistoryJob{
		id:    HistoryJobID{value: strings.Clone(id)},
		class: class,
	}, nil
}

// ID returns the job's immutable identifier.
func (job HistoryJob) ID() HistoryJobID {
	return job.id
}

// Class returns the job's application-level history class.
func (job HistoryJob) Class() HistoryClass {
	return job.class
}

// HistoryChunk is an immutable bounded collection of normalized messages.
type HistoryChunk struct {
	messages []Message
	byteSize int
}

// NewHistoryChunk validates, charges, and defensively copies messages. The
// charge is each normalized message's variable bytes plus a fixed 32-byte
// per-record index allowance.
func NewHistoryChunk(messages []Message) (HistoryChunk, error) {
	if len(messages) > MaxHistoryChunkRecords {
		return HistoryChunk{}, newValidationError(
			SizeExceeded,
			"history_chunk_records",
			MaxHistoryChunkRecords,
		)
	}
	if len(messages) == 0 {
		return HistoryChunk{}, nil
	}

	var messageBytes [MaxHistoryChunkRecords]int
	owned := make([]Message, len(messages))
	for index, message := range messages {
		byteSize, err := validateMessage(message)
		if err != nil {
			return HistoryChunk{}, err
		}
		message.byteSize = byteSize
		owned[index] = message
		messageBytes[index] = byteSize
	}

	byteSize, err := historyChunkByteSize(messageBytes[:len(messages)])
	if err != nil {
		return HistoryChunk{}, err
	}

	return HistoryChunk{messages: owned, byteSize: byteSize}, nil
}

// Len returns the number of messages in the chunk.
func (chunk HistoryChunk) Len() int {
	return len(chunk.messages)
}

// At returns the message at index and whether index was in bounds.
func (chunk HistoryChunk) At(index int) (Message, bool) {
	if index < 0 || index >= len(chunk.messages) {
		return Message{}, false
	}
	return chunk.messages[index], true
}

// ByteSize returns the chunk's conservative normalized byte charge.
func (chunk HistoryChunk) ByteSize() int {
	return chunk.byteSize
}

// SourceStatusInput contains only fixed-size cumulative source status values.
type SourceStatusInput struct {
	MetadataDropped, OnDemandDropped uint64
	BulkDropped, UnknownDropped      uint64
	Degraded                         bool
}

// SourceStatus is an immutable source degradation snapshot.
type SourceStatus struct {
	metadataDropped uint64
	onDemandDropped uint64
	bulkDropped     uint64
	unknownDropped  uint64
	degraded        bool
}

// NewSourceStatus constructs an immutable fixed-size status snapshot.
func NewSourceStatus(input SourceStatusInput) SourceStatus {
	return SourceStatus{
		metadataDropped: input.MetadataDropped,
		onDemandDropped: input.OnDemandDropped,
		bulkDropped:     input.BulkDropped,
		unknownDropped:  input.UnknownDropped,
		degraded:        input.Degraded,
	}
}

// MetadataDropped returns the cumulative metadata-category drop count.
func (status SourceStatus) MetadataDropped() uint64 {
	return status.metadataDropped
}

// OnDemandDropped returns the cumulative on-demand-category drop count.
func (status SourceStatus) OnDemandDropped() uint64 {
	return status.onDemandDropped
}

// BulkDropped returns the cumulative bulk-category drop count.
func (status SourceStatus) BulkDropped() uint64 {
	return status.bulkDropped
}

// UnknownDropped returns the cumulative unknown-category drop count.
func (status SourceStatus) UnknownDropped() uint64 {
	return status.unknownDropped
}

// Degraded reports whether source completeness is degraded.
func (status SourceStatus) Degraded() bool {
	return status.degraded
}

// SourceFailureKind is a closed, content-free source failure classification.
type SourceFailureKind uint8

const (
	SourceFull SourceFailureKind = iota + 1
	SourceClosed
	SourceRejected
)

func validateHistoryJobID(value string) *ValidationError {
	if value == "" {
		return newValidationError(Required, "history_job_id", 0)
	}
	if len(value) > MaxHistoryJobIDBytes {
		return newValidationError(TooLong, "history_job_id", MaxHistoryJobIDBytes)
	}
	if !utf8.ValidString(value) {
		return newValidationError(InvalidUTF8, "history_job_id", MaxHistoryJobIDBytes)
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return newValidationError(
				ControlCharacter,
				"history_job_id",
				MaxHistoryJobIDBytes,
			)
		}
	}
	return nil
}

func validateHistoryClass(class HistoryClass) *ValidationError {
	if class == 0 {
		return newValidationError(Required, "history_class", 0)
	}
	switch class {
	case HistoryMetadata, HistoryOnDemand, HistoryBulk, HistoryUnknown:
		return nil
	default:
		return newValidationError(InvalidValue, "history_class", 0)
	}
}

func historyChunkByteSize(messageBytes []int) (int, error) {
	total := 0
	for _, byteSize := range messageBytes {
		next, ok := checkedByteSum(total, byteSize, historyChunkRecordBytes)
		if !ok || next > MaxHistoryChunkBytes {
			return 0, newValidationError(
				SizeExceeded,
				"history_chunk",
				MaxHistoryChunkBytes,
			)
		}
		total = next
	}
	return total, nil
}
