package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	currentUIVersion = 1
	maxUIConfigBytes = 64 * 1024
)

var ErrInvalidUI = errors.New("invalid UI configuration")

// Theme identifies a supported terminal color theme.
type Theme string

const (
	ThemeTerminal     Theme = "terminal"
	ThemeDark         Theme = "dark"
	ThemeLight        Theme = "light"
	ThemeHighContrast Theme = "high_contrast"
	ThemeDefault            = ThemeTerminal
)

// UI contains user-configurable terminal preferences. It does not contain
// chat data, emoji recents, or transient rendering state.
type UI struct {
	Theme            Theme
	ShowTimestamps   bool
	ConfirmQuit      bool
	SendReadReceipts bool
}

// UIStore separates configuration consumers from its storage format.
type UIStore interface {
	Load() (UI, error)
	Save(UI) error
}

// UIFileStore persists UI configuration in one private local file.
type UIFileStore struct {
	path       string
	renameFile func(string, string) error
}

type uiFile struct {
	Version          int   `json:"version"`
	Theme            Theme `json:"theme"`
	ShowTimestamps   bool  `json:"show_timestamps"`
	ConfirmQuit      bool  `json:"confirm_quit"`
	SendReadReceipts bool  `json:"send_read_receipts"`
}

// DefaultUI returns the initial user-facing configuration.
func DefaultUI() UI {
	return UI{
		Theme:            ThemeDefault,
		ShowTimestamps:   true,
		ConfirmQuit:      false,
		SendReadReceipts: true,
	}
}

// Validate rejects unsupported user-facing configuration values.
func (settings UI) Validate() error {
	if !validTheme(settings.Theme) {
		return fmt.Errorf("%w: unsupported theme %q", ErrInvalidUI, settings.Theme)
	}
	return nil
}

func validTheme(theme Theme) bool {
	return theme == ThemeTerminal || theme == ThemeDark || theme == ThemeLight || theme == ThemeHighContrast
}

func normalizeLoadedTheme(theme Theme) Theme {
	if theme == "default" || !validTheme(theme) {
		return ThemeTerminal
	}
	return theme
}

// DefaultUIPath returns the user configuration file location.
func DefaultUIPath() (string, error) {
	directory, err := DefaultUserDirectories().ConfigDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "walite", "config.json"), nil
}

// NewDefaultUIStore constructs the default file-backed UI configuration store.
func NewDefaultUIStore() (UIStore, error) {
	path, err := DefaultUIPath()
	if err != nil {
		return nil, err
	}
	return NewUIFileStore(path), nil
}

// NewUIFileStore constructs a JSON-backed UI configuration store at path.
func NewUIFileStore(path string) UIStore {
	return &UIFileStore{path: path, renameFile: os.Rename}
}

// Load creates and returns defaults when the configuration file does not exist.
func (store *UIFileStore) Load() (UI, error) {
	if store == nil || store.path == "" {
		return UI{}, errors.New("invalid UI configuration path")
	}
	file, err := os.Open(store.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			defaults := DefaultUI()
			if err := store.Save(defaults); err != nil {
				return UI{}, fmt.Errorf("create default UI configuration: %w", err)
			}
			return defaults, nil
		}
		return UI{}, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUIConfigBytes+1))
	if err != nil {
		return UI{}, err
	}
	if len(data) > maxUIConfigBytes {
		return UI{}, fmt.Errorf("%w: file exceeds %d bytes", ErrInvalidUI, maxUIConfigBytes)
	}
	defaults := DefaultUI()
	saved := uiFile{
		Theme:            defaults.Theme,
		ShowTimestamps:   defaults.ShowTimestamps,
		ConfirmQuit:      defaults.ConfirmQuit,
		SendReadReceipts: defaults.SendReadReceipts,
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		return UI{}, fmt.Errorf("%w: %v", ErrInvalidUI, err)
	}
	if saved.Version != currentUIVersion {
		return UI{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidUI, saved.Version)
	}
	saved.Theme = normalizeLoadedTheme(saved.Theme)
	settings := UI{
		Theme:            saved.Theme,
		ShowTimestamps:   saved.ShowTimestamps,
		ConfirmQuit:      saved.ConfirmQuit,
		SendReadReceipts: saved.SendReadReceipts,
	}
	if err := settings.Validate(); err != nil {
		return UI{}, err
	}
	return settings, nil
}

// Save validates and atomically replaces the stored UI configuration.
func (store *UIFileStore) Save(settings UI) error {
	if store == nil || store.path == "" {
		return errors.New("invalid UI configuration path")
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	saved := uiFile{
		Version:          currentUIVersion,
		Theme:            settings.Theme,
		ShowTimestamps:   settings.ShowTimestamps,
		ConfirmQuit:      settings.ConfirmQuit,
		SendReadReceipts: settings.SendReadReceipts,
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	renameFile := store.renameFile
	if renameFile == nil {
		renameFile = os.Rename
	}
	return writeAtomicUIFile(store.path, data, renameFile)
}

func writeAtomicUIFile(path string, data []byte, renameFile func(string, string) error) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".config-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := renameFile(temporaryPath, path); err != nil {
		return err
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryFile.Close()
	return directoryFile.Sync()
}
