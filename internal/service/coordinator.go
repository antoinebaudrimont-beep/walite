package service

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const realtimeFairnessLimit = 32

type realtimeCoordinator struct {
	source        EventSource
	store         MessageStore
	policy        RetentionPolicy
	clock         Clock
	realtimeQ     *boundedQueue[model.Event]
	liveWriteQ    *boundedQueue[model.Message]
	resultQ       *boundedQueue[writeResult]
	updates       *fixedMailbox[viewSlot, model.Update]
	liveWriteBusy time.Duration
	localDrops    *saturatingCounter
	localDropWake <-chan struct{}
	shutdown      <-chan struct{}
	finalLimit    int
}

type saturatingCounter struct{ value atomic.Uint64 }

func (counter *saturatingCounter) add(value uint64) {
	for {
		current := counter.value.Load()
		next := current + value
		if math.MaxUint64-current < value {
			next = math.MaxUint64
		}
		if counter.value.CompareAndSwap(current, next) {
			return
		}
	}
}
func (counter *saturatingCounter) load() uint64 { return counter.value.Load() }

func (coordinator *realtimeCoordinator) Run(ctx context.Context) error {
	defer coordinator.liveWriteQ.Close()
	realtimeOpen, resultsOpen, statusOpen := true, true, true
	shuttingDown := false
	shutdownWake := coordinator.shutdown
	for realtimeOpen || resultsOpen {
		if err := ctx.Err(); err != nil {
			return err
		}
		coordinator.handleStatus(&statusOpen)
		select {
		case <-coordinator.localDropWake:
			coordinator.publishDegraded()
		default:
		}
		coordinator.handleResults(&resultsOpen)
		if !shuttingDown && shutdownWake != nil {
			select {
			case <-shutdownWake:
				shuttingDown = true
				shutdownWake = nil
			default:
			}
		}
		if shuttingDown && realtimeOpen {
			limit := coordinator.finalLimit
			if limit > model.MaxWriteBatchMessages {
				limit = model.MaxWriteBatchMessages
			}
			for handled := 0; handled < limit; handled++ {
				owned, ok := coordinator.realtimeQ.TryTake()
				if !ok {
					break
				}
				if err := coordinator.processRealtime(ctx, owned); err != nil {
					return err
				}
			}
			dropped := coordinator.realtimeQ.drainAndRelease()
			if dropped > 0 {
				coordinator.localDrops.add(uint64(dropped))
				coordinator.publishDegraded()
			}
			realtimeOpen = false
			coordinator.liveWriteQ.Close()
		}
		for handled := 0; handled < realtimeFairnessLimit && realtimeOpen; handled++ {
			owned, ok := coordinator.realtimeQ.TryTake()
			if !ok {
				break
			}
			if err := coordinator.processRealtime(ctx, owned); err != nil {
				return err
			}
		}
		if realtimeOpen && queueEnded(coordinator.realtimeQ) {
			realtimeOpen = false
			coordinator.liveWriteQ.Close()
		}
		if resultsOpen && queueEnded(coordinator.resultQ) {
			resultsOpen = false
		}
		if !realtimeOpen && !resultsOpen {
			return nil
		}
		var realtimeWake, resultWake, statusWake <-chan struct{}
		if realtimeOpen {
			realtimeWake = coordinator.realtimeQ.notEmptyChanges()
		}
		if resultsOpen {
			resultWake = coordinator.resultQ.notEmptyChanges()
		}
		if statusOpen {
			statusWake = coordinator.source.StatusWake()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-realtimeWake:
		case <-resultWake:
		case _, ok := <-statusWake:
			if !ok {
				statusOpen = false
			}
		case <-coordinator.localDropWake:
			coordinator.publishDegraded()
		case <-shutdownWake:
			shuttingDown = true
			shutdownWake = nil
		}
	}
	return nil
}

