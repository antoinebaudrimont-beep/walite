package service

import (
	"context"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

type historyJobState struct {
	job    model.HistoryJob
	active bool
}

type historyTransformer struct {
	source       EventSource
	historyQ     *boundedQueue[model.HistoryChunk]
	chunkRecords int
	chunkBytes   int64
}

func newHistoryTransformer(source EventSource, historyQ *boundedQueue[model.HistoryChunk], chunkRecords int, chunkBytes int64) (*historyTransformer, error) {
	if source == nil || historyQ == nil || chunkRecords <= 0 || chunkRecords > model.MaxHistoryChunkRecords || chunkBytes <= 0 || chunkBytes > model.MaxHistoryChunkBytes {
		return nil, errQueueInvariant
	}
	return &historyTransformer{source: source, historyQ: historyQ, chunkRecords: chunkRecords, chunkBytes: chunkBytes}, nil
}

func (transformer *historyTransformer) Run(ctx context.Context) error {
	if ctx == nil {
		return errQueueInvariant
	}
	metadataCh := transformer.source.MetadataHistoryJobs()
	onDemandCh := transformer.source.OnDemandHistoryJobs()
	bulkCh := transformer.source.BulkHistoryJobs()
	var metadata [2]historyJobState
	var onDemand [1]historyJobState
	var bulk [1]historyJobState
	defer func() {
		releaseJobStates(transformer.source, metadata[:])
		releaseJobStates(transformer.source, onDemand[:])
		releaseJobStates(transformer.source, bulk[:])
		drainHistoryJobs(transformer.source, metadataCh)
		drainHistoryJobs(transformer.source, onDemandCh)
		drainHistoryJobs(transformer.source, bulkCh)
		transformer.historyQ.Close()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := drainJobChannel(transformer.source, &metadataCh, metadata[:], model.HistoryMetadata); err != nil {
			return err
		}
		if err := drainJobChannel(transformer.source, &onDemandCh, onDemand[:], model.HistoryOnDemand); err != nil {
			return err
		}
		if err := drainJobChannel(transformer.source, &bulkCh, bulk[:], model.HistoryBulk); err != nil {
			return err
		}

		state := firstActive(metadata[:])
		if state == nil {
			state = firstActive(onDemand[:])
		}
		if state == nil {
			state = firstActive(bulk[:])
		}
		if state != nil {
			chunk, done, err := transformer.source.NextHistory(ctx, state.job, transformer.chunkRecords, transformer.chunkBytes)
			if err != nil {
				transformer.source.ReleaseHistory(state.job)
				state.active = false
				return err
			}
			if chunk.Len() == 0 && !done {
				transformer.source.ReleaseHistory(state.job)
				state.active = false
				return errQueueInvariant
			}
			if chunk.Len() > 0 {
				if err := transformer.historyQ.Put(ctx, chunk); err != nil {
					return err
				}
			}
			if done {
				transformer.source.ReleaseHistory(state.job)
				state.active = false
			}
			continue
		}
		if metadataCh == nil && onDemandCh == nil && bulkCh == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job, ok := <-metadataCh:
			if !ok {
				metadataCh = nil
			} else if err := admitHistoryJob(transformer.source, metadata[:], job, model.HistoryMetadata); err != nil {
				return err
			}
		case job, ok := <-onDemandCh:
			if !ok {
				onDemandCh = nil
			} else if err := admitHistoryJob(transformer.source, onDemand[:], job, model.HistoryOnDemand); err != nil {
				return err
			}
		case job, ok := <-bulkCh:
			if !ok {
				bulkCh = nil
			} else if err := admitHistoryJob(transformer.source, bulk[:], job, model.HistoryBulk); err != nil {
				return err
			}
		}
	}
}

func drainJobChannel(source EventSource, channel *<-chan model.HistoryJob, states []historyJobState, expected model.HistoryClass) error {
	for firstFree(states) != nil && *channel != nil {
		select {
		case job, ok := <-*channel:
			if !ok {
				*channel = nil
				return nil
			}
			if err := admitHistoryJob(source, states, job, expected); err != nil {
				return err
			}
		default:
			return nil
		}
	}
	return nil
}

func admitHistoryJob(source EventSource, states []historyJobState, job model.HistoryJob, expected model.HistoryClass) error {
	if job.Class() != expected {
		source.ReleaseHistory(job)
		return errQueueInvariant
	}
	state := firstFree(states)
	if state == nil {
		return errQueueInvariant
	}
	state.job, state.active = job, true
	return nil
}

