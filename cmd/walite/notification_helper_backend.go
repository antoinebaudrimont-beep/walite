package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

const (
	darwinHelperRequestVersion = 1
	darwinHelperRequestPrefix  = "notification-"
	darwinHelperRequestSuffix  = ".json"
	darwinHelperRequestMaxAge  = 24 * time.Hour
	darwinOpenCommand          = "/usr/bin/open"
)

var errITermSessionUnavailable = errors.New("iTerm2 session unavailable")

type darwinHelperRequest struct {
	Version          int    `json:"version"`
	NotificationID   string `json:"notification_id"`
	SessionUUID      string `json:"session_uuid"`
	Title            string `json:"title"`
	Body             string `json:"body"`
	ChatID           string `json:"chat_id,omitempty"`
	ActivationSocket string `json:"activation_socket,omitempty"`
	ActivationToken  string `json:"activation_token,omitempty"`
}

// A successful launch leaves the private request for the helper to delete after
// reading it; abandoned requests older than one day are removed the next time a
// backend is initialized.
type darwinHelperNotificationBackend struct {
	helperPath       string
	requestDir       string
	sessionUUID      string
	run              notificationCommandRunner
	activationSocket string
	activationToken  string
}

// Bind a private copy before starting the notification worker. Capabilities can
// be reused by multiple application runs; never mutate their shared backend or
// carry an old instance's endpoint into a run where IPC initialization failed.
func notifierWithChatActivation(notifier desktopNotifier, receiver *chatActivationReceiver) desktopNotifier {
	switch backend := notifier.(type) {
	case *darwinHelperNotificationBackend:
		if backend == nil {
			return notifier
		}
		owned := *backend
		owned.activationSocket, owned.activationToken = "", ""
		if receiver != nil {
			owned.activationSocket, owned.activationToken = receiver.socketPath, receiver.token
		}
		return &owned
	case fallbackNotificationBackend:
		backend.preferred = notifierWithChatActivation(backend.preferred, receiver)
		return backend
	default:
		return notifier
	}
}

type fallbackNotificationBackend struct {
	preferred desktopNotifier
	fallback  desktopNotifier
}

func (backend fallbackNotificationBackend) Notify(ctx context.Context, notification tui.Notification) error {
	if backend.preferred == nil {
		if backend.fallback == nil {
			return errCapabilityUnavailable
		}
		return backend.fallback.Notify(ctx, notification)
	}
	err := backend.preferred.Notify(ctx, notification)
	if err == nil || ctx.Err() != nil || backend.fallback == nil {
		return err
	}
	if fallbackErr := backend.fallback.Notify(ctx, notification); fallbackErr != nil {
		return errors.Join(err, fallbackErr)
	}
	return nil
}

func newDarwinHelperNotificationBackend(helperPath string, run notificationCommandRunner) (*darwinHelperNotificationBackend, error) {
	rawSessionID, ok := os.LookupEnv("ITERM_SESSION_ID")
	if !ok || strings.TrimSpace(rawSessionID) == "" {
		return nil, errITermSessionUnavailable
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return newDarwinHelperNotificationBackendWithSession(
		helperPath,
		filepath.Join(home, "Library", "Caches", "walite"),
		rawSessionID,
		run,
	)
}

func newDarwinHelperNotificationBackendWithSession(
	helperPath, requestDir, rawSessionID string,
	run notificationCommandRunner,
) (*darwinHelperNotificationBackend, error) {
	if run == nil {
		return nil, errCapabilityUnavailable
	}
	if !filepath.IsAbs(helperPath) {
		return nil, fmt.Errorf("%w: helper app path is not absolute", errCapabilityUnavailable)
	}
	info, err := os.Stat(helperPath)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect helper app: %v", errCapabilityUnavailable, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: helper app is not a bundle directory", errCapabilityUnavailable)
	}
	if !filepath.IsAbs(requestDir) {
		return nil, errors.New("notification request directory is not absolute")
	}
	sessionUUID, err := normalizeITermSessionUUID(rawSessionID)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateNotificationRequestDir(requestDir); err != nil {
		return nil, err
	}
	cleanupAbandonedDarwinHelperRequests(requestDir, time.Now())
	return &darwinHelperNotificationBackend{
		helperPath: helperPath, requestDir: requestDir, sessionUUID: sessionUUID, run: run,
	}, nil
}

func (backend *darwinHelperNotificationBackend) Notify(ctx context.Context, notification tui.Notification) error {
	if backend == nil || backend.run == nil {
		return errCapabilityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanupAbandonedDarwinHelperRequests(backend.requestDir, time.Now())
	requestPath, err := backend.writeRequest(notification)
	if err != nil {
		return err
	}
	if err := backend.run(ctx, darwinOpenCommand, []string{"-a", backend.helperPath, requestPath}); err != nil {
		_ = os.Remove(requestPath)
		return err
	}
	return nil
}

func (backend *darwinHelperNotificationBackend) writeRequest(notification tui.Notification) (path string, err error) {
	file, err := os.CreateTemp(backend.requestDir, darwinHelperRequestPrefix+"*"+darwinHelperRequestSuffix)
	if err != nil {
		return "", fmt.Errorf("create notification request: %w", err)
	}
	path = file.Name()
	keep := false
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close notification request: %w", closeErr)
		}
		if err != nil || !keep {
			_ = os.Remove(path)
		}
	}()
	if err = file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("protect notification request: %w", err)
	}
	base := filepath.Base(path)
	notificationID := strings.TrimSuffix(strings.TrimPrefix(base, darwinHelperRequestPrefix), darwinHelperRequestSuffix)
	request := darwinHelperRequest{
		Version: darwinHelperRequestVersion, NotificationID: notificationID,
		SessionUUID: backend.sessionUUID, Title: notification.Title, Body: notification.Body,
	}
	// Use the receiver's neutral identifier rules without resolving or changing
	// PN/LID identities. Incomplete activation metadata is omitted as one unit.
	if backend.activationSocket != "" && backend.activationToken != "" && strings.TrimSpace(notification.ChatID) != "" {
		if id, err := model.NewChatID(notification.ChatID); err == nil {
			request.ChatID = id.String()
			request.ActivationSocket, request.ActivationToken = backend.activationSocket, backend.activationToken
		}
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(request); err != nil {
		return "", fmt.Errorf("encode notification request: %w", err)
	}
	if err = file.Sync(); err != nil {
		return "", fmt.Errorf("sync notification request: %w", err)
	}
	keep = true
	return path, nil
}

func normalizeITermSessionUUID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if separator := strings.LastIndexByte(value, ':'); separator >= 0 {
		value = value[separator+1:]
	}
	if len(value) != 36 {
		return "", fmt.Errorf("%w: invalid UUID", errITermSessionUnavailable)
	}
	for index, character := range value {
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return "", fmt.Errorf("%w: invalid UUID", errITermSessionUnavailable)
			}
		default:
			if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
				return "", fmt.Errorf("%w: invalid UUID", errITermSessionUnavailable)
			}
		}
	}
	return strings.ToUpper(value), nil
}

func ensurePrivateNotificationRequestDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create notification request directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect notification request directory: %w", err)
	}
	return nil
}

func cleanupAbandonedDarwinHelperRequests(directory string, now time.Time) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	cutoff := now.Add(-darwinHelperRequestMaxAge)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, darwinHelperRequestPrefix) || !strings.HasSuffix(name, darwinHelperRequestSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(directory, name))
	}
}
