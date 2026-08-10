package model

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHistoryClasses(t *testing.T) {
	t.Parallel()

	classes := []HistoryClass{
		HistoryMetadata,
		HistoryOnDemand,
		HistoryBulk,
		HistoryUnknown,
	}
	for _, class := range classes {
		job, err := NewHistoryJob("history-a", class)
		if err != nil {
			t.Fatalf("NewHistoryJob(class %d): %v", class, err)
		}
		if got := job.Class(); got != class {
			t.Errorf("HistoryJob.Class() = %d, want %d", got, class)
		}
	}

	requireValidationError(t, historyClassError(0), Required, "history_class", 0)
	requireValidationError(
		t,
		historyClassError(HistoryClass(255)),
		InvalidValue,
		"history_class",
		0,
	)
}

func TestHistoryJobIDValidation(t *testing.T) {
	t.Parallel()

	job, err := NewHistoryJob("history-a", HistoryMetadata)
	if err != nil {
		t.Fatalf("NewHistoryJob(valid): %v", err)
	}
	if got := job.ID().String(); got != "history-a" {
		t.Fatalf("HistoryJob.ID().String() = %q, want %q", got, "history-a")
	}

	exact := strings.Repeat("h", MaxHistoryJobIDBytes)
	job, err = NewHistoryJob(exact, HistoryOnDemand)
	if err != nil {
		t.Fatalf("NewHistoryJob(exact ID limit): %v", err)
	}
	if got := len(job.ID().String()); got != MaxHistoryJobIDBytes {
		t.Fatalf("len(HistoryJob.ID()) = %d, want %d", got, MaxHistoryJobIDBytes)
	}

	tests := []struct {
		name  string
		value string
		code  ValidationCode
		limit int
	}{
		{name: "empty", code: Required},
		{
			name:  "129 bytes",
			value: strings.Repeat("h", MaxHistoryJobIDBytes+1),
			code:  TooLong,
			limit: MaxHistoryJobIDBytes,
		},
		{
			name:  "malformed UTF-8",
			value: string([]byte{'h', 0xff}),
			code:  InvalidUTF8,
			limit: MaxHistoryJobIDBytes,
		},
		{
			name:  "NUL",
			value: "history\x00a",
			code:  ControlCharacter,
			limit: MaxHistoryJobIDBytes,
		},
		{
			name:  "ASCII control",
			value: "history\na",
			code:  ControlCharacter,
			limit: MaxHistoryJobIDBytes,
		},
		{
			name:  "Unicode control",
			value: "history\u0085a",
			code:  ControlCharacter,
			limit: MaxHistoryJobIDBytes,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewHistoryJob(test.value, HistoryBulk)
			requireValidationError(
				t,
				err,
				test.code,
				"history_job_id",
				test.limit,
			)
		})
	}
}

func TestHistoryJobOwnsIdentifier(t *testing.T) {
	t.Parallel()

	identifierBytes := []byte("history-a")
	job, err := NewHistoryJob(mutableString(identifierBytes), HistoryBulk)
	if err != nil {
		t.Fatalf("NewHistoryJob: %v", err)
	}
	identifierBytes[0] = 'X'
	if got := job.ID().String(); got != "history-a" {
		t.Fatalf("HistoryJob.ID() after input mutation = %q, want %q", got, "history-a")
	}
}

func TestHistoryJobRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	_, err := NewHistoryJob("", HistoryMetadata)
	requireValidationError(t, err, Required, "history_job_id", 0)

	_, err = NewHistoryJob("history-a", HistoryClass(255))
	requireValidationError(t, err, InvalidValue, "history_class", 0)
}

func TestHistoryChunkEmpty(t *testing.T) {
	t.Parallel()

	chunk, err := NewHistoryChunk(nil)
	if err != nil {
		t.Fatalf("NewHistoryChunk(nil): %v", err)
	}
	if chunk.Len() != 0 {
		t.Fatalf("HistoryChunk.Len() = %d, want 0", chunk.Len())
	}
	if chunk.ByteSize() != 0 {
		t.Fatalf("HistoryChunk.ByteSize() = %d, want 0", chunk.ByteSize())
	}
	for _, index := range []int{-1, 0, 1} {
		if _, ok := chunk.At(index); ok {
			t.Errorf("HistoryChunk.At(%d) reported an element", index)
		}
	}
}

