// Package service owns the application contracts consumed by coordination.
package service

import (
	"context"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// EventSource supplies bounded real-time events, category-specific history
// jobs, and fixed-size source status to the application service.
type EventSource interface {
	Run(context.Context) error
	RealtimeEvents() <-chan model.Event
	MetadataHistoryJobs() <-chan model.HistoryJob
	OnDemandHistoryJobs() <-chan model.HistoryJob
	BulkHistoryJobs() <-chan model.HistoryJob
	StatusWake() <-chan struct{}
	Status() model.SourceStatus
	NextHistory(
		context.Context,
		model.HistoryJob,
		int,
		int64,
	) (model.HistoryChunk, bool, error)
	ReleaseHistory(model.HistoryJob)
}

// TextSender owns final outgoing message identity and time. Implementations
// return one normalized event but do not persist or publish it. Once a remote
// transport has irreversibly accepted a message, the implementation must
// return that successful event even if the context was concurrently cancelled;
// an error means no successful transport result exists for Core to admit.
type TextSender interface {
	// At most one same-chat quote is allowed, on the same acceptance path.
	SendText(context.Context, model.ChatID, string, ...model.TextQuote) (model.Event, error)
}

// MediaSender uploads and sends one already-inspected local media file. It
// returns transport-owned identity, time, and remote download metadata, but
// does not persist or publish the event itself.
type MediaSender interface {
	SendMedia(context.Context, model.ChatID, string, model.Media, uint64, StickerMetadata) (model.Event, error)
}

// MessageStore is the persistence contract consumed by coordination.
type MessageStore interface {
	EnsureChat(context.Context, model.Chat) error
	Write(context.Context, model.WriteBatch) error
	WriteRealtime(context.Context, model.WriteBatch) (model.LiveEventBatch, error)
	Page(context.Context, model.ChatID, model.Cursor, int) ([]model.Message, model.Cursor, error)
	RetentionSnapshot(context.Context, model.ChatID) (model.RetentionSnapshot, error)
	ApplyPrune(context.Context, model.PrunePlan) (model.PruneResult, error)
	Usage(context.Context) (model.CacheUsage, error)
}

// RetentionPolicy makes pure retention and pruning decisions.
type RetentionPolicy interface {
	Decide(model.Message, model.RetentionState) (model.RetentionDecision, error)
	PlanPrune(model.RetentionSnapshot, model.RetentionState) (model.PrunePlan, error)
}

// Clock supplies current time and selectable timers to service components.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

// Timer is a selectable, stoppable one-shot timer owned by a Clock.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}
