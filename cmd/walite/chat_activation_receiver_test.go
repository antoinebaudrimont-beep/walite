package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func activationTestDirectory(t *testing.T) string {
	t.Helper()
	// Keep real socket test paths short even when macOS TMPDIR is very long.
	directory, err := os.MkdirTemp("/tmp", "wa-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(directory); err != nil {
			t.Errorf("test directory cleanup: %v", err)
		}
	})
	return directory
}

func testActivationReceiver(t *testing.T, ctx context.Context, base string) *chatActivationReceiver {
	t.Helper()
	receiver, err := newChatActivationReceiverInDirectory(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(receiver.stop)
	return receiver
}

func activationJSON(t *testing.T, receiver *chatActivationReceiver, id string) []byte {
	t.Helper()
	data, err := json.Marshal(chatActivationCommand{Version: chatActivationVersion, ChatID: id, Token: receiver.token})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// EOF from the server is a deterministic barrier: receive/admission has
// completed before the owner closes the connection. It is not an IPC ack.
func sendActivationBytes(t *testing.T, receiver *chatActivationReceiver, data []byte) {
	t.Helper()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: receiver.socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := connection.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(connection)
	// Immediate size rejection can reset a stream with unread bytes on Linux.
	// That still establishes the owner's connection-close barrier.
	oversizeReset := len(data) > chatActivationMaxRequestBytes && errors.Is(err, syscall.ECONNRESET)
	if err != nil && !oversizeReset || len(response) != 0 {
		t.Fatalf("socket close barrier failed: %v", err)
	}
}

func TestChatActivationReceiverAuthenticatesAndPreservesStableIDs(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	assertMode(t, receiver.directory, 0o700)
	assertMode(t, receiver.socketPath, 0o600)
	if !filepath.IsAbs(receiver.socketPath) || len(receiver.socketPath) > chatActivationMaxSocketPathBytes || len(receiver.token) != 64 {
		t.Fatal("invalid socket or authentication bounds")
	}
	for _, id := range []string{"15550000001@s.whatsapp.net", "987654@lid", "family@g.us", "synthetic-café-👋", strings.Repeat("i", model.MaxIdentifierBytes)} {
		sendActivationBytes(t, receiver, activationJSON(t, receiver, id))
		select {
		case request := <-receiver.activations:
			if request.ChatID != id {
				t.Fatal("stable identity changed")
			}
		default:
			t.Fatal("authenticated activation missing")
		}
	}
}

func TestChatActivationReceiverRejectsInvalidRequests(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	valid := string(activationJSON(t, receiver, "chat@lid"))
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "wrong token", data: []byte(strings.Replace(valid, receiver.token, strings.Repeat("0", 64), 1))},
		{name: "missing token", data: []byte(`{"version":1,"chat_id":"chat@lid"}`)},
		{name: "malformed JSON", data: []byte(`{"version":`)},
		{name: "oversized", data: []byte(valid + strings.Repeat(" ", chatActivationMaxRequestBytes))},
		{name: "empty ChatID", data: activationJSON(t, receiver, "")},
		{name: "blank ChatID", data: activationJSON(t, receiver, "   ")},
		{name: "control ChatID", data: activationJSON(t, receiver, "chat\n@lid")},
		{name: "oversized ChatID", data: activationJSON(t, receiver, strings.Repeat("x", model.MaxIdentifierBytes+1))},
		{name: "invalid UTF8", data: []byte(strings.Replace(valid, "chat@lid", "chat\xff@lid", 1))},
		{name: "wrong version", data: []byte(strings.Replace(valid, `"version":1`, `"version":2`, 1))},
		{name: "null version", data: []byte(strings.Replace(valid, `"version":1`, `"version":null`, 1))},
		{name: "wrong field type", data: []byte(strings.Replace(valid, `"chat_id":"chat@lid"`, `"chat_id":7`, 1))},
		{name: "unknown field", data: []byte(strings.Replace(valid, `"version":1`, `"unknown":true,"version":1`, 1))},
		{name: "duplicate token", data: []byte(strings.Replace(valid, `"version":1`, `"token":"wrong","version":1`, 1))},
		{name: "wrong case", data: []byte(strings.Replace(valid, `"chat_id"`, `"Chat_ID"`, 1))},
		{name: "trailing command", data: []byte(valid + valid)},
		{name: "not an object", data: []byte(`[]`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			sendActivationBytes(t, receiver, test.data)
			if len(receiver.activations) != 0 {
				t.Fatal("invalid activation admitted")
			}
		})
	}
	// Rejections must not poison the receiver for later authenticated requests.
	sendActivationBytes(t, receiver, []byte(" \n"+valid+"\n "))
	if len(receiver.activations) != 1 {
		t.Fatal("receiver stopped after invalid input")
	}
}

