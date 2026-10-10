package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

const (
	linuxNotificationActionSlots   = 4
	linuxNotificationActionTimeout = 20 * time.Second
	linuxNotificationOutputLimit   = 8192
)

type linuxNotificationOutputRunner func(context.Context, string, []string) (string, error)

// Child output is bounded even if an optional desktop tool misbehaves. Run
// reaps the child; WaitDelay also bounds pipes inherited by descendants.
func runLinuxNotificationCommand(ctx context.Context, command string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	output := &linuxNotificationOutput{}
	cmd.Stdout = output
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err == nil && output.overflow {
		err = errCapabilityUnavailable
	}
	return output.text.String(), err
}

type linuxNotificationOutput struct {
	text     strings.Builder
	overflow bool
}

func (output *linuxNotificationOutput) Write(data []byte) (int, error) {
	length := len(data)
	remaining := linuxNotificationOutputLimit - output.text.Len()
	if remaining < length {
		output.overflow = true
		data = data[:remaining]
	}
	_, _ = output.text.Write(data)
	return length, nil
}

type linuxNotificationActionOptions struct {
	windowID             string
	x11                  bool
	wmctrl, xprop, gdbus string
	run                  linuxNotificationOutputRunner
}

func normalizedLinuxWindowID(value string) (string, error) {
	base := 10
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		base, value = 16, value[2:]
	}
	if value == "" || strings.ContainsAny(value, "+- \t\r\n") {
		return "", errCapabilityUnavailable
	}
	id, err := strconv.ParseUint(value, base, 32)
	if err != nil || id == 0 {
		return "", errCapabilityUnavailable
	}
	return "0x" + strconv.FormatUint(id, 16), nil
}

// Four fixed workers own at most four queued/in-flight action processes in
// total. Admission does not wait for clicks, and overflow uses ordinary
// notifications. The jobs channel is never closed under concurrent producers.
type linuxNotificationActions struct {
	ordinary                      notifySendBackend
	options                       linuxNotificationActionOptions
	windowID, marker, markerValue string
	ctx                           context.Context
	cancel                        context.CancelFunc
	jobs                          chan tui.Notification
	slots                         chan struct{}
	activations                   chan tui.ChatActivationRequest
	waiters                       sync.WaitGroup
	mu                            sync.Mutex
	activationMu                  sync.Mutex
	stopped                       bool
	stopOnce                      sync.Once
}

func newLinuxNotificationActions(parent context.Context, ordinary notifySendBackend, options linuxNotificationActionOptions) (*linuxNotificationActions, error) {
	id, err := normalizedLinuxWindowID(options.windowID)
	if err != nil || !options.x11 || options.wmctrl == "" || options.xprop == "" || options.gdbus == "" || ordinary.command == "" {
		return nil, errCapabilityUnavailable
	}
	if options.run == nil {
		options.run = runLinuxNotificationCommand
	}
	probe, cancel := context.WithTimeout(parent, notificationCommandTimeout)
	defer cancel()
	help, err := options.run(probe, ordinary.command, []string{"--help"})
	if err != nil || !strings.Contains(help, "--action=") || !strings.Contains(help, "--expire-time=") {
		return nil, errCapabilityUnavailable
	}
	capabilities, err := options.run(probe, options.gdbus, []string{"call", "--session", "--dest", "org.freedesktop.Notifications", "--object-path", "/org/freedesktop/Notifications", "--method", "org.freedesktop.Notifications.GetCapabilities"})
	if err != nil || !strings.Contains(capabilities, "'actions'") {
		return nil, errCapabilityUnavailable
	}
	// Reject nonexistent IDs before adding a per-instance identity marker. The
	// marker prevents an XID reused later from activating an unrelated window.
	properties, err := options.run(probe, options.xprop, []string{"-id", id, "WM_CLASS", "_NET_WM_PID"})
	if err != nil || !strings.Contains(properties, "WM_CLASS(STRING)") || !strings.Contains(properties, "_NET_WM_PID(CARDINAL)") {
		return nil, errCapabilityUnavailable
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errCapabilityUnavailable
	}
	value := hex.EncodeToString(random[:])
	ctx, cancelOwner := context.WithCancel(parent)
	actions := &linuxNotificationActions{
		ordinary: ordinary, options: options, windowID: id,
		marker: "_WALITE_NOTIFICATION_" + value, markerValue: value,
		ctx: ctx, cancel: cancelOwner, jobs: make(chan tui.Notification, linuxNotificationActionSlots),
		slots: make(chan struct{}, linuxNotificationActionSlots), activations: make(chan tui.ChatActivationRequest, chatActivationQueueCapacity),
	}
	_, err = options.run(probe, options.xprop, []string{"-id", id, "-f", actions.marker, "8s", "-set", actions.marker, value})
	if err != nil || !actions.windowMatches(probe) {
		cancelOwner()
		actions.removeMarker()
		return nil, errCapabilityUnavailable
	}
	for index := 0; index < linuxNotificationActionSlots; index++ {
		actions.waiters.Add(1)
		go actions.run()
	}
	return actions, nil
}

