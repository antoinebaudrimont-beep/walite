// Package wa supplies the bounded, offline event-source fake used by
// Milestone 1A. It contains no protocol or network implementation.
package wa

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const (
	maxHistorySpecRecords       = 1_000_000
	maxLifetimeHistoryIDs       = 256
	maxActiveHistoryJobs        = 4
	maxScriptSteps              = 256
	maxScriptBytes        int64 = 512 * 1024

	scriptStepEnvelopeBytes int64 = 256
	historyRecordIndexBytes int64 = 32
)

type sourceOperation uint8

const (
	operationHistorySpec sourceOperation = iota + 1
	operationScript
	operationRun
	operationRealtimeAdmission
	operationHistoryAdmission
	operationHistoryTraversal
)

// SourceError is a content-free fake-source failure. Its operation and kind
// are closed internal values, and any retained cause is reduced to a standard
// context sentinel.
type SourceError struct {
	operation sourceOperation
	kind      model.SourceFailureKind
	cause     error
}

// Error implements error without rendering an identifier, text, payload, or
// wrapped error string.
func (sourceErr *SourceError) Error() string {
	if sourceErr == nil {
		return "fake source failed"
	}
	return sourceOperationText(sourceErr.operation) + ": " + sourceFailureText(sourceErr.kind)
}

// Unwrap returns a fixed standard-library context cause when one exists.
func (sourceErr *SourceError) Unwrap() error {
	if sourceErr == nil {
		return nil
	}
	return sourceErr.cause
}

// SourceFailureKind exposes the shared, content-free source classification.
func (sourceErr *SourceError) SourceFailureKind() model.SourceFailureKind {
	if sourceErr == nil {
		return 0
	}
	return sourceErr.kind
}

// HistorySpec is immutable bounded state for one lazy synthetic history
// generator. It deliberately contains no generated record collection.
type HistorySpec struct {
	job        model.HistoryJob
	count      int
	chatID     model.ChatID
	start      time.Time
	textPrefix string
}

type scriptStepKind uint8

const (
	scriptRealtime scriptStepKind = iota + 1
	scriptHistory
	scriptBarrier
)

// ScriptStep is one immutable, gate-controlled action in a FakeSource script.
// Its fields remain opaque so construction cannot bypass validation performed
// by NewFakeSource.
type ScriptStep struct {
	kind  scriptStepKind
	event model.Event
	spec  HistorySpec
	gate  <-chan struct{}
}

type runState uint8

const (
	runNotStarted runState = iota
	runActive
	runFinished
)

type sourceStatus struct {
	metadataDropped uint64
	onDemandDropped uint64
	bulkDropped     uint64
	unknownDropped  uint64
	degraded        bool
}

type historyCursor struct {
	active bool
	spec   HistorySpec
	index  int
}

// FakeSource is a single-use, bounded, offline event source. One mutex covers
// admission state, status, tokens, cursors, lifetime IDs, and script reference
// clearing. Sends and producer-owned closes occur under that same mutex, which
// excludes send/close races.
type FakeSource struct {
	mu sync.Mutex

	runState        runState
	admissionClosed bool
	script          []ScriptStep
	scriptByteSize  int64

	realtime chan model.Event
	metadata chan model.HistoryJob
	onDemand chan model.HistoryJob
	bulk     chan model.HistoryJob
	statusCh chan struct{}

	status sourceStatus

	categoryTokens  [3]uint8
	cursors         [maxActiveHistoryJobs]historyCursor
	lifetimeIDs     [maxLifetimeHistoryIDs]string
	lifetimeIDCount int
}

