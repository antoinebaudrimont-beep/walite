package main

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

const (
	notificationQueueCapacity  = 32
	notificationCommandTimeout = 5 * time.Second
)

const darwinNotificationScript = `on run argv
display notification (item 2 of argv) with title (item 1 of argv)
end run`

type notificationCommandRunner func(context.Context, string, []string) error

func runNotificationCommand(ctx context.Context, command string, args []string) error {
	return exec.CommandContext(ctx, command, args...).Run()
}

type notifySendBackend struct {
	command string
	run     notificationCommandRunner
}

func (backend notifySendBackend) Notify(ctx context.Context, notification tui.Notification) error {
	if backend.command == "" || backend.run == nil {
		return errCapabilityUnavailable
	}
	return backend.run(ctx, backend.command, []string{"--app-name=walite", "--", notification.Title, notification.Body})
}

type darwinNotificationBackend struct {
	command string
	run     notificationCommandRunner
}

func (backend darwinNotificationBackend) Notify(ctx context.Context, notification tui.Notification) error {
	if backend.command == "" || backend.run == nil {
		return errCapabilityUnavailable
	}
	// The script is constant. Untrusted title/body values are argv data and can
	// never become AppleScript source or command options.
	return backend.run(ctx, backend.command, []string{"-e", darwinNotificationScript, "--", notification.Title, notification.Body})
}

type notificationWorker struct {
	backend  desktopNotifier
	requests chan tui.Notification
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	timeout  time.Duration
	mu       sync.Mutex
	stopped  bool
	stopOnce sync.Once
}

func newNotificationWorker(parent context.Context, backend desktopNotifier) *notificationWorker {
	ctx, cancel := context.WithCancel(parent)
	worker := &notificationWorker{
		backend: backend, requests: make(chan tui.Notification, notificationQueueCapacity),
		ctx: ctx, cancel: cancel, done: make(chan struct{}), timeout: notificationCommandTimeout,
	}
	go worker.run()
	return worker
}

func (worker *notificationWorker) run() {
	defer close(worker.done)
	for {
		if worker.ctx.Err() != nil {
			return
		}
		select {
		case <-worker.ctx.Done():
			return
		case notification := <-worker.requests:
			if worker.ctx.Err() != nil {
				return
			}
			if worker.backend == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(worker.ctx, worker.timeout)
			_ = worker.backend.Notify(ctx, notification)
			cancel()
		}
	}
}

func (worker *notificationWorker) admit(notification tui.Notification) bool {
	if worker == nil || worker.backend == nil {
		return false
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.stopped || worker.ctx.Err() != nil {
		return false
	}
	select {
	case worker.requests <- notification:
		return true
	default:
		return false
	}
}

func (worker *notificationWorker) stop() {
	if worker == nil {
		return
	}
	// Notifications are cosmetic: shutdown rejects new admissions, cancels the
	// in-flight command through worker.ctx, abandons queued jobs, and never
	// closes requests where concurrent producers could panic.
	worker.stopOnce.Do(func() {
		worker.mu.Lock()
		worker.stopped = true
		worker.cancel()
		worker.mu.Unlock()
	})
	<-worker.done
}
