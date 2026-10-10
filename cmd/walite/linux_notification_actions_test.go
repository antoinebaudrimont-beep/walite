package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type linuxActionResult struct {
	output string
	err    error
}
type linuxActionCall struct {
	args  []string
	ctx   context.Context
	reply chan linuxActionResult
}
type linuxActionFixture struct {
	mu                                sync.Mutex
	markers                           map[string]string
	stale, missing, noActions, oldCLI bool
	calls                             chan linuxActionCall
	focus                             chan string
	plain                             chan tui.Notification
}

func newLinuxActionFixture() *linuxActionFixture {
	return &linuxActionFixture{markers: make(map[string]string), calls: make(chan linuxActionCall, 8), focus: make(chan string, 8), plain: make(chan tui.Notification, 64)}
}

func (fixture *linuxActionFixture) run(ctx context.Context, command string, args []string) (string, error) {
	if command == "notify-send" {
		if reflect.DeepEqual(args, []string{"--help"}) {
			if fixture.oldCLI {
				return "no actions", nil
			}
			return "--action= --expire-time=", nil
		}
		call := linuxActionCall{args: append([]string(nil), args...), ctx: ctx, reply: make(chan linuxActionResult, 1)}
		fixture.calls <- call
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case result := <-call.reply:
			return result.output, result.err
		}
	}
	if command == "gdbus" {
		if fixture.noActions {
			return "(['body'],)", nil
		}
		return "(['body', 'actions'],)", nil
	}
	if command == "wmctrl" {
		fixture.focus <- args[2]
		return "", nil
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.missing {
		return "", errors.New("window absent")
	}
	if len(args) == 4 && args[2] == "WM_CLASS" {
		return "WM_CLASS(STRING) = terminal\n_NET_WM_PID(CARDINAL) = 42", nil
	}
	if len(args) == 8 && args[5] == "-set" {
		fixture.markers[args[6]] = args[7]
		return "", nil
	}
	if len(args) == 4 && args[2] == "-remove" {
		delete(fixture.markers, args[3])
		return "", nil
	}
	if len(args) == 3 && !fixture.stale {
		if value, ok := fixture.markers[args[2]]; ok {
			return args[2] + "(STRING) = \"" + value + "\"\n", nil
		}
	}
	return "not found", nil
}

func (fixture *linuxActionFixture) options() linuxNotificationActionOptions {
	return linuxNotificationActionOptions{windowID: "12345", x11: true, wmctrl: "wmctrl", xprop: "xprop", gdbus: "gdbus", run: fixture.run}
}

func (fixture *linuxActionFixture) backend() notifySendBackend {
	return notifySendBackend{command: "notify-send", iconPath: "/icon with spaces.png", run: func(ctx context.Context, _ string, args []string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fixture.plain <- tui.Notification{Title: args[len(args)-2], Body: args[len(args)-1]}
		return nil
	}}
}

func startLinuxActionFixture(t *testing.T, fixture *linuxActionFixture) *linuxNotificationActions {
	t.Helper()
	actions, err := newLinuxNotificationActions(context.Background(), fixture.backend(), fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(actions.stop)
	return actions
}

func receiveLinuxAction[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("action test failed to make progress")
		var zero T
		return zero
	}
}

func TestLinuxNotificationWindowIDValidation(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "+1", " 12", "12\n", "0x", "0x100000000", "123;command"} {
		if _, err := normalizedLinuxWindowID(value); err == nil {
			t.Errorf("accepted invalid WINDOWID %q", value)
		}
	}
	for _, value := range []string{"12345", "0x3039", "0X3039"} {
		if id, err := normalizedLinuxWindowID(value); err != nil || id != "0x3039" {
			t.Errorf("id=%q err=%v", id, err)
		}
	}
}

