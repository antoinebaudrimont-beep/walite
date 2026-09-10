package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

const sendMailboxCapacity = 1
const sendTimeout = 30 * time.Second
const mediaSendTimeout = 5 * time.Minute

type applicationTextValidator interface {
	ValidateText(service.SendTextRequest) error
}

// One worker owns network calls. One slot covers queued/in-flight work, and
// an unconsumed completion also prevents admission. Channels are never closed
// by producers. The application owns cancellation after admission.
type sendWorker struct {
	ctx         context.Context
	cancel      context.CancelFunc
	application applicationService
	requests    chan tui.SendRequest
	results     chan tui.SendResult
	done        chan struct{}
	mu          sync.Mutex
	busy        bool
}

func newSendWorker(parent context.Context, application applicationService) *sendWorker {
	ctx, cancel := context.WithCancel(parent)
	worker := &sendWorker{ctx: ctx, cancel: cancel, application: application,
		requests: make(chan tui.SendRequest, sendMailboxCapacity), results: make(chan tui.SendResult, sendMailboxCapacity), done: make(chan struct{})}
	go worker.run()
	return worker
}

func (worker *sendWorker) admit(ctx context.Context, request tui.SendRequest) error {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || worker.ctx.Err() != nil {
		return &service.CoreError{Kind: service.CoreCancelled}
	}
	if worker.busy || len(worker.results) != 0 {
		return &service.CoreError{Kind: service.CoreBusy}
	}
	if request.FilePath != "" {
		if request.Text != "" || request.ReplyToID != "" {
			return &service.CoreError{Kind: service.CoreMalformed}
		}
		if _, ok := worker.application.(applicationMediaSender); !ok {
			return wa.ErrMediaUnavailable
		}
		worker.busy = true
		worker.requests <- tui.SendRequest{ChatID: strings.Clone(request.ChatID), FilePath: strings.Clone(request.FilePath)}
		return nil
	}
	if _, ok := worker.application.(applicationTextSender); !ok {
		return wa.ErrTextUnavailable
	}
	validated, err := sendRequestFromTUI(request)
	if err != nil {
		return err
	}
	if validator, ok := worker.application.(applicationTextValidator); ok {
		if err := validator.ValidateText(validated); err != nil {
			if errors.Is(err, wa.ErrGroupReplyUnavailable) {
				return tui.ErrGroupReplyUnavailable
			}
			return err
		}
	}
	worker.busy = true
	quote := validated.Reply()
	worker.requests <- tui.SendRequest{ChatID: validated.ChatID().String(), Text: validated.Text(), ReplyToID: quote.MessageID().String(), ReplyToText: quote.Text(), ReplyToFromMe: quote.FromMe(),
		ReplyMediaKind: quote.Media().Kind().String(), ReplyMediaName: quote.Media().Name(), ReplyMediaMIME: quote.Media().MIMEType()}
	return nil
}

func (worker *sendWorker) run() {
	defer close(worker.done)
	defer close(worker.results)
	for {
		select {
		case <-worker.ctx.Done():
			return
		case request := <-worker.requests:
			timeout := sendTimeout
			if request.FilePath != "" {
				timeout = mediaSendTimeout
			}
			ctx, cancel := context.WithTimeout(worker.ctx, timeout)
			err := ctx.Err()
			if err == nil {
				if request.FilePath != "" {
					err = sendMediaFromTUI(ctx, worker.application, request)
				} else {
					err = sendTextFromTUI(ctx, worker.application, request)
				}
			}
			cancel()
			// Transport uncertainty must survive CoreCancelled wrapping: Core
			// may expose only ctx.Err when cancellation races an upstream error.
			uncertain := errors.Is(err, wa.ErrTextUncertain) || errors.Is(err, wa.ErrMediaSendUncertain) || errors.Is(err, &service.CoreError{Kind: service.CoreCancelled}) ||
				errors.Is(err, &service.CoreError{Kind: service.CoreInvariant}) || errors.Is(err, &service.CoreError{Kind: service.CoreMalformed})
			worker.mu.Lock()
			worker.results <- tui.SendResult{Failed: err != nil, Uncertain: err != nil && uncertain, Media: request.FilePath != "",
				StickerRejected: errors.Is(err, service.ErrStickerRejected)}
			worker.busy = false
			worker.mu.Unlock()
		}
	}
}

func (worker *sendWorker) stop() { worker.cancel(); <-worker.done }