func TestHistoryChunkRecordLimitAndIndexedAccess(t *testing.T) {
	t.Parallel()

	messages := make([]Message, MaxHistoryChunkRecords)
	wantBytes := 0
	for index := range messages {
		messages[index] = newHistoryTestMessage(t, "")
		wantBytes += messages[index].ByteSize() + historyChunkRecordBytes
	}

	chunk, err := NewHistoryChunk(messages)
	if err != nil {
		t.Fatalf("NewHistoryChunk(32 records): %v", err)
	}
	if got := chunk.Len(); got != MaxHistoryChunkRecords {
		t.Fatalf("HistoryChunk.Len() = %d, want %d", got, MaxHistoryChunkRecords)
	}
	if got := chunk.ByteSize(); got != wantBytes {
		t.Fatalf("HistoryChunk.ByteSize() = %d, want %d", got, wantBytes)
	}
	for index := range messages {
		got, ok := chunk.At(index)
		if !ok {
			t.Fatalf("HistoryChunk.At(%d) reported out of bounds", index)
		}
		if got.MessageID() != messages[index].MessageID() {
			t.Errorf("HistoryChunk.At(%d) returned the wrong message", index)
		}
	}
	for _, index := range []int{-1, MaxHistoryChunkRecords} {
		if _, ok := chunk.At(index); ok {
			t.Errorf("HistoryChunk.At(%d) reported an element", index)
		}
	}

	tooMany := append(messages, newHistoryTestMessage(t, ""))
	_, err = NewHistoryChunk(tooMany)
	requireValidationError(
		t,
		err,
		SizeExceeded,
		"history_chunk_records",
		MaxHistoryChunkRecords,
	)
}

func TestHistoryChunkExactByteLimit(t *testing.T) {
	t.Parallel()

	messages := exactHistoryChunkMessages(t)
	chunk, err := NewHistoryChunk(messages)
	if err != nil {
		t.Fatalf("NewHistoryChunk(exact byte limit): %v", err)
	}
	if got := chunk.ByteSize(); got != MaxHistoryChunkBytes {
		t.Fatalf("HistoryChunk.ByteSize() = %d, want %d", got, MaxHistoryChunkBytes)
	}

	last := len(messages) - 1
	messages[last] = newHistoryTestMessage(t, messages[last].Text()+"x")
	_, err = NewHistoryChunk(messages)
	requireValidationError(
		t,
		err,
		SizeExceeded,
		"history_chunk",
		MaxHistoryChunkBytes,
	)
}

func TestHistoryChunkRejectsInvalidMessage(t *testing.T) {
	t.Parallel()

	_, err := NewHistoryChunk([]Message{{}})
	requireValidationError(t, err, Required, "chat_id", 0)
}

func TestHistoryChunkAccountingOverflow(t *testing.T) {
	t.Parallel()

	exact, err := historyChunkByteSize([]int{MaxHistoryChunkBytes - historyChunkRecordBytes})
	if err != nil {
		t.Fatalf("historyChunkByteSize(exact limit): %v", err)
	}
	if exact != MaxHistoryChunkBytes {
		t.Fatalf("historyChunkByteSize(exact) = %d, want %d", exact, MaxHistoryChunkBytes)
	}

	const maxInt = int(^uint(0) >> 1)
	_, err = historyChunkByteSize([]int{maxInt})
	requireValidationError(
		t,
		err,
		SizeExceeded,
		"history_chunk",
		MaxHistoryChunkBytes,
	)
	_, err = historyChunkByteSize([]int{-1})
	requireValidationError(
		t,
		err,
		SizeExceeded,
		"history_chunk",
		MaxHistoryChunkBytes,
	)
}

