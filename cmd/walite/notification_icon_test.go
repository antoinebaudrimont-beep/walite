package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

func installNotificationTestIcon(t *testing.T, path string) {
	t.Helper()
	// Copy the official artwork only into disposable test directories.
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "walite-icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxNotificationIconDiscovery(t *testing.T) {
	for _, location := range []string{
		"data/icons/walite.png",
		"data/icons/walite-icon.png",
		"data/icons/hicolor/256x256/apps/walite.png",
		"data/pixmaps/walite.png",
		"home/.icons/walite.png",
		"bin/walite-icon.png",
		"bin/assets/walite-icon.png",
		"repository/assets/walite-icon.png",
	} {
		t.Run(location, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "with spaces")
			want := filepath.Join(root, filepath.FromSlash(location))
			installNotificationTestIcon(t, want)
			got := discoverLinuxNotificationIcon(filepath.Join(root, "home"), filepath.Join(root, "bin", "walite"), filepath.Join(root, "repository"), []string{filepath.Join(root, "data")})
			if got != want || !filepath.IsAbs(got) {
				t.Fatalf("icon=%q want=%q", got, want)
			}
		})
	}
}

func TestLinuxNotificationIconMissingInvalidAndRelativeLocations(t *testing.T) {
	root := t.TempDir()
	home, executable, workingDirectory := filepath.Join(root, "home"), filepath.Join(root, "bin", "walite"), filepath.Join(root, "repo")
	data := filepath.Join(root, "data")
	if got := discoverLinuxNotificationIcon(home, executable, workingDirectory, []string{data, "relative"}); got != "" {
		t.Fatalf("missing icon=%q", got)
	}
	bad := filepath.Join(data, "icons", "walite.png")
	if err := os.MkdirAll(filepath.Dir(bad), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("not a PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(data, "icons", "walite-icon.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := discoverLinuxNotificationIcon(home, executable, workingDirectory, []string{data}); got != "" {
		t.Fatalf("invalid icon=%q", got)
	}
	want := filepath.Join(workingDirectory, "assets", "walite-icon.png")
	installNotificationTestIcon(t, want)
	if got := discoverLinuxNotificationIcon(home, executable, workingDirectory, []string{data}); got != want {
		t.Fatalf("invalid icon prevented fallback: %q", got)
	}
}

func TestLinuxNotificationIconUsesXDGUserDefaultsAndPriority(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_DATA_DIRS", filepath.Join(root, "system"))
	defaultIcon := filepath.Join(home, ".local", "share", "icons", "walite.png")
	installNotificationTestIcon(t, defaultIcon)
	if got := defaultLinuxNotificationIcon(); got != defaultIcon {
		t.Fatalf("default icon=%q want=%q", got, defaultIcon)
	}
	customData := filepath.Join(root, "custom data")
	t.Setenv("XDG_DATA_HOME", customData)
	customIcon := filepath.Join(customData, "icons", "hicolor", "512x512", "apps", "walite.png")
	installNotificationTestIcon(t, customIcon)
	installNotificationTestIcon(t, filepath.Join(root, "system", "icons", "walite.png"))
	if got := defaultLinuxNotificationIcon(); got != customIcon {
		t.Fatalf("XDG user priority icon=%q want=%q", got, customIcon)
	}
	t.Setenv("XDG_DATA_HOME", "relative-invalid")
	if got := defaultLinuxNotificationIcon(); got != defaultIcon {
		t.Fatalf("relative XDG location accepted: %q", got)
	}
}

func TestLinuxNotificationIconArgumentsAndDesktopIndependence(t *testing.T) {
	icon := filepath.Join(t.TempDir(), "path with spaces", "walite-icon.png")
	installNotificationTestIcon(t, icon)
	notification := tui.Notification{ChatID: "chat@lid", Title: "--title é 👋", Body: "Synthetic body & 'quotes'"}
	for _, desktop := range []string{"XFCE", "i3"} {
		for _, iconPath := range []string{icon, ""} {
			t.Run(desktop+iconPath, func(t *testing.T) {
				t.Setenv("XDG_CURRENT_DESKTOP", desktop)
				var got []string
				capabilities := selectPlatformCapabilitiesWithOptions("linux", func(name string) (string, error) {
					if name == "notify-send" {
						return "/usr/bin/notify-send", nil
					}
					return "", os.ErrNotExist
				}, platformCapabilityOptions{
					linuxNotificationIconPath: iconPath,
					notificationRunner: func(_ context.Context, command string, args []string) error {
						if command != "/usr/bin/notify-send" {
							t.Fatalf("command=%q", command)
						}
						got = append([]string(nil), args...)
						return nil
					},
				})
				if err := capabilities.notifier.Notify(context.Background(), notification); err != nil {
					t.Fatal(err)
				}
				want := []string{"--app-name=walite"}
				if iconPath != "" {
					want = append(want, "--icon", iconPath)
				}
				want = append(want, "--", notification.Title, notification.Body)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("arguments=%q want=%q", got, want)
				}
			})
		}
	}
	mac := selectPlatformCapabilitiesWithOptions("darwin", darwinToolLookup, platformCapabilityOptions{linuxNotificationIconPath: icon})
	if _, ok := mac.notifier.(darwinNotificationBackend); !ok {
		t.Fatalf("Linux icon affected macOS notifier: %T", mac.notifier)
	}
}
