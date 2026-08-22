package service

import (
	"context"
	"errors"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const (
	maxHistoryBatchOperations = 25
	writeResultWeight         = int64(64)
)

type writeResult struct {
	Origin      model.WriteOrigin
	Attempted   int
	Written     int
	Discarded   int
	StoreFailed bool
}

func weighWriteResult(writeResult) (int64, error) { return writeResultWeight, nil }

type liveFirstWriter struct {
	store                  MessageStore
	liveQ                  *boundedQueue[model.Message]
	historyQ               *boundedQueue[model.Message]
	resultQ                *boundedQueue[writeResult]
	clock                  Clock
	batchWait              time.Duration
	batchMaxOperations     int
	policy                 RetentionPolicy
	retentionSnapshotLimit int
	historyStoreCtx        context.Context
	shutdown               <-chan struct{}
	finalLiveLimit         int
	afterHistorySelected   func()
}

func newLiveFirstWriter(
	store MessageStore,
	liveQ *boundedQueue[model.Message],
	historyQ *boundedQueue[model.Message],
	resultQ *boundedQueue[writeResult],
	clock Clock,
	batchWait time.Duration,
	batchMaxOperations int,
	retention ...writerRetention,
) (*liveFirstWriter, error) {
	if store == nil || liveQ == nil || historyQ == nil || resultQ == nil || clock == nil ||
		batchWait <= 0 || batchMaxOperations <= 0 || batchMaxOperations > model.MaxWriteBatchMessages {
		return nil, errQueueInvariant
	}
	writer := &liveFirstWriter{
		store: store, liveQ: liveQ, historyQ: historyQ, resultQ: resultQ,
		clock: clock, batchWait: batchWait, batchMaxOperations: batchMaxOperations,
	}
	if len(retention) > 1 || (len(retention) == 1 && (retention[0].policy == nil || retention[0].snapshotLimit <= 0 || retention[0].snapshotLimit > model.MaxRetentionSnapshotSummaries)) {
		return nil, errQueueInvariant
	}
	if len(retention) == 1 {
		writer.policy, writer.retentionSnapshotLimit = retention[0].policy, retention[0].snapshotLimit
		writer.historyStoreCtx = retention[0].historyStoreCtx
		writer.shutdown = retention[0].shutdown
		writer.finalLiveLimit = retention[0].finalLiveLimit
	}
	return writer, nil
}

type writerRetention struct {
	policy          RetentionPolicy
	snapshotLimit   int
	historyStoreCtx context.Context
	shutdown        <-chan struct{}
	finalLiveLimit  int
}

func (writer *liveFirstWriter) Run(ctx context.Context) error {
	if ctx == nil {
		return errQueueInvariant
	}
	historyCtx := writer.historyStoreCtx
	if historyCtx == nil {
		historyCtx = ctx
	}
	shutdownWake := writer.shutdown
	var pendingHistory lease[model.Message]
	hasPendingHistory := false
	defer func() {
		if hasPendingHistory {
			_ = pendingHistory.Release()
		}
		writer.liveQ.drainAndRelease()
		writer.historyQ.drainAndRelease()
		writer.resultQ.Close()
	}()

	liveOpen, historyOpen := true, true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-shutdownWake:
			return writer.finishShutdown(ctx, &pendingHistory, &hasPendingHistory)
		default:
		}
		if historyCtx.Err() != nil {
			if hasPendingHistory {
				_ = pendingHistory.Release()
				hasPendingHistory = false
			}
			writer.historyQ.drainAndRelease()
		}
		if liveOpen {
			if first, ok := writer.liveQ.TryTake(); ok {
				if err := writer.writeLive(ctx, first); err != nil {
					return err
				}
				continue
			}
		}
		if hasPendingHistory && historyCtx.Err() == nil {
			if liveOpen {
				if first, ok := writer.liveQ.TryTake(); ok {
					if err := writer.writeLive(ctx, first); err != nil {
						return err
					}
					continue
				}
			}
			first := pendingHistory
			hasPendingHistory = false
			if err := writer.writeHistory(historyCtx, first); err != nil {
				if errors.Is(err, context.Canceled) && ctx.Err() == nil {
					writer.historyQ.drainAndRelease()
					continue
				}
				return err
			}
			continue
		}

		if liveOpen && queueEnded(writer.liveQ) {
			liveOpen = false
		}
		if historyOpen && queueEnded(writer.historyQ) {
			historyOpen = false
		}
		if !liveOpen && !historyOpen {
			return nil
		}

		var liveWake, historyWake <-chan struct{}
		var liveStopped, historyStopped <-chan struct{}
		if liveOpen {
			liveWake = writer.liveQ.notEmptyChanges()
			liveStopped = writer.liveQ.stoppedCh
		}
		if historyOpen {
			historyWake = writer.historyQ.notEmptyChanges()
			historyStopped = writer.historyQ.stoppedCh
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-shutdownWake:
			return writer.finishShutdown(ctx, &pendingHistory, &hasPendingHistory)
		case <-liveWake:
		case <-liveStopped:
		case <-historyWake:
			if historyCtx.Err() != nil {
				writer.historyQ.drainAndRelease()
				continue
			}
			if first, ok := writer.historyQ.TryTake(); ok {
				pendingHistory = first
				hasPendingHistory = true
				if writer.afterHistorySelected != nil {
					writer.afterHistorySelected()
				}
			}
		case <-historyStopped:
			if historyCtx.Err() != nil {
				writer.historyQ.drainAndRelease()
				continue
			}
			if first, ok := writer.historyQ.TryTake(); ok {
				pendingHistory = first
				hasPendingHistory = true
				if writer.afterHistorySelected != nil {
					writer.afterHistorySelected()
				}
			}
		}
	}
}