func (actions *linuxNotificationActions) windowMatches(ctx context.Context) bool {
	output, err := actions.options.run(ctx, actions.options.xprop, []string{"-id", actions.windowID, actions.marker})
	return err == nil && strings.TrimSpace(output) == actions.marker+"(STRING) = \""+actions.markerValue+"\""
}

func (actions *linuxNotificationActions) Notify(ctx context.Context, notification tui.Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	actions.mu.Lock()
	if actions.stopped || actions.ctx.Err() != nil {
		actions.mu.Unlock()
		return context.Canceled
	}
	_, chatErr := model.NewChatID(notification.ChatID)
	if chatErr == nil && strings.TrimSpace(notification.ChatID) != "" {
		select {
		case actions.slots <- struct{}{}:
			actions.jobs <- notification // slots bound all queued AND active work
			actions.mu.Unlock()
			return nil
		default:
		}
	}
	actions.mu.Unlock()
	return actions.ordinary.Notify(ctx, notification)
}

func (actions *linuxNotificationActions) run() {
	defer actions.waiters.Done()
	for {
		select {
		case <-actions.ctx.Done():
			return
		case notification := <-actions.jobs:
			if actions.ctx.Err() != nil {
				return
			}
			actions.wait(notification)
			<-actions.slots
		}
	}
}

func (actions *linuxNotificationActions) wait(notification tui.Notification) {
	ctx, cancel := context.WithTimeout(actions.ctx, linuxNotificationActionTimeout)
	defer cancel()
	args := actions.ordinary.arguments(notification, []string{"--app-name=walite", "--action=default=Open", "--expire-time=15000"})
	output, err := actions.options.run(ctx, actions.ordinary.command, args)
	if err != nil {
		if ctx.Err() == nil {
			fallback, done := context.WithTimeout(actions.ctx, notificationCommandTimeout)
			_ = actions.ordinary.Notify(fallback, notification)
			done()
		}
		return
	}
	if ctx.Err() != nil || strings.TrimSpace(output) != "default" {
		return // expiry, dismissal, unexpected output: no activation
	}
	actions.activationMu.Lock()
	defer actions.activationMu.Unlock()
	if ctx.Err() != nil || !actions.windowMatches(ctx) {
		return
	}
	if _, err := actions.options.run(ctx, actions.options.wmctrl, []string{"-i", "-a", actions.windowID}); err != nil {
		return
	}
	actions.mu.Lock()
	defer actions.mu.Unlock()
	if actions.stopped || actions.ctx.Err() != nil {
		return
	}
	select {
	case actions.activations <- tui.ChatActivationRequest{ChatID: notification.ChatID}:
	default:
	}
}

func (actions *linuxNotificationActions) removeMarker() {
	ctx, cancel := context.WithTimeout(context.Background(), notificationCommandTimeout)
	defer cancel()
	if actions.windowMatches(ctx) {
		_, _ = actions.options.run(ctx, actions.options.xprop, []string{"-id", actions.windowID, "-remove", actions.marker})
	}
}

func (actions *linuxNotificationActions) stop() {
	actions.stopOnce.Do(func() {
		actions.mu.Lock()
		actions.stopped = true
		actions.cancel()
		actions.mu.Unlock()
		actions.waiters.Wait()
		actions.removeMarker()
	})
}

// Optional tools and daemon capabilities are checked once per application run,
// never in WhatsApp callbacks. Missing capabilities leave the ordinary backend.
func linuxNotificationActionFactory(lookup executableLookup, ordinary notifySendBackend, options *linuxNotificationActionOptions) func(context.Context) (*linuxNotificationActions, error) {
	if options == nil || !options.x11 {
		return nil
	}
	settings := *options
	if _, err := normalizedLinuxWindowID(settings.windowID); err != nil {
		return nil
	}
	var err error
	settings.wmctrl, err = lookup("wmctrl")
	if err != nil {
		return nil
	}
	settings.xprop, err = lookup("xprop")
	if err != nil {
		return nil
	}
	settings.gdbus, err = lookup("gdbus")
	if err != nil {
		return nil
	}
	return func(ctx context.Context) (*linuxNotificationActions, error) {
		return newLinuxNotificationActions(ctx, ordinary, settings)
	}
}

var _ desktopNotifier = (*linuxNotificationActions)(nil)