func TestHistoryChunkDefensiveCopy(t *testing.T) {
	t.Parallel()

	first := newHistoryTestMessage(t, "first")
	second := newHistoryTestMessage(t, "second")
	messages := []Message{first}
	chunk, err := NewHistoryChunk(messages)
	if err != nil {
		t.Fatalf("NewHistoryChunk: %v", err)
	}
	messages[0] = second

	got, ok := chunk.At(0)
	if !ok {
		t.Fatal("HistoryChunk.At(0) reported out of bounds")
	}
	if got.Text() != "first" {
		t.Fatalf("HistoryChunk.At(0).Text() = %q, want %q", got.Text(), "first")
	}
	got.text = "changed returned copy"
	again, ok := chunk.At(0)
	if !ok || again.Text() != "first" {
		t.Fatalf("mutating returned copy changed chunk: got (%q, %t)", again.Text(), ok)
	}
}

func TestSourceStatusAccessors(t *testing.T) {
	t.Parallel()

	status := NewSourceStatus(SourceStatusInput{
		MetadataDropped: 1,
		OnDemandDropped: 2,
		BulkDropped:     3,
		UnknownDropped:  4,
		Degraded:        true,
	})
	if got := status.MetadataDropped(); got != 1 {
		t.Errorf("MetadataDropped() = %d, want 1", got)
	}
	if got := status.OnDemandDropped(); got != 2 {
		t.Errorf("OnDemandDropped() = %d, want 2", got)
	}
	if got := status.BulkDropped(); got != 3 {
		t.Errorf("BulkDropped() = %d, want 3", got)
	}
	if got := status.UnknownDropped(); got != 4 {
		t.Errorf("UnknownDropped() = %d, want 4", got)
	}
	if !status.Degraded() {
		t.Error("Degraded() = false, want true")
	}

	zero := NewSourceStatus(SourceStatusInput{})
	if zero.MetadataDropped() != 0 || zero.OnDemandDropped() != 0 ||
		zero.BulkDropped() != 0 || zero.UnknownDropped() != 0 || zero.Degraded() {
		t.Fatalf("zero SourceStatus = %#v, want zero counters and false degraded", zero)
	}
}

func TestSourceFailureKindsAreFixedAndNonzero(t *testing.T) {
	t.Parallel()

	kinds := []SourceFailureKind{SourceFull, SourceClosed, SourceRejected}
	seen := make(map[SourceFailureKind]bool, len(kinds))
	for _, kind := range kinds {
		if kind == 0 {
			t.Fatal("SourceFailureKind zero value was assigned as a valid kind")
		}
		if seen[kind] {
			t.Fatalf("duplicate SourceFailureKind value %d", kind)
		}
		seen[kind] = true
	}
}

func TestHistoryValidationErrorDoesNotDiscloseContent(t *testing.T) {
	t.Parallel()

	private := "do-not-disclose\nhistory-a"
	_, err := NewHistoryJob(private, HistoryMetadata)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if strings.Contains(validationErr.Error(), private) ||
		strings.Contains(validationErr.Error(), "do-not-disclose") {
		t.Fatalf("ValidationError disclosed rejected content: %q", validationErr.Error())
	}
}

func historyClassError(class HistoryClass) error {
	_, err := NewHistoryJob("history-a", class)
	return err
}

func newHistoryTestMessage(t *testing.T, text string) Message {
	t.Helper()
	message, err := NewMessage(MessageInput{
		ChatID:    "chat-a",
		MessageID: "message-a",
		SentAt:    time.Unix(1, 0),
		Text:      text,
	})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	return message
}

func exactHistoryChunkMessages(t *testing.T) []Message {
	t.Helper()

	const fullRecords = 7
	const identifierBytes = len("chat-a") + len("message-a")
	fullRecordBytes := identifierBytes + MaxRetainedTextBytes + historyChunkRecordBytes
	finalRecordBytes := MaxHistoryChunkBytes - fullRecords*fullRecordBytes
	finalTextBytes := finalRecordBytes - identifierBytes - historyChunkRecordBytes
	if finalTextBytes < 0 || finalTextBytes > MaxRetainedTextBytes {
		t.Fatalf("test fixture final text size = %d, outside valid range", finalTextBytes)
	}

	messages := make([]Message, 0, fullRecords+1)
	for range fullRecords {
		messages = append(
			messages,
			newHistoryTestMessage(t, strings.Repeat("x", MaxRetainedTextBytes)),
		)
	}
	messages = append(
		messages,
		newHistoryTestMessage(t, strings.Repeat("x", finalTextBytes)),
	)
	return messages
}