func TestLinuxNotificationDefaultActionPreservesIndependentChatIDs(t *testing.T) {
	fixture := newLinuxActionFixture()
	actions := startLinuxActionFixture(t, fixture)
	notifications := []tui.Notification{
		{ChatID: "chat-a@lid", Title: "walite", Body: "New WhatsApp message"},
		{ChatID: "chat-b@g.us", Title: "--hostile ' café 👋", Body: "Unicode body & \"quotes\""},
	}
	for _, notification := range notifications {
		ctx, cancel := context.WithTimeout(context.Background(), notificationCommandTimeout)
		if err := actions.Notify(ctx, notification); err != nil {
			t.Fatal(err)
		}
		cancel() // five-second worker finishing must NOT cancel the action waiter
	}
	first, second := receiveLinuxAction(t, fixture.calls), receiveLinuxAction(t, fixture.calls)
	byTitle := map[string]linuxActionCall{first.args[len(first.args)-2]: first, second.args[len(second.args)-2]: second}
	for _, notification := range []tui.Notification{notifications[1], notifications[0]} {
		call := byTitle[notification.Title]
		want := []string{"--app-name=walite", "--action=default=Open", "--expire-time=15000", "--icon", "/icon with spaces.png", "--", notification.Title, notification.Body}
		if !reflect.DeepEqual(call.args, want) {
			t.Fatalf("argv=%q", call.args)
		}
		if deadline, ok := call.ctx.Deadline(); !ok || time.Until(deadline) > linuxNotificationActionTimeout || time.Until(deadline) < 15*time.Second {
			t.Fatal("wrong action deadline")
		}
		call.reply <- linuxActionResult{output: "default\n"}
		if id := receiveLinuxAction(t, fixture.focus); id != "0x3039" {
			t.Fatalf("focused %q", id)
		}
		if request := receiveLinuxAction(t, actions.activations); request.ChatID != notification.ChatID {
			t.Fatalf("activation=%+v", request)
		}
	}
}

func TestLinuxNotificationExpiryUnexpectedOutputAndStaleWindow(t *testing.T) {
	for _, test := range []struct {
		name, output string
		stale        bool
	}{
		{"expiration", "", false}, {"other action", "other", false}, {"unexpected output", "default\nother", false}, {"stale window", "default", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLinuxActionFixture()
			actions := startLinuxActionFixture(t, fixture)
			fixture.mu.Lock()
			fixture.stale = test.stale
			fixture.mu.Unlock()
			if err := actions.Notify(context.Background(), tui.Notification{ChatID: "chat-a@lid"}); err != nil {
				t.Fatal(err)
			}
			call := receiveLinuxAction(t, fixture.calls)
			call.reply <- linuxActionResult{output: test.output}
			<-call.ctx.Done() // wait() returned and canceled its context
			actions.stop()
			if len(fixture.focus) != 0 || len(actions.activations) != 0 {
				t.Fatal("invalid action changed focus/selection")
			}
		})
	}
}

func TestLinuxNotificationFourSlotsAndLaunchFailureFallback(t *testing.T) {
	fixture := newLinuxActionFixture()
	actions := startLinuxActionFixture(t, fixture)
	var calls []linuxActionCall
	for index := 0; index < linuxNotificationActionSlots; index++ {
		if err := actions.Notify(context.Background(), tui.Notification{ChatID: "chat-a@lid"}); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, receiveLinuxAction(t, fixture.calls))
	}
	if err := actions.Notify(context.Background(), tui.Notification{ChatID: "overflow@lid", Title: "ordinary"}); err != nil {
		t.Fatal(err)
	}
	if notification := receiveLinuxAction(t, fixture.plain); notification.Title != "ordinary" {
		t.Fatal("overflow did not fall back")
	}
	if len(actions.slots) != 4 {
		t.Fatal("action bound changed")
	}
	calls[0].reply <- linuxActionResult{err: errors.New("launch failure")}
	receiveLinuxAction(t, fixture.plain)
	actions.stop()
	for _, call := range calls {
		<-call.ctx.Done()
	}
	if len(actions.activations) != 0 || len(fixture.focus) != 0 {
		t.Fatal("shutdown activated chat")
	}
	if err := actions.Notify(context.Background(), tui.Notification{}); !errors.Is(err, context.Canceled) {
		t.Fatal("late notification accepted")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.markers) != 0 {
		t.Fatal("window marker left behind")
	}
}

