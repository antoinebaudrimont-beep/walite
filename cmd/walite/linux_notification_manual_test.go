package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

// Opt-in real-terminal fixture, skipped in all automated runs. No WhatsApp
// client, service, or database is constructed. USR1/USR2 inject synthetic A/B
// messages through the real TUI -> notification worker -> desktop backend.
func TestLinuxNotificationManualFixture(t *testing.T) {
	directory := os.Getenv("WALITE_CLICK_TEST_DIR")
	if directory == "" {
		t.Skip("requires explicit isolated manual fixture directory")
	}
	if runtime.GOOS != "linux" || !filepath.IsAbs(directory) {
		t.Fatal("manual fixture requires Linux and an absolute private directory")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatal("manual fixture directory must exist with permissions 0700")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(directory, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(directory, "data"))
	ctx, cancel := terminationContext(context.Background())
	defer cancel()
	trace, err := os.OpenFile(filepath.Join(directory, "actions.trace"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	var traceMu sync.Mutex
	capabilityOptions := platformCapabilityOptions{notificationRunner: runNotificationCommand, linuxNotificationIconPath: defaultLinuxNotificationIcon(), linuxActions: &linuxNotificationActionOptions{
		windowID: os.Getenv("WINDOWID"), x11: os.Getenv("DISPLAY") != "" && os.Getenv("XDG_SESSION_TYPE") != "wayland",
		run: func(ctx context.Context, command string, args []string) (string, error) {
			output, err := runLinuxNotificationCommand(ctx, command, args)
			traceMu.Lock()
			// Synthetic fixture diagnostics contain no text, identities or tokens.
			_, _ = fmt.Fprintf(trace, "%s ok=%t default=%t output_bytes=%d canceled=%t\n", filepath.Base(command), err == nil, strings.TrimSpace(output) == "default", len(output), ctx.Err() != nil)
			traceMu.Unlock()
			return output, err
		},
	}}
	capabilities := selectPlatformCapabilitiesWithOptions("linux", exec.LookPath, capabilityOptions)
	if capabilities.newLinuxNotificationActions == nil {
		t.Fatal("click capabilities missing: requires X11, WINDOWID, notify-send, gdbus, xprop and wmctrl")
	}
	actions, err := capabilities.newLinuxNotificationActions(ctx)
	if err != nil {
		t.Fatal("click dispatcher unavailable")
	}
	defer actions.stop()
	worker := newNotificationWorker(ctx, actions)
	defer worker.stop()
	live := make(chan tui.LiveMessage, 8)
	triggers := make(chan os.Signal, 8)
	signal.Notify(triggers, syscall.SIGUSR1, syscall.SIGUSR2)
	defer signal.Stop(triggers)
	producer, stopProducer := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sequence := uint64(1); ; sequence++ {
			select {
			case <-producer.Done():
				return
			case trigger := <-triggers:
				id, label := "notification-a", "A"
				if trigger == syscall.SIGUSR2 {
					id, label = "notification-b", "B"
				}
				now := time.Now()
				event := tui.LiveMessage{ChatID: id, MessageID: fmt.Sprintf("fixture-%d", sequence), Text: fmt.Sprintf("Fixture notification %s #%d", label, sequence), SentAt: now, ActivityTime: now, BodyRetained: true, UnreadCount: 1, NotificationEligible: true}
				select {
				case live <- event:
				case <-producer.Done():
					return
				}
			}
		}
	}()
	defer func() { stopProducer(); <-done }()
	pidPath := filepath.Join(directory, "fixture.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pidPath)
	screen, err := tcell.NewScreen()
	if err != nil {
		t.Fatal(err)
	}
	options := tui.DefaultOptions()
	options.NotificationPreviews = true // synthetic A/B labels, no private data
	initial := tui.InitialState{Chats: []tui.InitialChat{
		{ID: "notification-a", Title: "Fixture A", Messages: []tui.InitialMessage{{ID: "a-initial", Text: "Synthetic conversation A", BodyRetained: true, SentAt: time.Now()}}},
		{ID: "notification-b", Title: "Fixture B", Messages: []tui.InitialMessage{{ID: "b-initial", Text: "Synthetic conversation B", BodyRetained: true, SentAt: time.Now()}}},
	}}
	if err := tui.Run(ctx, screen, tui.Input{Options: options, InitialState: initial, LiveEvents: live, ChatActivations: actions.activations, Notify: worker.admit, NotificationsAvailable: true}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