func (writer *liveFirstWriter) finishShutdown(ctx context.Context, pending *lease[model.Message], hasPending *bool) error {
	if *hasPending {
		_ = pending.Release()
		*hasPending = false
	}
	writer.historyQ.drainAndRelease()
	for !writer.liveQ.Stats().Stopped {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-writer.liveQ.notEmptyChanges():
		case <-writer.liveQ.stoppedCh:
		}
	}
	limit := writer.finalLiveLimit
	if limit <= 0 || limit > model.MaxWriteBatchMessages {
		limit = model.MaxWriteBatchMessages
	}
	var leases [model.MaxWriteBatchMessages]lease[model.Message]
	count := 0
	for count < limit {
		owned, ok := writer.liveQ.TryTake()
		if !ok {
			break
		}
		leases[count] = owned
		count++
	}
	var err error
	if count > 0 {
		err = writer.apply(ctx, model.WriteRealtime, leases[:count])
	}
	writer.liveQ.drainAndRelease()
	return err
}

func queueEnded[T any](queue *boundedQueue[T]) bool {
	stats := queue.Stats()
	return stats.Stopped && stats.Entries == 0
}

func (writer *liveFirstWriter) writeLive(ctx context.Context, first lease[model.Message]) error {
	var leases [model.MaxWriteBatchMessages]lease[model.Message]
	leases[0] = first
	count := 1
	if writer.batchMaxOperations > 1 {
		timer := writer.clock.NewTimer(writer.batchWait)
		if timer == nil {
			_ = first.Release()
			return errQueueInvariant
		}
		for count < writer.batchMaxOperations {
			if next, ok := writer.liveQ.TryTake(); ok {
				leases[count] = next
				count++
				continue
			}
			if queueEnded(writer.liveQ) {
				break
			}
			select {
			case <-ctx.Done():
				timer.Stop()
				releaseMessageLeases(leases[:count])
				return ctx.Err()
			case <-timer.C():
				return writer.apply(ctx, model.WriteRealtime, leases[:count])
			case <-writer.liveQ.notEmptyChanges():
			case <-writer.liveQ.stoppedCh:
			}
		}
		timer.Stop()
	}
	return writer.apply(ctx, model.WriteRealtime, leases[:count])
}

func (writer *liveFirstWriter) writeHistory(ctx context.Context, first lease[model.Message]) error {
	var leases [maxHistoryBatchOperations]lease[model.Message]
	leases[0] = first
	count := 1
	limit := writer.batchMaxOperations
	if limit > maxHistoryBatchOperations {
		limit = maxHistoryBatchOperations
	}
	for count < limit {
		next, ok := writer.historyQ.TryTake()
		if !ok {
			break
		}
		leases[count] = next
		count++
	}
	return writer.apply(ctx, model.WriteHistory, leases[:count])
}

