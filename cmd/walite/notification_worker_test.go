package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type notificationBackendFunc func(context.Context, tui.Notification) error

func (function notificationBackendFunc) Notify(ctx context.Context, notification tui.Notification) error {
	return function(ctx, notification)
}

func TestNotificationBackendsPassUntrustedTextOnlyAsArguments(t *testing.T) {
	notification := tui.Notification{
		ChatID: "private-chat@lid",
		Title:  `--hello ' " \ & 🖕 👨‍👩‍👧‍👦`,
		Body:   "line one\nline two: it's \"quoted\" \\ A&B",
	}
	for _, test := range []struct {
		name    string
		backend desktopNotifier
		want    []string
	}{
		{
			name: "linux",
			backend: notifySendBackend{command: "/usr/bin/notify-send", run: func(_ context.Context, command string, args []string) error {
				if command != "/usr/bin/notify-send" {
					t.Fatalf("command=%q", command)
				}
				return nil
			}},
			want: []string{"--app-name=walite", "--", notification.Title, notification.Body},
		},
		{
			name: "darwin",
			backend: darwinNotificationBackend{command: "/usr/bin/osascript", run: func(_ context.Context, command string, args []string) error {
				if command != "/usr/bin/osascript" {
					t.Fatalf("command=%q", command)
				}
				return nil
			}},
			want: []string{"-e", darwinNotificationScript, "--", notification.Title, notification.Body},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got []string
			switch backend := test.backend.(type) {
			case notifySendBackend:
				original := backend.run
				backend.run = func(ctx context.Context, command string, args []string) error {
					got = append([]string(nil), args...)
					return original(ctx, command, args)
				}
				test.backend = backend
			case darwinNotificationBackend:
				original := backend.run
				backend.run = func(ctx context.Context, command string, args []string) error {
					got = append([]string(nil), args...)
					return original(ctx, command, args)
				}
				test.backend = backend
			}
			if err := test.backend.Notify(context.Background(), notification); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("args=%q want=%q", got, test.want)
			}
			if test.name == "darwin" && (strings.Contains(darwinNotificationScript, notification.Title) || strings.Contains(darwinNotificationScript, notification.Body)) {
				t.Fatal("untrusted notification data entered AppleScript source")
			}
		})
	}
}

func TestNotificationWorkerPreservesChatIdentity(t *testing.T) {
	got := make(chan tui.Notification, 1)
	worker := newNotificationWorker(context.Background(), notificationBackendFunc(func(_ context.Context, notification tui.Notification) error {
		got <- notification
		return nil
	}))
	defer worker.stop()
	want := tui.Notification{ChatID: "family@g.us", Title: "walite", Body: "New WhatsApp message"}
	if !worker.admit(want) {
		t.Fatal("notification rejected")
	}
	if notification := <-got; notification != want {
		t.Fatalf("notification=%+v want=%+v", notification, want)
	}
}

func TestNotificationWorkerIsBoundedNonblockingSingleExecutionAndIdempotent(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	backend := notificationBackendFunc(func(ctx context.Context, _ tui.Notification) error {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	})
	worker := newNotificationWorker(context.Background(), backend)
	worker.timeout = time.Hour
	if !worker.admit(tui.Notification{Title: "first", Body: "first"}) {
		t.Fatal("first notification rejected")
	}
	<-started
	for index := 0; index < notificationQueueCapacity; index++ {
		if !worker.admit(tui.Notification{Title: "queued", Body: "queued"}) {
			t.Fatalf("queue rejected at %d", index)
		}
	}
	if worker.admit(tui.Notification{Title: "overflow", Body: "overflow"}) {
		t.Fatal("bounded queue accepted overflow")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent backend calls=%d want=1", got)
	}
	var stops sync.WaitGroup
	for index := 0; index < 4; index++ {
		stops.Add(1)
		go func() { defer stops.Done(); worker.stop() }()
	}
	stops.Wait()
	if worker.admit(tui.Notification{}) {
		t.Fatal("stopped worker accepted work")
	}
	select {
	case <-worker.done:
	default:
		t.Fatal("worker did not terminate cleanly")
	}
}

func TestNotificationWorkerTimeoutAndBackendFailureDoNotStopWorker(t *testing.T) {
	started := make(chan struct{}, 1)
	finished := make(chan int, 3)
	canceled := make(chan error, 1)
	var calls atomic.Int32
	backend := notificationBackendFunc(func(ctx context.Context, _ tui.Notification) error {
		call := int(calls.Add(1))
		if call == 1 {
			if _, ok := ctx.Deadline(); !ok {
				started <- struct{}{}
				canceled <- errors.New("notification command has no deadline")
				return errors.New("notification command has no deadline")
			}
			started <- struct{}{}
			<-ctx.Done()
			canceled <- ctx.Err()
			return ctx.Err()
		}
		finished <- call
		if call == 2 {
			return errors.New("synthetic backend failure")
		}
		return nil
	})
	worker := newNotificationWorker(context.Background(), backend)
	worker.timeout = 25 * time.Millisecond
	defer worker.stop()
	if !worker.admit(tui.Notification{Title: "timeout"}) {
		t.Fatal("timeout request rejected")
	}
	<-started
	if err := <-canceled; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
	if !worker.admit(tui.Notification{Title: "failure"}) || <-finished != 2 {
		t.Fatal("backend failure request did not execute")
	}
	if !worker.admit(tui.Notification{Title: "after failure"}) || <-finished != 3 {
		t.Fatal("worker stopped after backend failure")
	}
}

func TestNotificationWorkerConcurrentAdmissionAndShutdownIsSafe(t *testing.T) {
	started := make(chan struct{}, 1)
	backend := notificationBackendFunc(func(ctx context.Context, _ tui.Notification) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	})
	worker := newNotificationWorker(context.Background(), backend)
	worker.timeout = time.Hour
	if !worker.admit(tui.Notification{Title: "in flight"}) {
		t.Fatal("initial request rejected")
	}
	<-started
	start := make(chan struct{})
	var operations sync.WaitGroup
	for index := 0; index < notificationQueueCapacity*2; index++ {
		operations.Add(1)
		go func() {
			defer operations.Done()
			<-start
			worker.admit(tui.Notification{Title: "concurrent"})
		}()
	}
	operations.Add(1)
	go func() {
		defer operations.Done()
		<-start
		worker.stop()
	}()
	close(start)
	operations.Wait()
	if worker.admit(tui.Notification{Title: "after stop"}) {
		t.Fatal("admission succeeded after shutdown")
	}
}

func TestNotificationWorkerParentCancellationStopsPromptly(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	backend := notificationBackendFunc(func(ctx context.Context, _ tui.Notification) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	worker := newNotificationWorker(parent, backend)
	worker.timeout = time.Hour
	if !worker.admit(tui.Notification{Title: "in flight"}) {
		t.Fatal("initial request rejected")
	}
	<-started
	cancel()
	select {
	case <-worker.done:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not terminate worker")
	}
	if worker.admit(tui.Notification{Title: "after cancellation"}) {
		t.Fatal("admission succeeded after parent cancellation")
	}
	worker.stop()
}

func TestMissingNotificationBackendIsGraceful(t *testing.T) {
	worker := newNotificationWorker(context.Background(), nil)
	defer worker.stop()
	if worker.admit(tui.Notification{Title: "ignored", Body: "ignored"}) {
		t.Fatal("missing optional backend accepted notification")
	}
}
