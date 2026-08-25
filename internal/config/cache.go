package config

import (
	"os"
	"path/filepath"
)

// DefaultCachePath returns walite's XDG application-cache database path.
func DefaultCachePath() (string, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "walite", "walite-cache.db"), nil
}
