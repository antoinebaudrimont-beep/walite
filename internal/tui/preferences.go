package tui

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const maxPreferencesBytes = 4 * 1024

type preferencesFile struct {
	RecentEmoji []string `json:"recentEmoji"`
}

func defaultPreferencesPath() (string, error) {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDirectory, "walite", "preferences.json"), nil
}

func loadEmojiPreferences(path string, picker *emojiPickerState) error {
	if path == "" || picker == nil {
		return errors.New("invalid preferences target")
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxPreferencesBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxPreferencesBytes {
		return errors.New("preferences too large")
	}
	var preferences preferencesFile
	if err := json.Unmarshal(data, &preferences); err != nil {
		return err
	}

	clear(picker.recent[:])
	picker.recentCount = 0
	for _, value := range preferences.RecentEmoji {
		if picker.recentCount == maxRecentEmoji {
			break
		}
		if !validEmojiPreference(value) || containsRecent(picker, value) {
			continue
		}
		picker.recent[picker.recentCount] = value
		picker.recentCount++
	}
	picker.recentCursor = 0
	picker.clamp()
	return nil
}

func saveEmojiPreferences(path string, picker *emojiPickerState) error {
	if path == "" || picker == nil {
		return errors.New("invalid preferences target")
	}
	picker.clamp()
	preferences := preferencesFile{
		RecentEmoji: make([]string, 0, picker.recentCount),
	}
	for index := 0; index < picker.recentCount; index++ {
		value := picker.recent[index]
		if validEmojiPreference(value) && !containsString(preferences.RecentEmoji, value) {
			preferences.RecentEmoji = append(preferences.RecentEmoji, value)
		}
	}
	data, err := json.MarshalIndent(preferences, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".preferences-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func containsRecent(picker *emojiPickerState, value string) bool {
	for index := 0; index < picker.recentCount; index++ {
		if picker.recent[index] == value {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
