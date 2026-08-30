package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultWhatsAppSessionPathUsesXDGDataHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)

	got, err := DefaultWhatsAppSessionPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "walite", "whatsmeow-session.db")
	if got != want {
		t.Fatalf("DefaultWhatsAppSessionPath()=%q want=%q", got, want)
	}
}

func TestDefaultWhatsAppSessionPathUsesPrivateDataFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")

	got, err := DefaultWhatsAppSessionPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "share", "walite", "whatsmeow-session.db")
	if got != want {
		t.Fatalf("DefaultWhatsAppSessionPath()=%q want=%q", got, want)
	}
}

func TestDefaultWhatsAppSessionPathRejectsRelativeDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "relative")
	if _, err := DefaultWhatsAppSessionPath(); err == nil {
		t.Fatal("relative XDG_DATA_HOME accepted")
	}
}
