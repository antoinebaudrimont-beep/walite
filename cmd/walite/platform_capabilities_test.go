package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

func TestLinuxCapabilitySelectionPreservesValidatedHelpers(t *testing.T) {
	available := map[string]string{
		"xfce4-terminal": "/usr/bin/xfce4-terminal",
		"xdg-open":       "/usr/bin/xdg-open",
		"xclip":          "/usr/bin/xclip",
		"ueberzugpp":     "/usr/bin/ueberzugpp",
		"notify-send":    "/usr/bin/notify-send",
	}
	capabilities := selectPlatformCapabilities("linux", func(name string) (string, error) {
		if path := available[name]; path != "" {
			return path, nil
		}
		return "", os.ErrNotExist
	})
	pairing, pairingOK := capabilities.pairing.(commandPairingPresenter)
	opener, openerOK := capabilities.opener.(commandSystemOpener)
	clipboard, clipboardOK := capabilities.clipboard.(commandClipboard)
	inline, inlineOK := capabilities.inlineImage.(*ueberzugPreviewer)
	notifier, notifierOK := capabilities.notifier.(notifySendBackend)
	if !pairingOK || pairing.command != available["xfce4-terminal"] ||
		!openerOK || opener.command != available["xdg-open"] ||
		!clipboardOK || clipboard.command != available["xclip"] || strings.Join(clipboard.args, "\x00") != "-selection\x00clipboard\x00-in" ||
		!inlineOK || inline.binary != available["ueberzugpp"] || !notifierOK || notifier.command != available["notify-send"] || capabilities.platformError != nil || capabilities.newChatActivation != nil {
		t.Fatalf("capabilities=%+v pairing=%+v opener=%+v clipboard=%+v inline=%+v", capabilities, pairing, opener, clipboard, inline)
	}
}

func TestOptionalCapabilitiesAreIndependentAndUnsupportedPlatformIsControlled(t *testing.T) {
	capabilities := selectPlatformCapabilities("linux", func(name string) (string, error) {
		if name == "xdg-open" {
			return "/usr/bin/xdg-open", nil
		}
		return "", os.ErrNotExist
	})
	if capabilities.opener == nil || capabilities.clipboard != nil || capabilities.notifier != nil {
		t.Fatalf("capabilities=%+v", capabilities)
	}
	if err := capabilities.pairing.Present(context.Background(), "/usr/bin/walite"); !errors.Is(err, errCapabilityUnavailable) {
		t.Fatalf("pairing error=%v", err)
	}
	if err := capabilities.inlineImage.Show(context.Background(), filepath.Join(t.TempDir(), "image.jpg"), tui.MediaRequest{}); !errors.Is(err, errCapabilityUnavailable) {
		t.Fatalf("inline error=%v", err)
	}

	unsupported := selectPlatformCapabilities("plan9", func(string) (string, error) { return "", os.ErrNotExist })
	if unsupported.platformError == nil || unsupported.opener != nil || unsupported.clipboard != nil {
		t.Fatalf("unsupported=%+v", unsupported)
	}
	if err := unsupported.pairing.Present(context.Background(), "/usr/bin/walite"); !errors.Is(err, errCapabilityUnavailable) {
		t.Fatalf("unsupported pairing error=%v", err)
	}
}

