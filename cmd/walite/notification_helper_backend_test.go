package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

const testITermSessionID = "w0t0p0:235dea09-f11a-4853-b92f-cc1e3e85de65"

func TestDarwinHelperBackendSerializesPrivateVersionedRequestAndDirectArguments(t *testing.T) {
	backend, requestDir, helperPath := newTestDarwinHelperBackend(t, testITermSessionID, func(context.Context, string, []string) error { return nil })
	notification := tui.Notification{ChatID: "chat@lid", Title: `Alice --flag "quotes"`, Body: "Café 🖕 A&B"}
	var gotCommand string
	var gotArguments []string
	backend.run = func(_ context.Context, command string, arguments []string) error {
		gotCommand = command
		gotArguments = append([]string(nil), arguments...)
		return nil
	}
	if err := backend.Notify(context.Background(), notification); err != nil {
		t.Fatal(err)
	}
	if gotCommand != darwinOpenCommand {
		t.Fatalf("command=%q", gotCommand)
	}
	files := requestFiles(t, requestDir)
	if len(files) != 1 {
		t.Fatalf("request files=%v", files)
	}
	requestPath := files[0]
	if want := []string{"-a", helperPath, requestPath}; !reflect.DeepEqual(gotArguments, want) {
		t.Fatalf("arguments=%q want=%q", gotArguments, want)
	}
	request := readDarwinHelperRequest(t, requestPath)
	data, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 5 || strings.Contains(string(data), notification.ChatID) {
		t.Fatalf("version-1 helper request changed: fields=%v", fields)
	}
	if request.Version != darwinHelperRequestVersion || request.NotificationID == "" ||
		request.SessionUUID != "235DEA09-F11A-4853-B92F-CC1E3E85DE65" ||
		request.Title != notification.Title || request.Body != notification.Body {
		t.Fatalf("request=%+v", request)
	}
	if request.NotificationID != requestIDFromPath(requestPath) {
		t.Fatalf("notification ID=%q path=%q", request.NotificationID, requestPath)
	}
	assertMode(t, requestDir, 0o700)
	assertMode(t, requestPath, 0o600)
}

func TestNormalizeITermSessionUUID(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
		wantError         bool
	}{
		{name: "iTerm value", input: testITermSessionID, want: "235DEA09-F11A-4853-B92F-CC1E3E85DE65"},
		{name: "bare UUID", input: "235DEA09-F11A-4853-B92F-CC1E3E85DE65", want: "235DEA09-F11A-4853-B92F-CC1E3E85DE65"},
		{name: "missing", wantError: true},
		{name: "invalid hex", input: "w0t0p0:235DEA09-F11A-4853-B92F-CC1E3E85DE6Z", wantError: true},
		{name: "invalid shape", input: "w0t0p0:not-a-uuid", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeITermSessionUUID(test.input)
			if test.wantError {
				if !errors.Is(err, errITermSessionUnavailable) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("uuid=%q error=%v want=%q", got, err, test.want)
			}
		})
	}
}