func TestChatActivationReceiverInstancesAreIndependentAndRestartRejectsStaleToken(t *testing.T) {
	base := activationTestDirectory(t)
	first := testActivationReceiver(t, context.Background(), base)
	second := testActivationReceiver(t, context.Background(), base)
	if first.socketPath == second.socketPath || first.token == second.token {
		t.Fatal("instance identity reused")
	}
	oldPath := first.socketPath
	stale := activationJSON(t, first, "first@lid")
	sendActivationBytes(t, second, stale)
	if len(second.activations) != 0 {
		t.Fatal("another instance's token accepted")
	}
	sendActivationBytes(t, first, stale)
	if request := <-first.activations; request.ChatID != "first@lid" {
		t.Fatal("wrong first instance identity")
	}
	first.stop()
	if _, err := net.DialTimeout("unix", oldPath, time.Second); err == nil {
		t.Fatal("old endpoint remained reachable")
	}
	sendActivationBytes(t, second, activationJSON(t, second, "second@g.us"))
	if request := <-second.activations; request.ChatID != "second@g.us" {
		t.Fatal("other instance affected by shutdown")
	}
	restarted := testActivationReceiver(t, context.Background(), base)
	if restarted.socketPath == oldPath || restarted.token == first.token {
		t.Fatal("restart reused authentication identity")
	}
	sendActivationBytes(t, restarted, stale)
	if len(restarted.activations) != 0 {
		t.Fatal("stale command admitted after restart")
	}
	sendActivationBytes(t, restarted, activationJSON(t, restarted, "restart@lid"))
	if request := <-restarted.activations; request.ChatID != "restart@lid" {
		t.Fatal("restart activation missing")
	}
}

func TestChatActivationReceiverQueueIsBoundedAndNonblocking(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	if cap(receiver.activations) != 8 {
		t.Fatal("activation queue capacity changed")
	}
	for index := 0; index < chatActivationQueueCapacity+2; index++ {
		sendActivationBytes(t, receiver, activationJSON(t, receiver, "chat@lid"))
	}
	if len(receiver.activations) != chatActivationQueueCapacity {
		t.Fatal("queue overflowed")
	}
	<-receiver.activations
	sendActivationBytes(t, receiver, activationJSON(t, receiver, "newest@g.us"))
	for index := 0; index < chatActivationQueueCapacity-1; index++ {
		<-receiver.activations
	}
	if request := <-receiver.activations; request.ChatID != "newest@g.us" {
		t.Fatal("receiver blocked after queue saturation")
	}
	// Shutdown must discard cosmetic queued requests, not wait for a consumer.
	sendActivationBytes(t, receiver, activationJSON(t, receiver, "abandoned@lid"))
	receiver.stop()
	if _, open := <-receiver.activations; open {
		t.Fatal("shutdown retained queued activations")
	}
}

func TestChatActivationReceiverCancellationStopsIdleAndBlockedReads(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "blocked reader"}[blocked], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			receiver := testActivationReceiver(t, ctx, activationTestDirectory(t))
			if blocked {
				connection, err := net.Dial("unix", receiver.socketPath)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				if _, err := connection.Write([]byte(`{"version":`)); err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			select {
			case <-receiver.done:
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not stop receiver")
			}
			var stops sync.WaitGroup
			for index := 0; index < 4; index++ {
				stops.Add(1)
				go func() { defer stops.Done(); receiver.stop() }()
			}
			stops.Wait()
			for _, path := range []string{receiver.socketPath, receiver.directory} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("owned socket/directory not cleaned up")
				}
			}
		})
	}
}

func TestChatActivationReceiverReadDeadlineRejectsUnfinishedCommand(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	connection, err := net.Dial("unix", receiver.socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// Without EOF even a complete object must not be admitted.
	if _, err := connection.Write(activationJSON(t, receiver, "unfinished@lid")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(connection); err != nil {
		t.Fatalf("read deadline did not release connection: %v", err)
	}
	if len(receiver.activations) != 0 {
		t.Fatal("unfinished stream accepted")
	}
	sendActivationBytes(t, receiver, activationJSON(t, receiver, "after-timeout@lid"))
	if len(receiver.activations) != 1 {
		t.Fatal("stalled client prevented subsequent activation")
	}
}

func TestChatActivationReceiverCleanupPreservesReplacedSocket(t *testing.T) {
	receiver := testActivationReceiver(t, context.Background(), activationTestDirectory(t))
	if err := os.Remove(receiver.socketPath); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: receiver.socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	replacement.SetUnlinkOnClose(false)
	defer replacement.Close()
	info, err := os.Lstat(receiver.socketPath)
	if err != nil {
		t.Fatal(err)
	}
	receiver.stop()
	current, err := os.Lstat(receiver.socketPath)
	if err != nil || !os.SameFile(current, info) {
		t.Fatal("shutdown removed another owner's socket")
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(receiver.socketPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(receiver.directory); err != nil {
		t.Fatal(err)
	}
}

func TestChatActivationReceiverRejectsCanceledContextAndLongPaths(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newChatActivationReceiverInDirectory(ctx, "/tmp"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error=%v", err)
	}
	if _, err := newChatActivationReceiverInDirectory(context.Background(), "/tmp/"+strings.Repeat("x", chatActivationMaxSocketPathBytes)); err == nil {
		t.Fatal("overlong Darwin socket path accepted")
	}
	if _, err := newChatActivationReceiverInDirectory(context.Background(), "relative"); err == nil {
		t.Fatal("relative socket base accepted")
	}
}
