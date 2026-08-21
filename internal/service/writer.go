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
	StoreFailed bool
}

func weighWriteResult(writeResult) (int64, error) { return writeResultWeight, nil }

type liveFirstWriter struct {
	store                MessageStore
	liveQ                *boundedQueue[model.Message]
	historyQ             *boundedQueue[model.Message]
	resultQ              *boundedQueue[writeResult]
	clock                Clock
	batchWait            time.Duration
	batchMaxOperations   int
	afterHistorySelected func()
}

func newLiveFirstWriter(
	store MessageStore,
	liveQ *boundedQueue[model.Message],
	historyQ *boundedQueue[model.Message],
	resultQ *boundedQueue[writeResult],
	clock Clock,
	batchWait time.Duration,
	batchMaxOperations int,
) (*liveFirstWriter, error) {
	if store == nil || liveQ == nil || historyQ == nil || resultQ == nil || clock == nil ||
		batchWait <= 0 || batchMaxOperations <= 0 || batchMaxOperations > model.MaxWriteBatchMessages {
		return nil, errQueueInvariant
	}
	return &liveFirstWriter{
		store: store, liveQ: liveQ, historyQ: historyQ, resultQ: resultQ,
		clock: clock, batchWait: batchWait, batchMaxOperations: batchMaxOperations,
	}, nil
}

func (writer *liveFirstWriter) Run(ctx context.Context) error {
	if ctx == nil {
		return errQueueInvariant
	}
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
		if liveOpen {
			if first, ok := writer.liveQ.TryTake(); ok {
				if err := writer.writeLive(ctx, first); err != nil {
					return err
				}
				continue
			}
		}
		if hasPendingHistory {
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
			if err := writer.writeHistory(ctx, first); err != nil {
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
		case <-liveWake:
		case <-liveStopped:
		case <-historyWake:
			if first, ok := writer.historyQ.TryTake(); ok {
				pendingHistory = first
				hasPendingHistory = true
				if writer.afterHistorySelected != nil {
					writer.afterHistorySelected()
				}
			}
		case <-historyStopped:
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
	if err := ctx.Err(); err != nil {
		releaseMessageLeases(leases)
		return err
	}
	var messages [model.MaxWriteBatchMessages]model.Message
	for index := range leases {
		messages[index] = leases[index].Value()
	}
	batch, err := model.NewWriteBatch(origin, messages[:len(leases)])
	if err != nil {
		releaseMessageLeases(leases)
		return err
	}
	err = writer.store.Write(ctx, batch)
	releaseMessageLeases(leases)
	result := writeResult{Origin: origin, Attempted: len(leases), Written: len(leases)}
	if err != nil {
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

func releaseMessageLeases(leases []lease[model.Message]) {
	for index := range leases {
		_ = leases[index].Release()
	}
}
