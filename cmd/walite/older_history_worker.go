package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

type olderHistoryApplication interface {
	LoadOlderMessages(context.Context, model.ChatID, model.MessageID, time.Time, bool, int) ([]model.Message, error)
}

// olderHistoryWorker owns one in-flight request and one coalesced pending slot.
// It never starts a goroutine per request.
type olderHistoryWorker struct {
	ctx       context.Context
	cancel    context.CancelFunc
	source    olderHistoryApplication
	reactions reactionSummaryApplication

	mu             sync.Mutex
	active         bool
	activeRequest  tui.OlderHistoryRequest
	pending        bool
	pendingRequest tui.OlderHistoryRequest
	wake           chan struct{}
	results        chan tui.OlderHistoryResult
	done           chan struct{}
	stopOnce       sync.Once
}

func newOlderHistoryWorker(parent context.Context, application applicationService) *olderHistoryWorker {
	ctx, cancel := context.WithCancel(parent)
	source, _ := application.(olderHistoryApplication)
	reactions, _ := application.(reactionSummaryApplication)
	worker := &olderHistoryWorker{
		ctx: ctx, cancel: cancel, source: source, reactions: reactions,
		wake: make(chan struct{}, 1), results: make(chan tui.OlderHistoryResult, 1), done: make(chan struct{}),
	}
	go worker.run()
	return worker
}

func (worker *olderHistoryWorker) admit(request tui.OlderHistoryRequest) bool {
	if worker == nil || request.ChatID == "" || request.OldestMessageID == "" || request.OldestSentAt.IsZero() || request.Count != tui.OlderHistoryPageSize {
		return false
	}
	if _, err := model.NewChatID(request.ChatID); err != nil {
		return false
	}
	if _, err := model.NewMessageID(request.OldestMessageID); err != nil {
		return false
	}
	worker.mu.Lock()
	if worker.active && worker.activeRequest == request || worker.pending && worker.pendingRequest == request {
		worker.mu.Unlock()
		return true
	}
	worker.pendingRequest, worker.pending = request, true
	worker.mu.Unlock()
	worker.signal()
	return true
}

func (worker *olderHistoryWorker) signal() {
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (worker *olderHistoryWorker) take() (tui.OlderHistoryRequest, bool) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if !worker.pending {
		return tui.OlderHistoryRequest{}, false
	}
	request := worker.pendingRequest
	worker.pendingRequest, worker.pending = tui.OlderHistoryRequest{}, false
	worker.activeRequest, worker.active = request, true
	return request, true
}

func (worker *olderHistoryWorker) finish(request tui.OlderHistoryRequest) {
	worker.mu.Lock()
	if worker.active && worker.activeRequest == request {
		worker.activeRequest, worker.active = tui.OlderHistoryRequest{}, false
	}
	worker.mu.Unlock()
}

func (worker *olderHistoryWorker) run() {
	defer close(worker.done)
	defer close(worker.results)
	for {
		select {
		case <-worker.ctx.Done():
			return
		case <-worker.wake:
		}
		for {
			request, ok := worker.take()
			if !ok {
				break
			}
			result, emit := worker.load(request)
			worker.finish(request)
			if emit {
				replaceOlderHistoryResult(worker.results, result)
			}
		}
	}
}

func (worker *olderHistoryWorker) load(request tui.OlderHistoryRequest) (tui.OlderHistoryResult, bool) {
	result := tui.OlderHistoryResult{Request: request}
	if worker.source == nil {
		result.Kind = tui.OlderHistoryUnavailable
		return result, true
	}
	chatID, err := model.NewChatID(request.ChatID)
	if err != nil {
		result.Kind = tui.OlderHistoryFailed
		return result, true
	}
	messageID, err := model.NewMessageID(request.OldestMessageID)
	if err != nil {
		result.Kind = tui.OlderHistoryFailed
		return result, true
	}
	messages, err := worker.source.LoadOlderMessages(worker.ctx, chatID, messageID, request.OldestSentAt, request.OldestFromMe, request.Count)
	if err != nil {
		if errors.Is(err, context.Canceled) && worker.ctx.Err() != nil {
			return result, false
		}
		if errors.Is(err, wa.ErrHistoryRequestUnavailable) {
			result.Kind = tui.OlderHistoryUnavailable
		} else {
			result.Kind = tui.OlderHistoryFailed
		}
		return result, true
	}
	if len(messages) == 0 {
		result.Kind = tui.OlderHistoryNoMore
		return result, true
	}
	if len(messages) > request.Count {
		result.Kind = tui.OlderHistoryFailed
		return result, true
	}
	// SQLite returns nearest-first; the TUI accepts the complete bounded page
	// and merges it into the selected chat's independently bounded history.
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].SentAt().Equal(messages[j].SentAt()) {
			return messages[i].MessageID().String() < messages[j].MessageID().String()
		}
		return messages[i].SentAt().Before(messages[j].SentAt())
	})
	result.Messages = make([]tui.InitialMessage, len(messages))
	for index, message := range messages {
		result.Messages[index] = initialMessageFromModel(message)
		if worker.reactions != nil {
			summary, err := worker.reactions.ReactionSummary(worker.ctx, message.ChatID(), message.MessageID())
			if err != nil {
				result.Kind = tui.OlderHistoryFailed
				result.Messages = nil
				return result, true
			}
			result.Messages[index].Reactions = reactionGroupsFromModel(summary)
		}
	}
	result.Kind = tui.OlderHistoryLoaded
	return result, true
}

func replaceOlderHistoryResult(destination chan tui.OlderHistoryResult, result tui.OlderHistoryResult) {
	select {
	case <-destination:
	default:
	}
	select {
	case destination <- result:
	default:
	}
}

func (worker *olderHistoryWorker) stop() {
	if worker == nil {
		return
	}
	worker.stopOnce.Do(func() {
		worker.cancel()
		<-worker.done
	})
}
