package wa

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	waEvents "go.mau.fi/whatsmeow/types/events"
)

func TestWhatsmeowSessionStoreCreatesPrivateUnlinkedDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "whatsmeow-session.db")
	client, err := newWhatsmeowConnectionClient(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if client.Linked() {
		t.Fatal("new device unexpectedly linked")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close=%v", err)
	}

	assertPrivateMode(t, filepath.Dir(path), 0o700)
	assertPrivateMode(t, path, 0o600)
}

func TestSessionLinkedClosesUnlinkedStoreBeforeReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "whatsmeow-session.db")
	linked, err := SessionLinked(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if linked {
		t.Fatal("new session unexpectedly linked")
	}
	client, err := newWhatsmeowConnectionClient(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen after probe: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWhatsmeowEventAdapterIgnoresMessages(t *testing.T) {
	client := &whatsmeowConnectionClient{events: make(chan protocolEvent, 1)}
	client.handleEvent(&waEvents.Message{})
	if got := len(client.events); got != 0 {
		t.Fatalf("message produced %d connection events", got)
	}
	client.handleEvent(&waEvents.Connected{})
	if got := len(client.events); got != 1 {
		t.Fatalf("connected produced %d events", got)
	}
}

func TestWhatsmeowSessionStoreRejectsSymlinkPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission test")
	}
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := newWhatsmeowConnectionClient(context.Background(), filepath.Join(link, "session.db")); err == nil {
		t.Fatal("symlinked session path accepted")
	}
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode=%#o want %#o", path, got, want)
	}
}
