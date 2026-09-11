package config

import (
	"path/filepath"
)

const whatsAppSessionFilename = "whatsmeow-session.db"
const applicationCacheFilename = "walite-cache.db"

// DefaultWhatsAppSessionPath returns walite's persistent linked-device store
// path. Session credentials are private application data, not disposable cache.
func DefaultWhatsAppSessionPath() (string, error) {
	dataHome, err := DefaultUserDirectories().DataDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, "walite", whatsAppSessionFilename), nil
}

// DefaultApplicationCachePath returns walite's disposable application-cache
// database path, separate from persistent WhatsApp credentials.
func DefaultApplicationCachePath() (string, error) {
	cacheHome, err := DefaultUserDirectories().CacheDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheHome, "walite", applicationCacheFilename), nil
}

// DefaultMediaCachePath returns the private disposable decrypted-media cache.
func DefaultMediaCachePath() (string, error) {
	cacheHome, err := DefaultUserDirectories().CacheDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheHome, "walite", "media"), nil
}

// DefaultMediaSavePath is the user-owned destination for explicit saves.
func DefaultMediaSavePath() (string, error) {
	downloads, err := DefaultUserDirectories().DownloadsDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(downloads, "walite"), nil
}