// NewHistorySpec validates and takes bounded ownership of lazy synthetic
// history-generator state.
func NewHistorySpec(
	job model.HistoryJob,
	count int,
	chat model.ChatID,
	start time.Time,
	textPrefix string,
) (HistorySpec, error) {
	ownedJob, err := model.NewHistoryJob(job.ID().String(), job.Class())
	if err != nil {
		return HistorySpec{}, newSourceError(
			operationHistorySpec,
			model.SourceRejected,
			nil,
		)
	}
	ownedChat, err := model.NewChatID(chat.String())
	if err != nil {
		return HistorySpec{}, newSourceError(
			operationHistorySpec,
			model.SourceRejected,
			nil,
		)
	}
	if start.IsZero() {
		return HistorySpec{}, newSourceError(
			operationHistorySpec,
			model.SourceRejected,
			nil,
		)
	}

	if ownedJob.Class() == model.HistoryMetadata {
		if count != 0 || textPrefix != "" {
			return HistorySpec{}, newSourceError(
				operationHistorySpec,
				model.SourceRejected,
				nil,
			)
		}
	} else if count < 0 || count > maxHistorySpecRecords {
		return HistorySpec{}, newSourceError(
			operationHistorySpec,
			model.SourceRejected,
			nil,
		)
	}

	ownedPrefix, _ := model.NormalizeText(textPrefix)
	return HistorySpec{
		job:        ownedJob,
		count:      count,
		chatID:     ownedChat,
		start:      start,
		textPrefix: ownedPrefix,
	}, nil
}

// NewRealtimeStep constructs one scripted nonblocking real-time admission.
func NewRealtimeStep(event model.Event, gate <-chan struct{}) ScriptStep {
	return ScriptStep{kind: scriptRealtime, event: event, gate: gate}
}

// NewHistoryStep constructs one scripted nonblocking history admission.
func NewHistoryStep(spec HistorySpec, gate <-chan struct{}) ScriptStep {
	return ScriptStep{kind: scriptHistory, spec: spec, gate: gate}
}

// NewBarrierStep constructs one script step that only waits for its gate. A
// nil gate makes it an immediate no-op.
func NewBarrierStep(gate <-chan struct{}) ScriptStep {
	return ScriptStep{kind: scriptBarrier, gate: gate}
}

// NewFakeSource validates and defensively copies a finite script. Its
// conservative retained charge is a fixed 256-byte allowance per step plus the
// normalized event bytes or history job/chat/prefix bytes held by that step.
func NewFakeSource(script []ScriptStep) (*FakeSource, error) {
	if len(script) > maxScriptSteps {
		return nil, newSourceError(operationScript, model.SourceRejected, nil)
	}

	owned := make([]ScriptStep, len(script))
	var total int64
	for index, step := range script {
		normalized, charge, err := normalizeScriptStep(step)
		if err != nil {
			return nil, err
		}
		if charge < 0 || total > maxScriptBytes-charge {
			return nil, newSourceError(operationScript, model.SourceRejected, nil)
		}
		total += charge
		if total > maxScriptBytes {
			return nil, newSourceError(operationScript, model.SourceRejected, nil)
		}
		owned[index] = normalized
	}

	return &FakeSource{
		script:         owned,
		scriptByteSize: total,
		realtime:       make(chan model.Event, 1),
		metadata:       make(chan model.HistoryJob, 2),
		onDemand:       make(chan model.HistoryJob, 1),
		bulk:           make(chan model.HistoryJob, 1),
		statusCh:       make(chan struct{}, 1),
	}, nil
}

// Run executes the finite script sequentially. The first call is the sole
// owner and closer of all five output channels; concurrent and later calls are
// rejected.
func (source *FakeSource) Run(ctx context.Context) error {
	if source == nil || ctx == nil {
		return newSourceError(operationRun, model.SourceRejected, nil)
	}

	source.mu.Lock()
	if source.runState != runNotStarted || source.realtime == nil ||
		source.metadata == nil || source.onDemand == nil || source.bulk == nil ||
		source.statusCh == nil {
		source.mu.Unlock()
		return newSourceError(operationRun, model.SourceRejected, nil)
	}
	source.runState = runActive
	source.mu.Unlock()

	defer source.finishRun()

	for index := range source.script {
		step := source.scriptAt(index)
		if err := waitForGate(ctx, step.gate); err != nil {
			return err
		}

		switch step.kind {
		case scriptRealtime:
			_ = source.TryRealtime(step.event)
		case scriptHistory:
			_ = source.TryHistory(step.spec)
		case scriptBarrier:
			// The gate wait is the entire step.
		default:
			return newSourceError(operationScript, model.SourceRejected, nil)
		}
		source.clearScriptStep(index)
	}
	return nil
}

