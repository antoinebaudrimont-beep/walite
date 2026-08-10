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