func firstFree(states []historyJobState) *historyJobState {
	for index := range states {
		if !states[index].active {
			return &states[index]
		}
	}
	return nil
}
func firstActive(states []historyJobState) *historyJobState {
	for index := range states {
		if states[index].active {
			return &states[index]
		}
	}
	return nil
}
func releaseJobStates(source EventSource, states []historyJobState) {
	for index := range states {
		if states[index].active {
			source.ReleaseHistory(states[index].job)
			states[index].active = false
		}
	}
}
func drainHistoryJobs(source EventSource, channel <-chan model.HistoryJob) {
	for channel != nil {
		select {
		case job, ok := <-channel:
			if !ok {
				return
			}
			source.ReleaseHistory(job)
		default:
			return
		}
	}
}

type historyIngester struct {
	store         MessageStore
	policy        RetentionPolicy
	clock         Clock
	historyQ      *boundedQueue[model.HistoryChunk]
	historyWriteQ *boundedQueue[model.Message]
}

func newHistoryIngester(store MessageStore, policy RetentionPolicy, clock Clock, historyQ *boundedQueue[model.HistoryChunk], historyWriteQ *boundedQueue[model.Message]) (*historyIngester, error) {
	if store == nil || policy == nil || clock == nil || historyQ == nil || historyWriteQ == nil {
		return nil, errQueueInvariant
	}
	return &historyIngester{store: store, policy: policy, clock: clock, historyQ: historyQ, historyWriteQ: historyWriteQ}, nil
}

func (ingester *historyIngester) Run(ctx context.Context) error {
	if ctx == nil {
		return errQueueInvariant
	}
	defer func() { ingester.historyQ.drainAndRelease(); ingester.historyWriteQ.Close() }()
	for {
		chunkLease, err := ingester.historyQ.Take(ctx)
		if err != nil {
			if err == errQueueStopped {
				return nil
			}
			return err
		}
		if err := ingester.processHistoryChunk(ctx, chunkLease); err != nil {
			return err
		}
	}
}

func (ingester *historyIngester) processHistoryChunk(ctx context.Context, chunkLease lease[model.HistoryChunk]) error {
	defer chunkLease.Release()
	chunk := chunkLease.Value()
	for index := 0; index < chunk.Len(); index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		message, ok := chunk.At(index)
		if !ok {
			return errQueueInvariant
		}
		normalized, err := normalizeHistoryMessage(message)
		if err != nil {
			return err
		}
		snapshot, err := ingester.store.RetentionSnapshot(ctx, normalized.ChatID())
		if err != nil {
			return err
		}
		usage, err := ingester.store.Usage(ctx)
		if err != nil {
			return err
		}
		state := model.RetentionState{Now: ingester.clock.Now(), NewerBodies: countNewerBodies(snapshot, normalized), Usage: usage, Origin: model.WriteHistory}
		decision, err := ingester.policy.Decide(normalized, state)
		if err != nil {
			return err
		}
		var downstream model.Message
		switch decision.Action() {
		case model.KeepBody:
			downstream = normalized
		case model.KeepMetadata:
			downstream = normalized.WithoutBody()
		case model.Discard:
			continue
		default:
			return errQueueInvariant
		}
		if err := ingester.historyWriteQ.Put(ctx, downstream); err != nil {
			return err
		}
	}
	return nil
}

func normalizeHistoryMessage(message model.Message) (model.Message, error) {
	var messages [1]model.Message
	messages[0] = message
	batch, err := model.NewWriteBatch(model.WriteHistory, messages[:])
	if err != nil {
		return model.Message{}, err
	}
	normalized, ok := batch.At(0)
	if !ok {
		return model.Message{}, errQueueInvariant
	}
	return normalized, nil
}

func countNewerBodies(snapshot model.RetentionSnapshot, candidate model.Message) int {
	count := 0
	for index := 0; index < snapshot.Len(); index++ {
		summary, ok := snapshot.At(index)
		if !ok || !summary.BodyRetained() {
			continue
		}
		if summary.SentAt().After(candidate.SentAt()) ||
			(summary.SentAt().Equal(candidate.SentAt()) && summary.MessageID().String() > candidate.MessageID().String()) {
			count++
		}
	}
	return count
}