func TestDarwinCapabilitySelectionUsesBuiltInToolsWithoutITerm(t *testing.T) {
	available := map[string]string{
		"osascript": "/usr/bin/osascript",
		"open":      "/usr/bin/open",
		"pbcopy":    "/usr/bin/pbcopy",
		"iTerm2":    "/Applications/iTerm.app",
	}
	probed := make([]string, 0, 3)
	capabilities := selectPlatformCapabilities("darwin", func(name string) (string, error) {
		probed = append(probed, name)
		if path := available[name]; path != "" {
			return path, nil
		}
		return "", os.ErrNotExist
	})
	pairing, pairingOK := capabilities.pairing.(darwinPairingPresenter)
	opener, openerOK := capabilities.opener.(commandSystemOpener)
	clipboard, clipboardOK := capabilities.clipboard.(commandClipboard)
	notifier, notifierOK := capabilities.notifier.(darwinNotificationBackend)
	if !pairingOK || pairing.command != available["osascript"] || !openerOK || opener.command != available["open"] ||
		!clipboardOK || clipboard.command != available["pbcopy"] || !notifierOK || notifier.command != available["osascript"] || !capabilities.systemMedia ||
		capabilities.platformError != nil || capabilities.newChatActivation != nil {
		t.Fatalf("capabilities=%+v", capabilities)
	}
	if strings.Contains(strings.Join(probed, " "), "iTerm") {
		t.Fatalf("iTerm was probed: %v", probed)
	}
	type commandCall struct {
		name, input string
		args        []string
	}
	calls := make([]commandCall, 0, 2)
	runner := func(_ context.Context, name string, args []string, input io.Reader) error {
		var data []byte
		if input != nil {
			data, _ = io.ReadAll(input)
		}
		calls = append(calls, commandCall{name: name, input: string(data), args: append([]string(nil), args...)})
		return nil
	}
	opener.run = runner
	clipboard.run = runner
	const value = "https://example.test/Caf%C3%A9?x=$(ignored)&y=日本語"
	if err := opener.Open(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if err := clipboard.Copy(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].name != available["open"] || len(calls[0].args) != 1 || calls[0].args[0] != value || calls[0].input != "" ||
		calls[1].name != available["pbcopy"] || len(calls[1].args) != 0 || calls[1].input != value {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestDarwinCapabilitySelectionPrefersInstalledNotificationHelper(t *testing.T) {
	home := t.TempDir()
	helperPath := filepath.Join(home, "Applications", darwinNotificationHelperAppName)
	if err := os.MkdirAll(helperPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ITERM_SESSION_ID", testITermSessionID)
	var calls []string
	capabilities := selectPlatformCapabilitiesWithOptions("darwin", darwinToolLookup, platformCapabilityOptions{
		darwinNotificationHelperPath: helperPath,
		notificationRunner: func(_ context.Context, command string, _ []string) error {
			calls = append(calls, command)
			return nil
		},
	})
	backend, ok := capabilities.notifier.(fallbackNotificationBackend)
	if !ok || capabilities.newChatActivation == nil {
		t.Fatalf("notifier=%T want fallbackNotificationBackend", capabilities.notifier)
	}
	helper, ok := backend.preferred.(*darwinHelperNotificationBackend)
	if !ok || helper.helperPath != helperPath || helper.sessionUUID != "235DEA09-F11A-4853-B92F-CC1E3E85DE65" {
		t.Fatalf("preferred=%T %+v", backend.preferred, helper)
	}
	if _, ok := backend.fallback.(darwinNotificationBackend); !ok {
		t.Fatalf("fallback=%T want darwinNotificationBackend", backend.fallback)
	}

	// Selection captures the originating session; later environment changes do
	// not redirect an already initialized Walite instance.
	t.Setenv("ITERM_SESSION_ID", "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE")
	if err := capabilities.notifier.Notify(context.Background(), tui.Notification{Title: "title", Body: "body"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{darwinOpenCommand}) {
		t.Fatalf("commands=%v", calls)
	}
	requests := requestFiles(t, filepath.Join(home, "Library", "Caches", "walite"))
	if len(requests) != 1 {
		t.Fatalf("requests=%v", requests)
	}
	if got := readDarwinHelperRequest(t, requests[0]).SessionUUID; got != "235DEA09-F11A-4853-B92F-CC1E3E85DE65" {
		t.Fatalf("session UUID=%q", got)
	}
}

func TestDarwinCapabilitySelectionFallsBackWhenHelperCannotInitialize(t *testing.T) {
	home := t.TempDir()
	installedHelper := filepath.Join(home, "Applications", darwinNotificationHelperAppName)
	if err := os.MkdirAll(installedHelper, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	for _, test := range []struct {
		name, sessionID, helperPath string
	}{
		{name: "missing helper", sessionID: testITermSessionID, helperPath: filepath.Join(home, "Applications", "Missing.app")},
		{name: "missing session", helperPath: installedHelper},
		{name: "invalid session", sessionID: "not-an-iterm-session", helperPath: installedHelper},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ITERM_SESSION_ID", test.sessionID)
			capabilities := selectPlatformCapabilitiesWithOptions("darwin", darwinToolLookup, platformCapabilityOptions{
				darwinNotificationHelperPath: test.helperPath,
			})
			if _, ok := capabilities.notifier.(darwinNotificationBackend); !ok || capabilities.newChatActivation != nil {
				t.Fatalf("notifier=%T want darwinNotificationBackend", capabilities.notifier)
			}
		})
	}
}

func TestDarwinHelperLaunchFailureUsesAppleScriptFallback(t *testing.T) {
	home := t.TempDir()
	helperPath := filepath.Join(home, "Applications", darwinNotificationHelperAppName)
	if err := os.MkdirAll(helperPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ITERM_SESSION_ID", testITermSessionID)
	launchError := errors.New("synthetic helper launch failure")
	var calls []string
	capabilities := selectPlatformCapabilitiesWithOptions("darwin", darwinToolLookup, platformCapabilityOptions{
		darwinNotificationHelperPath: helperPath,
		notificationRunner: func(_ context.Context, command string, _ []string) error {
			calls = append(calls, command)
			if command == darwinOpenCommand {
				return launchError
			}
			return nil
		},
	})
	if err := capabilities.notifier.Notify(context.Background(), tui.Notification{Title: "title", Body: "body"}); err != nil {
		t.Fatalf("fallback notification error=%v", err)
	}
	if want := []string{darwinOpenCommand, "/usr/bin/osascript"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands=%v want=%v", calls, want)
	}
	if requests := requestFiles(t, filepath.Join(home, "Library", "Caches", "walite")); len(requests) != 0 {
		t.Fatalf("failed helper launch retained requests: %v", requests)
	}
}

func TestDarwinHelperSelectionDoesNotChangeLinuxBackend(t *testing.T) {
	t.Setenv("ITERM_SESSION_ID", testITermSessionID)
	capabilities := selectPlatformCapabilitiesWithOptions("linux", func(name string) (string, error) {
		if name == "notify-send" {
			return "/usr/bin/notify-send", nil
		}
		return "", os.ErrNotExist
	}, platformCapabilityOptions{darwinNotificationHelperPath: "/Applications/Walite Notifications.app"})
	notifier, ok := capabilities.notifier.(notifySendBackend)
	if !ok || notifier.command != "/usr/bin/notify-send" {
		t.Fatalf("notifier=%T %+v", capabilities.notifier, notifier)
	}
}

func darwinToolLookup(name string) (string, error) {
	switch name {
	case "osascript", "open", "pbcopy":
		return "/usr/bin/" + name, nil
	default:
		return "", os.ErrNotExist
	}
}

func TestApplicationAndTUINeverNamePairingTerminal(t *testing.T) {
	application, err := os.ReadFile("application.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(application), "xfce4-terminal") || strings.Contains(string(application), "launchXFCE") {
		t.Fatal("application wiring knows the concrete pairing terminal")
	}
	root := filepath.Join("..", "..", "internal", "tui")
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(contents), "xfce4-terminal") {
			t.Errorf("%s names a concrete terminal", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