func (coordinator *realtimeCoordinator) processRealtime(ctx context.Context, owned lease[model.Event]) error {
	defer owned.Release()
	event := owned.Value()
	normalized, err := model.NewEvent(event.Message(), event.ReceivedAt())
	if err != nil {
		return errQueueInvariant
	}
	message := normalized.Message()
	snapshot, err := coordinator.store.RetentionSnapshot(ctx, message.ChatID())
	if err != nil {
		return err
	}
	usage, err := coordinator.store.Usage(ctx)
	if err != nil {
		return err
	}
	decision, err := coordinator.policy.Decide(message, model.RetentionState{Now: coordinator.clock.Now(), NewerBodies: countNewerBodies(snapshot, message), Usage: usage, Origin: model.WriteRealtime})
	if err != nil {
		return err
	}
	downstream := message
	switch decision.Action() {
	case model.KeepBody:
	case model.KeepMetadata:
		downstream = message.WithoutBody()
	case model.Discard:
		downstream = model.Message{}
	default:
		return errQueueInvariant
	}
	if decision.Action() != model.Discard {
		if err := coordinator.admitLive(ctx, downstream); err != nil {
			return err
		}
	}
	update, err := model.NewUpdate(model.UpdateInput{Kind: model.UpdateLive, ChatID: message.ChatID(), MessageID: message.MessageID(), Accepted: 1, Degraded: coordinator.localDrops.load() > 0})
	if err != nil {
		return errQueueInvariant
	}
	slot, _ := updateSlot(model.UpdateLive)
	return coordinator.updates.Replace(slot, update)
}

func (coordinator *realtimeCoordinator) admitLive(ctx context.Context, message model.Message) error {
	if err := coordinator.liveWriteQ.TryPut(message); err == nil {
		return nil
	} else if err != errQueueFull {
		return err
	}
	timer := coordinator.clock.NewTimer(coordinator.liveWriteBusy)
	if timer == nil {
		return errQueueInvariant
	}
	defer timer.Stop()
	statusWake := coordinator.source.StatusWake()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-coordinator.liveWriteQ.spaceChanges():
			if err := coordinator.liveWriteQ.TryPut(message); err == nil {
				return nil
			} else if err != errQueueFull {
				return err
			}
		case <-coordinator.liveWriteQ.budgetChanges():
			if err := coordinator.liveWriteQ.TryPut(message); err == nil {
				return nil
			} else if err != errQueueFull {
				return err
			}
		case <-timer.C():
			coordinator.localDrops.add(1)
			coordinator.publishDegraded()
			return nil
		case <-coordinator.resultQ.notEmptyChanges():
			open := true
			coordinator.handleResults(&open)
		case <-coordinator.localDropWake:
			coordinator.publishDegraded()
		case _, ok := <-statusWake:
			if ok {
				coordinator.publishDegraded()
			} else {
				statusWake = nil
			}
		}
	}
}

func (coordinator *realtimeCoordinator) handleStatus(open *bool) {
	if !*open {
		return
	}
	for {
		select {
		case _, ok := <-coordinator.source.StatusWake():
			if !ok {
				*open = false
				return
			}
			coordinator.publishDegraded()
		default:
			return
		}
	}
}
func (coordinator *realtimeCoordinator) handleResults(open *bool) {
	if !*open {
		return
	}
	for {
		owned, ok := coordinator.resultQ.TryTake()
		if !ok {
			if queueEnded(coordinator.resultQ) {
				*open = false
			}
			return
		}
		result := owned.Value()
		kind := model.UpdateSummary
		if result.Origin == model.WriteHistory {
			kind = model.UpdateHistory
		}
		update, err := model.NewUpdate(model.UpdateInput{Kind: kind, Accepted: uint64(result.Written), Discarded: uint64(result.Discarded), Degraded: result.StoreFailed})
		if err == nil {
			slot, _ := updateSlot(kind)
			_ = coordinator.updates.Replace(slot, update)
		}
		_ = owned.Release()
	}
}
func (coordinator *realtimeCoordinator) publishDegraded() {
	status := coordinator.source.Status()
	update, err := model.NewUpdate(model.UpdateInput{Kind: model.UpdateDegraded, Discarded: saturatingSum(status.MetadataDropped(), status.OnDemandDropped(), status.BulkDropped(), status.UnknownDropped(), coordinator.localDrops.load()), Degraded: true})
	if err == nil {
		slot, _ := updateSlot(model.UpdateDegraded)
		_ = coordinator.updates.Replace(slot, update)
	}
}
func saturatingSum(values ...uint64) uint64 {
	var total uint64
	for _, value := range values {
		if math.MaxUint64-total < value {
			return math.MaxUint64
		}
		total += value
	}
	return total
}
