package main

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

const linkHelperTimeout = 20 * time.Second

type linkCommandRunner func(context.Context, string, []string, io.Reader) error

type linkWorker struct {
	ctx       context.Context
	cancel    context.CancelFunc
	opener    systemOpener
	clipboard clipboard
	requests  chan tui.LinkRequest
	results   chan tui.LinkResult
	done      chan struct{}
	mu        sync.Mutex
	busy      bool
}

func newDefaultLinkWorker(parent context.Context) *linkWorker {
	capabilities := defaultPlatformCapabilities()
	return newLinkWorkerWithCapabilities(parent, capabilities.opener, capabilities.clipboard)
}

func newLinkWorker(parent context.Context, open, copyTool string, copyArgs []string, runner linkCommandRunner) *linkWorker {
	var opener systemOpener
	if open != "" {
		opener = commandSystemOpener{command: open, run: runner}
	}
	var clipboardCapability clipboard
	if copyTool != "" {
		clipboardCapability = commandClipboard{command: copyTool, args: copyArgs, run: runner}
	}
	return newLinkWorkerWithCapabilities(parent, opener, clipboardCapability)
}

func newLinkWorkerWithCapabilities(parent context.Context, opener systemOpener, clipboardCapability clipboard) *linkWorker {
	ctx, cancel := context.WithCancel(parent)
	worker := &linkWorker{ctx: ctx, cancel: cancel, opener: opener, clipboard: clipboardCapability,
		requests: make(chan tui.LinkRequest, 1), results: make(chan tui.LinkResult, 1), done: make(chan struct{})}
	go worker.loop()
	return worker
}

func (worker *linkWorker) admit(request tui.LinkRequest) bool {
	if worker == nil || !validLinkRequest(request) {
		return false
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.ctx.Err() != nil || worker.busy || len(worker.results) != 0 {
		return false
	}
	worker.busy = true
	worker.requests <- tui.LinkRequest{Action: request.Action, URL: strings.Clone(request.URL)}
	return true
}

func validLinkRequest(request tui.LinkRequest) bool {
	if len(request.URL) == 0 || len(request.URL) > tui.MaxLinkBytes || (request.Action != tui.LinkOpen && request.Action != tui.LinkCopy) {
		return false
	}
	parsed, err := url.ParseRequestURI(request.URL)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func (worker *linkWorker) loop() {
	defer close(worker.done)
	defer close(worker.results)
	for {
		select {
		case <-worker.ctx.Done():
			return
		case request := <-worker.requests:
			status := worker.execute(request)
			worker.mu.Lock()
			select {
			case worker.results <- tui.LinkResult{Status: status}:
			case <-worker.ctx.Done():
				worker.mu.Unlock()
				return
			}
			worker.busy = false
			worker.mu.Unlock()
		}
	}
}

func (worker *linkWorker) execute(request tui.LinkRequest) string {
	ctx, cancel := context.WithTimeout(worker.ctx, linkHelperTimeout)
	defer cancel()
	switch request.Action {
	case tui.LinkOpen:
		if worker.opener == nil {
			return "Opening links requires xdg-open"
		}
		if err := worker.opener.Open(ctx, request.URL); err != nil {
			return "Could not open link"
		}
		return "Link opened"
	case tui.LinkCopy:
		if worker.clipboard == nil {
			return "Copying links requires xclip or xsel"
		}
		if err := worker.clipboard.Copy(ctx, request.URL); err != nil {
			return "Could not copy link"
		}
		return "Link copied"
	default:
		return "Link action rejected"
	}
}

func runLinkCommand(ctx context.Context, name string, args []string, stdin io.Reader) error {
	if ctx == nil || name == "" {
		return errors.New("link helper rejected")
	}
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = stdin
	return command.Run()
}

func (worker *linkWorker) stop() {
	if worker == nil {
		return
	}
	worker.cancel()
	<-worker.done
}
