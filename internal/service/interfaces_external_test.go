package service_test

import (
	"context"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

var (
	_ service.EventSource = (*contractSource)(nil)
	_ service.EventSource = (*wa.FakeSource)(nil)
	_ service.Clock       = (*contractClock)(nil)
	_ service.Timer       = (*contractTimer)(nil)
)

type contractSource struct{}

func (*contractSource) Run(context.Context) error {
	return nil
}

func (*contractSource) RealtimeEvents() <-chan model.Event {
	return nil
}

func (*contractSource) MetadataHistoryJobs() <-chan model.HistoryJob {
	return nil
}

func (*contractSource) OnDemandHistoryJobs() <-chan model.HistoryJob {
	return nil
}

func (*contractSource) BulkHistoryJobs() <-chan model.HistoryJob {
	return nil
}

func (*contractSource) StatusWake() <-chan struct{} {
	return nil
}

func (*contractSource) Status() model.SourceStatus {
	return model.SourceStatus{}
}

func (*contractSource) NextHistory(
	context.Context,
	model.HistoryJob,
	int,
	int64,
) (model.HistoryChunk, bool, error) {
	return model.HistoryChunk{}, true, nil
}

func (*contractSource) ReleaseHistory(model.HistoryJob) {}

type contractClock struct{}

func (*contractClock) Now() time.Time {
	return time.Time{}
}

func (*contractClock) NewTimer(time.Duration) service.Timer {
	return (*contractTimer)(nil)
}

type contractTimer struct{}

func (*contractTimer) C() <-chan time.Time {
	return nil
}

func (*contractTimer) Stop() bool {
	return false
}
