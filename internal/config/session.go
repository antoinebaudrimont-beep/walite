package config

import (
	"errors"
	"os"
	"path/filepath"
)

const whatsAppSessionFilename = "whatsmeow-session.db"

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
