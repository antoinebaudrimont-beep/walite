package main

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type notificationBackendFunc func(context.Context, tui.Notification) error

func (function notificationBackendFunc) Notify(ctx context.Context, notification tui.Notification) error {
	return function(ctx, notification)
}

func TestNotificationBackendsPassUntrustedTextOnlyAsArguments(t *testing.T) {
	notification := tui.Notification{Title: `--title ' " $(touch nope)`, Body: "line one\n& rm -rf / 🙂"}
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
			if test.name == "darwin" && (containsText(darwinNotificationScript, notification.Title) || containsText(darwinNotificationScript, notification.Body)) {
				t.Fatal("untrusted notification data entered AppleScript source")
			}
		})
	}
}

func containsText(value, search string) bool {
	return search != "" && strings.Contains(value, search)
}

func TestNotificationWorkerIsBoundedNonblockingTimedAndIdempotent(t *testing.T) {
	started := make(chan struct{})
	backend := notificationBackendFunc(func(ctx context.Context, _ tui.Notification) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	})
	worker := newNotificationWorker(context.Background(), backend)
	worker.timeout = 20 * time.Millisecond
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
	var stops sync.WaitGroup
	for index := 0; index < 4; index++ {
		stops.Add(1)
		go func() { defer stops.Done(); worker.stop() }()
	}
	stops.Wait()
	if worker.admit(tui.Notification{}) {
		t.Fatal("stopped worker accepted work")
	}
}

func TestMissingNotificationBackendIsGraceful(t *testing.T) {
	worker := newNotificationWorker(context.Background(), nil)
	defer worker.stop()
	if worker.admit(tui.Notification{Title: "ignored", Body: "ignored"}) {
		t.Fatal("missing optional backend accepted notification")
	}
}