func TestDarwinHelperBackendCapturesEnvironmentAndRejectsMissingSession(t *testing.T) {
	home := t.TempDir()
	helperPath := filepath.Join(t.TempDir(), "Walite Notifications.app")
	if err := os.Mkdir(helperPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ITERM_SESSION_ID", testITermSessionID)
	backend, err := newDarwinHelperNotificationBackend(helperPath, func(context.Context, string, []string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if backend.sessionUUID != "235DEA09-F11A-4853-B92F-CC1E3E85DE65" || backend.requestDir != filepath.Join(home, "Library", "Caches", "walite") {
		t.Fatalf("backend=%+v", backend)
	}
	if err := os.Unsetenv("ITERM_SESSION_ID"); err != nil {
		t.Fatal(err)
	}
	if _, err := newDarwinHelperNotificationBackend(helperPath, func(context.Context, string, []string) error { return nil }); !errors.Is(err, errITermSessionUnavailable) {
		t.Fatalf("missing session error=%v", err)
	}
}

func TestDarwinHelperBackendCreatesUniqueExclusiveRequests(t *testing.T) {
	backend, requestDir, _ := newTestDarwinHelperBackend(t, testITermSessionID, func(context.Context, string, []string) error { return nil })
	const count = 32
	for index := 0; index < count; index++ {
		if err := backend.Notify(context.Background(), tui.Notification{Title: "title", Body: "body"}); err != nil {
			t.Fatal(err)
		}
	}
	files := requestFiles(t, requestDir)
	if len(files) != count {
		t.Fatalf("request count=%d want=%d", len(files), count)
	}
	seen := make(map[string]struct{}, count)
	for _, path := range files {
		id := readDarwinHelperRequest(t, path).NotificationID
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate notification ID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestDarwinHelperBackendRejectsUnavailableHelper(t *testing.T) {
	_, err := newDarwinHelperNotificationBackendWithSession(
		filepath.Join(t.TempDir(), "missing.app"), filepath.Join(t.TempDir(), "requests"), testITermSessionID,
		func(context.Context, string, []string) error { return nil },
	)
	if !errors.Is(err, errCapabilityUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestDarwinHelperBackendLaunchFailureAndCancellationCleanRequests(t *testing.T) {
	launchError := errors.New("synthetic open failure")
	backend, requestDir, _ := newTestDarwinHelperBackend(t, testITermSessionID, func(context.Context, string, []string) error { return launchError })
	if err := backend.Notify(context.Background(), tui.Notification{}); !errors.Is(err, launchError) {
		t.Fatalf("launch error=%v", err)
	}
	if files := requestFiles(t, requestDir); len(files) != 0 {
		t.Fatalf("failed launch retained requests: %v", files)
	}

	started := make(chan struct{})
	backend.run = func(ctx context.Context, _ string, _ []string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- backend.Notify(ctx, tui.Notification{}) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v", err)
	}
	if files := requestFiles(t, requestDir); len(files) != 0 {
		t.Fatalf("canceled request retained files: %v", files)
	}
}

func TestDarwinHelperBackendPreservesDifferentCapturedSessions(t *testing.T) {
	root := t.TempDir()
	helperPath := filepath.Join(root, "Walite Notifications.app")
	requestDir := filepath.Join(root, "requests")
	if err := os.Mkdir(helperPath, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, string, []string) error { return nil }
	first, err := newDarwinHelperNotificationBackendWithSession(helperPath, requestDir, testITermSessionID, runner)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newDarwinHelperNotificationBackendWithSession(helperPath, requestDir, "w1t2p3:AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE", runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Notify(context.Background(), tui.Notification{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := second.Notify(context.Background(), tui.Notification{Title: "second"}); err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for _, path := range requestFiles(t, requestDir) {
		sessions = append(sessions, readDarwinHelperRequest(t, path).SessionUUID)
	}
	sort.Strings(sessions)
	want := []string{"235DEA09-F11A-4853-B92F-CC1E3E85DE65", "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"}
	if !reflect.DeepEqual(sessions, want) {
		t.Fatalf("sessions=%v want=%v", sessions, want)
	}
}

func TestDarwinHelperBackendCleansOnlyOldOwnedRequests(t *testing.T) {
	root := t.TempDir()
	oldRequest := filepath.Join(root, darwinHelperRequestPrefix+"old"+darwinHelperRequestSuffix)
	newRequest := filepath.Join(root, darwinHelperRequestPrefix+"new"+darwinHelperRequestSuffix)
	unrelated := filepath.Join(root, "unrelated.json")
	for _, path := range []string{oldRequest, newRequest, unrelated} {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldRequest, now.Add(-darwinHelperRequestMaxAge-time.Second), now.Add(-darwinHelperRequestMaxAge-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newRequest, now, now); err != nil {
		t.Fatal(err)
	}
	cleanupAbandonedDarwinHelperRequests(root, now)
	if _, err := os.Stat(oldRequest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old request remains: %v", err)
	}
	for _, path := range []string{newRequest, unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("preserved file %s: %v", path, err)
		}
	}
}

func TestDarwinHelperNotificationActivationMetadataIsOptionalAndPrivate(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	var requestPath string
	helper, requestDir, _ := newTestDarwinHelperBackend(t, testITermSessionID, func(_ context.Context, _ string, args []string) error {
		requestPath = args[2]
		return nil
	})
	bound := notifierWithChatActivation(helper, receiver)
	for _, test := range []struct {
		name, chatID   string
		wantActivation bool
	}{
		{name: "direct PN", chatID: "15550000001@s.whatsapp.net", wantActivation: true},
		{name: "direct LID", chatID: "987654@lid", wantActivation: true},
		{name: "group", chatID: "family@g.us", wantActivation: true},
		{name: "Unicode identity", chatID: "synthetic-café-👋", wantActivation: true},
		{name: "missing ChatID"},
		{name: "blank ChatID", chatID: "   "},
		{name: "control ChatID", chatID: "chat\n@lid"},
		{name: "oversized ChatID", chatID: strings.Repeat("i", model.MaxIdentifierBytes+1)},
		{name: "invalid UTF8 ChatID", chatID: "chat\xff@lid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Privacy-off presentation stays generic even when identity and
			// credentials are present as private request metadata.
			notification := tui.Notification{ChatID: test.chatID, Title: "walite", Body: "New WhatsApp message"}
			if err := bound.Notify(context.Background(), notification); err != nil {
				t.Fatal(err)
			}
			request := readDarwinHelperRequest(t, requestPath)
			data, err := os.ReadFile(requestPath)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal("invalid JSON request")
			}
			if test.wantActivation {
				if len(fields) != 8 || request.ChatID != test.chatID || request.ActivationSocket != receiver.socketPath || request.ActivationToken != receiver.token {
					t.Fatal("activation metadata does not match this notification and receiver")
				}
			} else if len(fields) != 5 || request.ChatID != "" || request.ActivationSocket != "" || request.ActivationToken != "" {
				t.Fatal("invalid/missing ChatID retained activation metadata")
			}
			// Match the existing Swift decoder's five known fields. Additive
			// keys must not change the original version-1 presentation contract.
			var legacy struct {
				Version        int    `json:"version"`
				NotificationID string `json:"notification_id"`
				SessionUUID    string `json:"session_uuid"`
				Title          string `json:"title"`
				Body           string `json:"body"`
			}
			if json.Unmarshal(data, &legacy) != nil || legacy.Version != 1 || legacy.NotificationID == "" || legacy.SessionUUID != "235DEA09-F11A-4853-B92F-CC1E3E85DE65" ||
				legacy.Title != notification.Title || legacy.Body != notification.Body {
				t.Fatal("legacy version-1 fields changed")
			}
			if strings.Contains(request.Title+request.Body, receiver.token) || strings.Contains(request.Title+request.Body, receiver.socketPath) {
				t.Fatal("activation credentials leaked into notification text")
			}
			assertMode(t, requestDir, 0o700)
			assertMode(t, requestPath, 0o600)
		})
	}
	if helper.activationSocket != "" || helper.activationToken != "" {
		t.Fatal("binding mutated shared helper backend")
	}
	// A backend reused after IPC becomes unavailable must not retain a stale
	// instance's credentials, and the fallback wrapper must remain intact.
	wrapped := fallbackNotificationBackend{preferred: bound, fallback: darwinNotificationBackend{command: "/usr/bin/osascript"}}
	unbound := notifierWithChatActivation(wrapped, nil).(fallbackNotificationBackend)
	fallback, ok := unbound.fallback.(darwinNotificationBackend)
	if !ok || fallback.command != "/usr/bin/osascript" {
		t.Fatal("binding replaced AppleScript fallback")
	}
	if err := unbound.Notify(context.Background(), tui.Notification{ChatID: "chat@lid"}); err != nil {
		t.Fatal(err)
	}
	if request := readDarwinHelperRequest(t, requestPath); request.ChatID != "" || request.ActivationSocket != "" || request.ActivationToken != "" {
		t.Fatal("unavailable receiver retained old activation metadata")
	}
}

func TestChatActivationBindingPreservesLinuxNotificationArguments(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	var arguments []string
	backend := notifySendBackend{command: "/usr/bin/notify-send", run: func(_ context.Context, command string, args []string) error {
		if command != "/usr/bin/notify-send" {
			t.Fatal("Linux notification command changed")
		}
		arguments = append([]string(nil), args...)
		return nil
	}}
	bound := notifierWithChatActivation(backend, receiver)
	if _, ok := bound.(notifySendBackend); !ok {
		t.Fatal("Linux notification backend replaced")
	}
	notification := tui.Notification{ChatID: "chat@lid", Title: "walite", Body: "New WhatsApp message"}
	if err := bound.Notify(context.Background(), notification); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(arguments, []string{"--app-name=walite", "--", notification.Title, notification.Body}) {
		t.Fatal("activation metadata affected Linux notification arguments")
	}
}

func newTestDarwinHelperBackend(t *testing.T, sessionID string, runner notificationCommandRunner) (*darwinHelperNotificationBackend, string, string) {
	t.Helper()
	root := t.TempDir()
	helperPath := filepath.Join(root, "Walite Notifications.app")
	requestDir := filepath.Join(root, "requests")
	if err := os.Mkdir(helperPath, 0o755); err != nil {
		t.Fatal(err)
	}
	backend, err := newDarwinHelperNotificationBackendWithSession(helperPath, requestDir, sessionID, runner)
	if err != nil {
		t.Fatal(err)
	}
	return backend, requestDir, helperPath
}

func requestFiles(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	return paths
}

func readDarwinHelperRequest(t *testing.T, path string) darwinHelperRequest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var request darwinHelperRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func requestIDFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(strings.TrimPrefix(base, darwinHelperRequestPrefix), darwinHelperRequestSuffix)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode=%#o want=%#o", path, got, want)
	}
}
