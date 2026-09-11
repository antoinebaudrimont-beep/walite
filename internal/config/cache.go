package config

import "path/filepath"

// DefaultCachePath returns walite's XDG application-cache database path.
func DefaultCachePath() (string, error) {
	directory, err := DefaultUserDirectories().CacheDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "walite", "walite-cache.db"), nil
}
