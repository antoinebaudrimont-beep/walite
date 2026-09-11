package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var ErrUnsupportedUserDirectories = errors.New("user directories unsupported on this platform")

// UserDirectories separates private configuration, disposable cache,
// persistent application data, and user-owned downloads. Platform policy is
// selected here rather than in application or TUI code.
type UserDirectories interface {
	ConfigDirectory() (string, error)
	CacheDirectory() (string, error)
	DataDirectory() (string, error)
	DownloadsDirectory() (string, error)
}

// DefaultUserDirectories returns the current host's directory policy. v0.5A
// preserves Linux XDG behavior; non-Linux data/download policy remains an
// explicit v0.5B concern instead of inheriting Linux paths accidentally.
func DefaultUserDirectories() UserDirectories {
	return systemUserDirectories{goos: runtime.GOOS}
}

type systemUserDirectories struct{ goos string }

func (directories systemUserDirectories) ConfigDirectory() (string, error) {
	return os.UserConfigDir()
}

func (directories systemUserDirectories) CacheDirectory() (string, error) {
	return os.UserCacheDir()
}

func (directories systemUserDirectories) DataDirectory() (string, error) {
	if directories.goos != "linux" {
		return "", ErrUnsupportedUserDirectories
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	if !filepath.IsAbs(dataHome) {
		return "", errors.New("invalid data directory")
	}
	return filepath.Clean(dataHome), nil
}

func (directories systemUserDirectories) DownloadsDirectory() (string, error) {
	if directories.goos != "linux" {
		return "", ErrUnsupportedUserDirectories
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", errors.New("invalid home directory")
	}
	return filepath.Join(filepath.Clean(home), "Downloads"), nil
}