func (writer *liveFirstWriter) apply(ctx context.Context, origin model.WriteOrigin, leases []lease[model.Message]) error {
	defer releaseMessageLeases(leases)
	if err := ctx.Err(); err != nil {
		return err
	}
	var messages [model.MaxWriteBatchMessages]model.Message
	for index := range leases {
		messages[index] = leases[index].Value()
	}
	batch, err := model.NewWriteBatch(origin, messages[:len(leases)])
	if err != nil {
		return err
	}
	written, discarded := len(leases), 0
	if writer.policy != nil {
		written, discarded, err = writer.applyBoundedBatch(ctx, batch)
	} else {
		err = writer.store.Write(ctx, batch)
	}
	result := writeResult{Origin: origin, Attempted: len(leases), Written: written, Discarded: discarded}
	if err != nil {
		if origin == model.WriteHistory && errors.Is(err, context.Canceled) {
			return err
		}
		result.Written = 0
		result.StoreFailed = true
		_ = writer.resultQ.TryPut(result)
		return err
	}
	if publishErr := writer.resultQ.Put(ctx, result); publishErr != nil {
		if errors.Is(publishErr, errQueueStopped) && ctx.Err() != nil {
			return ctx.Err()
		}
		return publishErr
	}
	return nil
}

func (writer *liveFirstWriter) applyBoundedBatch(ctx context.Context, batch model.WriteBatch) (int, int, error) {
	var retained [model.MaxWriteBatchMessages]model.Message
	var chats [model.MaxWriteBatchMessages]model.ChatID
	retainedCount, chatCount, discarded := 0, 0, 0
	for index := 0; index < batch.Len(); index++ {
		if err := ctx.Err(); err != nil {
			return 0, discarded, err
		}
		message, ok := batch.At(index)
		if !ok {
			return 0, discarded, errQueueInvariant
		}
		snapshot, err := writer.store.RetentionSnapshot(ctx, message.ChatID())
		if err != nil {
			return 0, discarded, err
		}
		if snapshot.Len() > writer.retentionSnapshotLimit {
			return 0, discarded, errQueueInvariant
		}
		usage, err := writer.store.Usage(ctx)
		if err != nil {
			return 0, discarded, err
		}
		decision, err := writer.policy.Decide(message, model.RetentionState{Now: writer.clock.Now(), NewerBodies: countNewerBodies(snapshot, message), Usage: usage, Origin: batch.Origin()})
		if err != nil {
			return 0, discarded, err
		}
		switch decision.Action() {
		case model.KeepBody:
			retained[retainedCount] = message
			retainedCount++
		case model.KeepMetadata:
			retained[retainedCount] = message.WithoutBody()
			retainedCount++
		case model.Discard:
			discarded++
		default:
			return 0, discarded, errQueueInvariant
		}
		seen := false
		for chatIndex := 0; chatIndex < chatCount; chatIndex++ {
			if chats[chatIndex].String() == message.ChatID().String() {
				seen = true
				break
			}
		}
		if !seen {
			chats[chatCount] = message.ChatID()
			chatCount++
		}
	}
	if retainedCount > 0 {
		filtered, err := model.NewWriteBatch(batch.Origin(), retained[:retainedCount])
		if err != nil {
			return 0, discarded, err
		}
		if err := writer.store.Write(ctx, filtered); err != nil {
			return 0, discarded, err
		}
	}
	for index := 0; index < chatCount; index++ {
		if err := ctx.Err(); err != nil {
			return retainedCount, discarded, err
		}
		snapshot, err := writer.store.RetentionSnapshot(ctx, chats[index])
		if err != nil {
			return retainedCount, discarded, err
		}
		if snapshot.Len() > writer.retentionSnapshotLimit {
			return retainedCount, discarded, errQueueInvariant
		}
		usage, err := writer.store.Usage(ctx)
		if err != nil {
			return retainedCount, discarded, err
		}
		plan, err := writer.policy.PlanPrune(snapshot, model.RetentionState{Now: writer.clock.Now(), Usage: usage, Origin: batch.Origin()})
		if err != nil {
			return retainedCount, discarded, err
		}
		if _, err := writer.store.ApplyPrune(ctx, plan); err != nil {
			return retainedCount, discarded, err
		}
	}
	return retainedCount, discarded, nil
}

func releaseMessageLeases(leases []lease[model.Message]) {
	for index := range leases {
		_ = leases[index].Release()
	}
}