// RealtimeEvents returns the source-owned capacity-one real-time output.
func (source *FakeSource) RealtimeEvents() <-chan model.Event {
	if source == nil {
		return nil
	}
	return source.realtime
}

// MetadataHistoryJobs returns the source-owned capacity-two metadata output.
func (source *FakeSource) MetadataHistoryJobs() <-chan model.HistoryJob {
	if source == nil {
		return nil
	}
	return source.metadata
}

// OnDemandHistoryJobs returns the source-owned capacity-one on-demand output.
func (source *FakeSource) OnDemandHistoryJobs() <-chan model.HistoryJob {
	if source == nil {
		return nil
	}
	return source.onDemand
}

// BulkHistoryJobs returns the source-owned capacity-one bulk output.
func (source *FakeSource) BulkHistoryJobs() <-chan model.HistoryJob {
	if source == nil {
		return nil
	}
	return source.bulk
}

// StatusWake returns a source-owned capacity-one coalescing status hint.
func (source *FakeSource) StatusWake() <-chan struct{} {
	if source == nil {
		return nil
	}
	return source.statusCh
}

// Status returns the complete fixed-size cumulative status snapshot.
func (source *FakeSource) Status() model.SourceStatus {
	if source == nil {
		return model.NewSourceStatus(model.SourceStatusInput{})
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	return model.NewSourceStatus(model.SourceStatusInput{
		MetadataDropped: source.status.metadataDropped,
		OnDemandDropped: source.status.onDemandDropped,
		BulkDropped:     source.status.bulkDropped,
		UnknownDropped:  source.status.unknownDropped,
		Degraded:        source.status.degraded,
	})
}

// TryRealtime validates and nonblockingly admits one event. It never retains
// an overflow value or starts a producer goroutine.
func (source *FakeSource) TryRealtime(event model.Event) error {
	if source == nil {
		return newSourceError(operationRealtimeAdmission, model.SourceClosed, nil)
	}

	source.mu.Lock()
	defer source.mu.Unlock()
	if source.admissionClosed {
		return newSourceError(operationRealtimeAdmission, model.SourceClosed, nil)
	}

	normalized, err := model.NewEvent(event.Message(), event.ReceivedAt())
	if err != nil {
		source.markDegradedLocked()
		return newSourceError(operationRealtimeAdmission, model.SourceRejected, nil)
	}
	select {
	case source.realtime <- normalized:
		return nil
	default:
		source.markDegradedLocked()
		return newSourceError(operationRealtimeAdmission, model.SourceFull, nil)
	}
}

// TryHistory validates and nonblockingly admits one lazy job while reserving
// its category token until ReleaseHistory.
func (source *FakeSource) TryHistory(spec HistorySpec) error {
	if source == nil {
		return newSourceError(operationHistoryAdmission, model.SourceClosed, nil)
	}

	source.mu.Lock()
	defer source.mu.Unlock()
	if source.admissionClosed {
		return newSourceError(operationHistoryAdmission, model.SourceClosed, nil)
	}

	owned, err := cloneHistorySpec(spec)
	if err != nil {
		source.markHistoryDropLocked(spec.job.Class())
		return newSourceError(operationHistoryAdmission, model.SourceRejected, nil)
	}
	class := owned.job.Class()
	if class == model.HistoryUnknown {
		source.markHistoryDropLocked(class)
		return newSourceError(operationHistoryAdmission, model.SourceRejected, nil)
	}
	category, capacity, ok := historyCategory(class)
	if !ok {
		source.markDegradedLocked()
		return newSourceError(operationHistoryAdmission, model.SourceRejected, nil)
	}

	identifier := owned.job.ID().String()
	if source.hasLifetimeIDLocked(identifier) {
		source.markHistoryDropLocked(class)
		return newSourceError(operationHistoryAdmission, model.SourceRejected, nil)
	}
	if source.categoryTokens[category] >= capacity {
		source.markHistoryDropLocked(class)
		return newSourceError(operationHistoryAdmission, model.SourceFull, nil)
	}
	if source.lifetimeIDCount >= maxLifetimeHistoryIDs {
		source.markHistoryDropLocked(class)
		return newSourceError(operationHistoryAdmission, model.SourceRejected, nil)
	}

	cursorIndex := source.freeCursorLocked()
	if cursorIndex < 0 {
		source.markHistoryDropLocked(class)
		return newSourceError(operationHistoryAdmission, model.SourceRejected, nil)
	}
	output := source.historyOutputLocked(class)
	select {
	case output <- owned.job:
		source.cursors[cursorIndex] = historyCursor{active: true, spec: owned}
		source.categoryTokens[category]++
		source.lifetimeIDs[source.lifetimeIDCount] = strings.Clone(identifier)
		source.lifetimeIDCount++
		return nil
	default:
		// Token/channel capacities are intentionally identical. Reaching this
		// branch is still handled as a bounded full admission without mutating
		// cursor, token, or lifetime-ID state.
		source.markHistoryDropLocked(class)
		return newSourceError(operationHistoryAdmission, model.SourceFull, nil)
	}
}

// NextHistory lazily generates one bounded, exactly-once chunk for a
// previously admitted job. Traversal remains valid after Run closes outputs.
func (source *FakeSource) NextHistory(
	ctx context.Context,
	job model.HistoryJob,
	maxRecords int,
	maxBytes int64,
) (model.HistoryChunk, bool, error) {
	if source == nil || ctx == nil || maxRecords <= 0 || maxBytes <= 0 {
		return model.HistoryChunk{}, false, newSourceError(
			operationHistoryTraversal,
			model.SourceRejected,
			nil,
		)
	}
	if err := contextFailure(ctx); err != nil {
		return model.HistoryChunk{}, false, err
	}
	if maxRecords > model.MaxHistoryChunkRecords {
		maxRecords = model.MaxHistoryChunkRecords
	}
	if maxBytes > int64(model.MaxHistoryChunkBytes) {
		maxBytes = int64(model.MaxHistoryChunkBytes)
	}

	source.mu.Lock()
	defer source.mu.Unlock()
	if err := contextFailure(ctx); err != nil {
		return model.HistoryChunk{}, false, err
	}
	cursorIndex := source.cursorForJobLocked(job)
	if cursorIndex < 0 {
		return model.HistoryChunk{}, false, newSourceError(
			operationHistoryTraversal,
			model.SourceRejected,
			nil,
		)
	}
	cursor := &source.cursors[cursorIndex]
	if cursor.index >= cursor.spec.count {
		chunk, err := model.NewHistoryChunk(nil)
		if err != nil {
			return model.HistoryChunk{}, false, newSourceError(
				operationHistoryTraversal,
				model.SourceRejected,
				nil,
			)
		}
		return chunk, true, nil
	}

	startIndex := cursor.index
	nextIndex := startIndex
	var records [model.MaxHistoryChunkRecords]model.Message
	recordCount := 0
	var byteSize int64
	for recordCount < maxRecords && nextIndex < cursor.spec.count {
		if err := contextFailure(ctx); err != nil {
			return model.HistoryChunk{}, false, err
		}
		message, err := historyMessage(cursor.spec, nextIndex)
		if err != nil {
			return model.HistoryChunk{}, false, newSourceError(
				operationHistoryTraversal,
				model.SourceRejected,
				nil,
			)
		}
		recordBytes := int64(message.ByteSize()) + historyRecordIndexBytes
		if recordBytes > maxBytes-byteSize {
			if recordCount == 0 {
				return model.HistoryChunk{}, false, newSourceError(
					operationHistoryTraversal,
					model.SourceRejected,
					nil,
				)
			}
			break
		}
		records[recordCount] = message
		recordCount++
		byteSize += recordBytes
		nextIndex++
	}
	if err := contextFailure(ctx); err != nil {
		return model.HistoryChunk{}, false, err
	}

	chunk, err := model.NewHistoryChunk(records[:recordCount])
	if err != nil || int64(chunk.ByteSize()) > maxBytes {
		return model.HistoryChunk{}, false, newSourceError(
			operationHistoryTraversal,
			model.SourceRejected,
			nil,
		)
	}
	cursor.index = nextIndex
	return chunk, nextIndex >= cursor.spec.count, nil
}

// ReleaseHistory idempotently removes one active cursor and returns its
// category token. The job ID remains consumed for this source lifetime.
func (source *FakeSource) ReleaseHistory(job model.HistoryJob) {
	if source == nil {
		return
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	index := source.cursorForJobLocked(job)
	if index < 0 {
		return
	}
	class := source.cursors[index].spec.job.Class()
	source.cursors[index] = historyCursor{}
	category, _, ok := historyCategory(class)
	if ok && source.categoryTokens[category] > 0 {
		source.categoryTokens[category]--
	}
}

func normalizeScriptStep(step ScriptStep) (ScriptStep, int64, error) {
	switch step.kind {
	case scriptRealtime:
		event, err := model.NewEvent(step.event.Message(), step.event.ReceivedAt())
		if err != nil {
			return ScriptStep{}, 0, newSourceError(operationScript, model.SourceRejected, nil)
		}
		return NewRealtimeStep(event, step.gate),
			scriptStepEnvelopeBytes + int64(event.ByteSize()), nil
	case scriptHistory:
		spec, err := cloneHistorySpec(step.spec)
		if err != nil {
			return ScriptStep{}, 0, newSourceError(operationScript, model.SourceRejected, nil)
		}
		charge := scriptStepEnvelopeBytes + int64(len(spec.job.ID().String())) +
			int64(len(spec.chatID.String())) + int64(len(spec.textPrefix))
		return NewHistoryStep(spec, step.gate), charge, nil
	case scriptBarrier:
		return NewBarrierStep(step.gate), scriptStepEnvelopeBytes, nil
	default:
		return ScriptStep{}, 0, newSourceError(operationScript, model.SourceRejected, nil)
	}
}

func cloneHistorySpec(spec HistorySpec) (HistorySpec, error) {
	return NewHistorySpec(
		spec.job,
		spec.count,
		spec.chatID,
		spec.start,
		spec.textPrefix,
	)
}

func (source *FakeSource) scriptAt(index int) ScriptStep {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.script[index]
}

func (source *FakeSource) clearScriptStep(index int) {
	source.mu.Lock()
	source.script[index] = ScriptStep{}
	source.mu.Unlock()
}

func (source *FakeSource) finishRun() {
	source.mu.Lock()
	defer source.mu.Unlock()

	// Publishing closed admission under the send mutex must happen before any
	// output close. A concurrent Try call either sends while this lock is held
	// first, or observes SourceClosed after it acquires the lock.
	source.admissionClosed = true
	for index := range source.script {
		source.script[index] = ScriptStep{}
	}
	close(source.realtime)
	close(source.metadata)
	close(source.onDemand)
	close(source.bulk)
	close(source.statusCh)
	source.runState = runFinished
}

func waitForGate(ctx context.Context, gate <-chan struct{}) error {
	if err := contextFailure(ctx); err != nil {
		return err
	}
	if gate == nil {
		return nil
	}
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return contextFailure(ctx)
	}
}

func contextFailure(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}

func historyMessage(spec HistorySpec, index int) (model.Message, error) {
	messageID := spec.job.ID().String() + "-" + strconv.Itoa(index)
	text := spec.textPrefix + strconv.Itoa(index)
	sentAt := spec.start.Add(time.Duration(index) * time.Second)
	if sentAt.IsZero() {
		// A nonzero start immediately before Go's zero Time can otherwise
		// produce one invalid synthetic record. Keep every generated timestamp
		// nonzero without imposing an undocumented restriction on valid starts.
		sentAt = sentAt.Add(time.Nanosecond)
	}
	return model.NewMessage(model.MessageInput{
		ChatID:    spec.chatID.String(),
		MessageID: messageID,
		SentAt:    sentAt,
		Text:      text,
	})
}

func historyCategory(class model.HistoryClass) (index int, capacity uint8, ok bool) {
	switch class {
	case model.HistoryMetadata:
		return 0, 2, true
	case model.HistoryOnDemand:
		return 1, 1, true
	case model.HistoryBulk:
		return 2, 1, true
	default:
		return 0, 0, false
	}
}

func (source *FakeSource) historyOutputLocked(class model.HistoryClass) chan model.HistoryJob {
	switch class {
	case model.HistoryMetadata:
		return source.metadata
	case model.HistoryOnDemand:
		return source.onDemand
	case model.HistoryBulk:
		return source.bulk
	default:
		return nil
	}
}

func (source *FakeSource) freeCursorLocked() int {
	for index := range source.cursors {
		if !source.cursors[index].active {
			return index
		}
	}
	return -1
}

func (source *FakeSource) cursorForJobLocked(job model.HistoryJob) int {
	identifier := job.ID().String()
	class := job.Class()
	for index := range source.cursors {
		cursor := &source.cursors[index]
		if cursor.active && cursor.spec.job.ID().String() == identifier &&
			cursor.spec.job.Class() == class {
			return index
		}
	}
	return -1
}

func (source *FakeSource) hasLifetimeIDLocked(identifier string) bool {
	for index := 0; index < source.lifetimeIDCount; index++ {
		if source.lifetimeIDs[index] == identifier {
			return true
		}
	}
	return false
}

func (source *FakeSource) markHistoryDropLocked(class model.HistoryClass) {
	switch class {
	case model.HistoryMetadata:
		saturatingIncrement(&source.status.metadataDropped)
	case model.HistoryOnDemand:
		saturatingIncrement(&source.status.onDemandDropped)
	case model.HistoryBulk:
		saturatingIncrement(&source.status.bulkDropped)
	case model.HistoryUnknown:
		saturatingIncrement(&source.status.unknownDropped)
	}
	source.markDegradedLocked()
}

func (source *FakeSource) markDegradedLocked() {
	source.status.degraded = true
	select {
	case source.statusCh <- struct{}{}:
	default:
	}
}

func saturatingIncrement(value *uint64) {
	if *value < math.MaxUint64 {
		*value++
	}
}

func newSourceError(
	operation sourceOperation,
	kind model.SourceFailureKind,
	cause error,
) *SourceError {
	return &SourceError{
		operation: operation,
		kind:      kind,
		cause:     fixedContextCause(cause),
	}
}

func fixedContextCause(cause error) error {
	switch {
	case errors.Is(cause, context.Canceled):
		return context.Canceled
	case errors.Is(cause, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func sourceOperationText(operation sourceOperation) string {
	switch operation {
	case operationHistorySpec:
		return "fake history specification"
	case operationScript:
		return "fake source script"
	case operationRun:
		return "fake source run"
	case operationRealtimeAdmission:
		return "fake realtime admission"
	case operationHistoryAdmission:
		return "fake history admission"
	case operationHistoryTraversal:
		return "fake history traversal"
	default:
		return "fake source operation"
	}
}

func sourceFailureText(kind model.SourceFailureKind) string {
	switch kind {
	case model.SourceFull:
		return "full"
	case model.SourceClosed:
		return "closed"
	case model.SourceRejected:
		return "rejected"
	default:
		return "failed"
	}
}
