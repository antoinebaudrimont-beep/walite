package config

import (
	"errors"
	"os"
	"path/filepath"
)

const whatsAppSessionFilename = "whatsmeow-session.db"
const applicationCacheFilename = "walite-cache.db"

// DefaultWhatsAppSessionPath returns walite's persistent linked-device store
// path. Session credentials are private application data, not disposable cache.
func DefaultWhatsAppSessionPath() (string, error) {
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
	return filepath.Join(filepath.Clean(dataHome), "walite", whatsAppSessionFilename), nil
}

// DefaultApplicationCachePath returns walite's disposable application-cache
// database path, separate from persistent WhatsApp credentials.
func DefaultApplicationCachePath() (string, error) {
	cacheHome := os.Getenv("XDG_CACHE_HOME")
	if cacheHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		cacheHome = filepath.Join(home, ".cache")
	}
	if !filepath.IsAbs(cacheHome) {
		return "", errors.New("invalid cache directory")
	}
	return filepath.Join(filepath.Clean(cacheHome), "walite", applicationCacheFilename), nil
}

// DefaultMediaCachePath returns the private disposable decrypted-media cache.
func DefaultMediaCachePath() (string, error) {
	cacheHome := os.Getenv("XDG_CACHE_HOME")
	if cacheHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		cacheHome = filepath.Join(home, ".cache")
	}
	if !filepath.IsAbs(cacheHome) {
		return "", errors.New("invalid cache directory")
	}
	return filepath.Join(filepath.Clean(cacheHome), "walite", "media"), nil
}

// DefaultMediaSavePath is the user-owned destination for explicit saves.
func DefaultMediaSavePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", errors.New("invalid home directory")
	}
	return filepath.Join(filepath.Clean(home), "Downloads", "walite"), nil
}
