package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

const (
	readReceiptQueueCapacity = 32
	readReceiptTimeout       = 15 * time.Second
)

type applicationReadReceipter interface {
	MarkRead(context.Context, model.ReadReceiptRequest) error
}

// readReceiptWorker owns all synchronous transport calls. Its fixed ring
// coalesces pending requests per chat to the newest bounded known frontier.
// Failed calls are not retried and never affect local unread persistence.
type readReceiptWorker struct {
	ctx      context.Context
	cancel   context.CancelFunc
	sender   applicationReadReceipter
	pending  [readReceiptQueueCapacity]model.ReadReceiptRequest
	head     int
	count    int
	wake     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	stopOnce sync.Once
}

func newReadReceiptWorker(parent context.Context, application applicationService) *readReceiptWorker {
	ctx, cancel := context.WithCancel(parent)
	sender, _ := application.(applicationReadReceipter)
	worker := &readReceiptWorker{ctx: ctx, cancel: cancel, sender: sender, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go worker.run()
	return worker
}

func (worker *readReceiptWorker) admit(request tui.ReadReceiptRequest) bool {
	if worker == nil || worker.sender == nil || worker.ctx.Err() != nil {
		return false
	}
	adapted, err := readReceiptRequestFromTUI(request)
	if err != nil {
		return false
	}
	worker.mu.Lock()
	for offset := 0; offset < worker.count; offset++ {
		index := (worker.head + offset) % len(worker.pending)
		if worker.pending[index].ChatID() == adapted.ChatID() {
			merged, mergeErr := mergeReadReceiptRequests(worker.pending[index], adapted)
			if mergeErr != nil {
				worker.mu.Unlock()
				return false
			}
			worker.pending[index] = merged
			worker.mu.Unlock()
			return true
		}
	}
	if worker.count == len(worker.pending) {
		worker.mu.Unlock()
		return false
	}
	index := (worker.head + worker.count) % len(worker.pending)
	worker.pending[index] = adapted
	worker.count++
	worker.mu.Unlock()
	worker.signal()
	return true
}

func readReceiptRequestFromTUI(request tui.ReadReceiptRequest) (model.ReadReceiptRequest, error) {
	if len(request.Messages) == 0 || len(request.Messages) > model.MaxReadReceiptMessages {
		return model.ReadReceiptRequest{}, errors.New("read receipt request rejected")
	}
	inputs := make([]model.ReadReceiptMessageInput, len(request.Messages))
	for index, message := range request.Messages {
		inputs[index] = model.ReadReceiptMessageInput{MessageID: message.MessageID, SentAt: message.SentAt, SenderID: message.SenderID}
	}
	return model.NewReadReceiptRequest(request.ChatID, request.IsGroup, inputs)
}

func mergeReadReceiptRequests(left, right model.ReadReceiptRequest) (model.ReadReceiptRequest, error) {
	if left.ChatID() != right.ChatID() || left.IsGroup() != right.IsGroup() {
		return model.ReadReceiptRequest{}, errors.New("read receipt requests do not match")
	}
	var merged [model.MaxReadReceiptMessages * 2]model.ReadReceiptMessageInput
	count := 0
	appendRequest := func(request model.ReadReceiptRequest) {
		for index := 0; index < request.Len(); index++ {
			message, _ := request.At(index)
			found := -1
			for existing := 0; existing < count; existing++ {
				if merged[existing].MessageID == message.MessageID().String() {
					found = existing
					break
				}
			}
			value := model.ReadReceiptMessageInput{MessageID: message.MessageID().String(), SentAt: message.SentAt(), SenderID: message.SenderID().String()}
			if found >= 0 {
				if value.SentAt.After(merged[found].SentAt) {
					merged[found] = value
				}
				continue
			}
			merged[count] = value
			count++
		}
	}
	appendRequest(left)
	appendRequest(right)
	values := merged[:count]
	sort.Slice(values, func(i, j int) bool {
		if values[i].SentAt.Equal(values[j].SentAt) {
			return values[i].MessageID < values[j].MessageID
		}
		return values[i].SentAt.Before(values[j].SentAt)
	})
	if len(values) > model.MaxReadReceiptMessages {
		values = values[len(values)-model.MaxReadReceiptMessages:]
	}
	return model.NewReadReceiptRequest(left.ChatID().String(), left.IsGroup(), values)
}

func (worker *readReceiptWorker) signal() {
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (worker *readReceiptWorker) take() (model.ReadReceiptRequest, bool) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.count == 0 {
		return model.ReadReceiptRequest{}, false
	}
	request := worker.pending[worker.head]
	worker.pending[worker.head] = model.ReadReceiptRequest{}
	worker.head = (worker.head + 1) % len(worker.pending)
	worker.count--
	return request, true
}

func (worker *readReceiptWorker) run() {
	defer close(worker.done)
	for {
		select {
		case <-worker.ctx.Done():
			return
		case <-worker.wake:
		}
		for {
			if worker.ctx.Err() != nil {
				return
			}
			request, ok := worker.take()
			if !ok {
				break
			}
			ctx, cancel := context.WithTimeout(worker.ctx, readReceiptTimeout)
			_ = worker.sender.MarkRead(ctx, request)
			cancel()
		}
	}
}

func (worker *readReceiptWorker) stopWorker() {
	worker.stopOnce.Do(func() {
		worker.cancel()
		<-worker.done
	})
}

func (worker *readReceiptWorker) stop() { worker.stopWorker() }
