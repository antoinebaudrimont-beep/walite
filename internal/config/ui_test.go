package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestMissingUIConfigLoadsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "config.json")
	store := NewUIFileStore(path)
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := DefaultUI(); got != want {
		t.Fatalf("Load()=%+v want=%+v", got, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("created config: %v", err)
	}
}

func TestCreatedDefaultUIConfigContainsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walite", "config.json")
	if _, err := NewUIFileStore(path).Load(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved uiFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	defaults := DefaultUI()
	if saved.Version != currentUIVersion || saved.Theme != defaults.Theme ||
		saved.ShowTimestamps != defaults.ShowTimestamps || saved.ConfirmQuit != defaults.ConfirmQuit {
		t.Fatalf("created config=%+v defaults=%+v", saved, defaults)
	}
}

func TestCreatedDefaultUIConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not supported")
	}
	directory := filepath.Join(t.TempDir(), "walite")
	path := filepath.Join(directory, "config.json")
	if _, err := NewUIFileStore(path).Load(); err != nil {
		t.Fatal(err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory permissions=%#o", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file permissions=%#o", got)
	}
}

func TestValidUIConfigLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "version": 1,
  "theme": "default",
  "show_timestamps": false,
  "confirm_quit": true
}
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := NewUIFileStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	want := UI{Theme: ThemeDefault, ShowTimestamps: false, ConfirmQuit: true}
	if got != want {
		t.Fatalf("Load()=%+v want=%+v", got, want)
	}
}

func TestInvalidUIConfigJSONReturnsControlledError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"theme":`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewUIFileStore(path).Load()
	if !errors.Is(err, ErrInvalidUI) {
		t.Fatalf("Load error=%v", err)
	}
}

func TestInvalidUIConfigValuesReturnControlledError(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "unsupported theme",
			data: `{"version":1,"theme":"neon","show_timestamps":true,"confirm_quit":false}`,
		},
		{
			name: "unsupported version",
			data: `{"version":2,"theme":"default","show_timestamps":true,"confirm_quit":false}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := NewUIFileStore(path).Load()
			if !errors.Is(err, ErrInvalidUI) {
				t.Fatalf("Load error=%v", err)
			}
		})
	}
}

func TestSaveUIConfigCreatesPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not supported")
	}
	directory := filepath.Join(t.TempDir(), "walite")
	path := filepath.Join(directory, "config.json")
	settings := UI{Theme: ThemeDefault, ShowTimestamps: false, ConfirmQuit: true}
	store := NewUIFileStore(path)
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != settings {
		t.Fatalf("Load()=%+v want=%+v", loaded, settings)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory permissions=%#o", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file permissions=%#o", got)
	}
}

func TestSaveUIConfigIsAtomic(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "walite")
	path := filepath.Join(directory, "config.json")
	store := NewUIFileStore(path)
	if err := store.Save(DefaultUI()); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	renameErr := errors.New("synthetic rename failure")
	failingStore := &UIFileStore{
		path: path,
		renameFile: func(string, string) error {
			return renameErr
		},
	}
	changed := DefaultUI()
	changed.ConfirmQuit = true
	if err := failingStore.Save(changed); !errors.Is(err, renameErr) {
		t.Fatalf("Save error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("failed atomic replacement changed the existing config")
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(directory, ".config-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary files left behind: %v", temporaryFiles)
	}
}
