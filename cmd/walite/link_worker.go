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
	ctx      context.Context
	cancel   context.CancelFunc
	run      linkCommandRunner
	open     string
	copy     string
	copyArgs []string
	requests chan tui.LinkRequest
	results  chan tui.LinkResult
	done     chan struct{}
	mu       sync.Mutex
	busy     bool
}

func newDefaultLinkWorker(parent context.Context) *linkWorker {
	open, _ := exec.LookPath("xdg-open")
	copyTool, copyArgs := "", []string(nil)
	if candidate, err := exec.LookPath("xclip"); err == nil {
		copyTool, copyArgs = candidate, []string{"-selection", "clipboard", "-in"}
	} else if candidate, err := exec.LookPath("xsel"); err == nil {
		copyTool, copyArgs = candidate, []string{"--clipboard", "--input"}
	}
	return newLinkWorker(parent, open, copyTool, copyArgs, runLinkCommand)
}

func newLinkWorker(parent context.Context, open, copyTool string, copyArgs []string, runner linkCommandRunner) *linkWorker {
	ctx, cancel := context.WithCancel(parent)
	worker := &linkWorker{ctx: ctx, cancel: cancel, run: runner, open: open, copy: copyTool,
		copyArgs: append([]string(nil), copyArgs...), requests: make(chan tui.LinkRequest, 1), results: make(chan tui.LinkResult, 1), done: make(chan struct{})}
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
		if worker.open == "" {
			return "Opening links requires xdg-open"
		}
		if err := worker.run(ctx, worker.open, []string{request.URL}, nil); err != nil {
			return "Could not open link"
		}
		return "Link opened"
	case tui.LinkCopy:
		if worker.copy == "" {
			return "Copying links requires xclip or xsel"
		}
		if err := worker.run(ctx, worker.copy, append([]string(nil), worker.copyArgs...), strings.NewReader(request.URL)); err != nil {
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