func TestLinuxNotificationMissingCapabilitiesFallBack(t *testing.T) {
	for _, test := range []string{"daemon actions", "old notify-send", "missing window", "invalid window", "wayland"} {
		t.Run(test, func(t *testing.T) {
			fixture := newLinuxActionFixture()
			options := fixture.options()
			switch test {
			case "daemon actions":
				fixture.noActions = true
			case "old notify-send":
				fixture.oldCLI = true
			case "missing window":
				fixture.missing = true
			case "invalid window":
				options.windowID = "wrong"
			case "wayland":
				options.x11 = false
			}
			if actions, err := newLinuxNotificationActions(context.Background(), fixture.backend(), options); err == nil || actions != nil {
				t.Fatal("unsupported actions enabled")
			}
		})
	}
	fixture := newLinuxActionFixture()
	for _, missing := range []string{"wmctrl", "xprop", "gdbus"} {
		capabilities := selectPlatformCapabilitiesWithOptions("linux", func(name string) (string, error) {
			if name == missing {
				return "", errCapabilityUnavailable
			}
			return name, nil
		}, platformCapabilityOptions{linuxActions: func() *linuxNotificationActionOptions { options := fixture.options(); return &options }(), notificationRunner: fixture.backend().run})
		if capabilities.newLinuxNotificationActions != nil {
			t.Fatalf("missing %s not detected", missing)
		}
		if err := capabilities.notifier.Notify(context.Background(), tui.Notification{Title: "ordinary"}); err != nil {
			t.Fatal(err)
		}
		receiveLinuxAction(t, fixture.plain)
	}
}

func TestLinuxNotificationInstancesAndConcurrentShutdown(t *testing.T) {
	fixture := newLinuxActionFixture()
	first := startLinuxActionFixture(t, fixture)
	second := startLinuxActionFixture(t, fixture)
	if first.marker == second.marker {
		t.Fatal("instances shared a window marker")
	}
	if err := second.Notify(context.Background(), tui.Notification{ChatID: "second@lid"}); err != nil {
		t.Fatal(err)
	}
	call := receiveLinuxAction(t, fixture.calls)
	first.stop()
	call.reply <- linuxActionResult{output: "default"}
	receiveLinuxAction(t, fixture.focus)
	if request := receiveLinuxAction(t, second.activations); request.ChatID != "second@lid" || len(first.activations) != 0 {
		t.Fatal("activation crossed instances")
	}
	var workers sync.WaitGroup
	for index := 0; index < 16; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_ = second.Notify(context.Background(), tui.Notification{ChatID: "second@lid"})
		}()
	}
	for index := 0; index < 3; index++ {
		workers.Add(1)
		go func() { defer workers.Done(); second.stop() }()
	}
	workers.Wait()
	second.stop()
}

func TestLinuxNotificationOutputIsBounded(t *testing.T) {
	output := &linuxNotificationOutput{}
	input := []byte(strings.Repeat("x", linuxNotificationOutputLimit+100))
	if n, err := output.Write(input); n != len(input) || err != nil || output.text.Len() != linuxNotificationOutputLimit || !output.overflow {
		t.Fatal("child output is not bounded")
	}
}

// A fake desktop tool in a real child process exercises cancellation/reaping
// without depending on a desktop session, wmctrl, or notification daemon.
func TestLinuxNotificationChildProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--walite-action-child" {
		return
	}
	connection, err := net.Dial("unix", os.Args[len(os.Args)-1])
	if err != nil {
		os.Exit(2)
	}
	defer connection.Close()
	_, _ = fmt.Fprintln(connection, os.Getpid())
	var data [1]byte
	_, _ = connection.Read(data[:]) // barrier; parent cancels the owned process
	os.Exit(0)
}

func TestLinuxNotificationCommandCancellationReapsProcess(t *testing.T) {
	directory := activationTestDirectory(t)
	listener, err := net.Listen("unix", directory+"/child.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runLinuxNotificationCommand(ctx, os.Args[0], []string{"-test.run=^TestLinuxNotificationChildProcess$", "--", "--walite-action-child", directory + "/child.sock"})
		done <- err
	}()
	if listener, ok := listener.(*net.UnixListener); ok {
		_ = listener.SetDeadline(time.Now().Add(3 * time.Second))
	}
	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	var pid int
	if _, err := fmt.Fscanln(connection, &pid); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := receiveLinuxAction(t, done); err == nil {
		t.Fatal("cancellation did not stop the child")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Release()
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("child still exists after Run returned")
	}
}
