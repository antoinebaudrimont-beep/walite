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

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

const testITermSessionID = "w0t0p0:235dea09-f11a-4853-b92f-cc1e3e85de65"

func TestDarwinHelperBackendSerializesPrivateVersionedRequestAndDirectArguments(t *testing.T) {
	backend, requestDir, helperPath := newTestDarwinHelperBackend(t, testITermSessionID, func(context.Context, string, []string) error { return nil })
	notification := tui.Notification{Title: `Alice --flag "quotes"`, Body: "Café 🖕 A&B"}
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
