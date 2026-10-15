package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

const (
	chatActivationVersion         = 1
	chatActivationQueueCapacity   = 8
	chatActivationTicketCapacity  = 32
	chatActivationMaxRequestBytes = 4096
	chatActivationReadTimeout     = time.Second
	// Darwin sockaddr_un has 104 bytes, including the terminating NUL.
	chatActivationMaxSocketPathBytes = 103
)

type chatActivationCommand struct {
	Version      int    `json:"version"`
	ChatID       string `json:"chat_id,omitempty"`
	Token        string `json:"token,omitempty"`
	ActivationID string `json:"activation_id,omitempty"`
}

// chatActivationReceiver is dormant until explicitly constructed. It owns one
// accept/read goroutine, one connection at a time, and a cosmetic bounded queue.
// Clients write one JSON object and close their write side (EOF framing). There
// is no response or delivery acknowledgement. Full queues drop authenticated
// commands. Shutdown cancels reads and discards pending activations.
type chatActivationReceiver struct {
	socketPath    string
	token         string
	directory     string
	socketInfo    os.FileInfo
	directoryInfo os.FileInfo
	listener      *net.UnixListener
	activations   chan tui.ChatActivationRequest
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan struct{}
	mu            sync.Mutex
	connection    *net.UnixConn
	tickets       map[string]string
	ticketOrder   []string
	transportOnce sync.Once
}

func newChatActivationReceiver(parent context.Context) (*chatActivationReceiver, error) {
	// macOS TMPDIR and home paths can already exceed the Unix socket limit.
	// Mkdir creates a private random Walite directory in the short system path.
	return newChatActivationReceiverInDirectory(parent, "/tmp")
}

func newChatActivationReceiverInDirectory(parent context.Context, baseDirectory string) (*chatActivationReceiver, error) {
	if parent == nil || !filepath.IsAbs(baseDirectory) {
		return nil, errors.New("chat activation receiver rejected")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	var random [48]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errors.New("chat activation randomness unavailable")
	}
	directory := filepath.Join(baseDirectory, "walite-activation-"+hex.EncodeToString(random[:8]))
	socketPath := filepath.Join(directory, "s-"+hex.EncodeToString(random[8:16])+".sock")
	if len(socketPath) > chatActivationMaxSocketPathBytes {
		return nil, errors.New("chat activation socket path too long")
	}
	// Exclusive directory creation rejects collisions and existing symlinks.
	if err := os.Mkdir(directory, 0o700); err != nil {
		return nil, errors.New("create private chat activation directory")
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(directory)
		}
	}()
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, errors.New("protect chat activation directory")
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, errors.New("inspect chat activation directory")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, errors.New("listen on chat activation socket")
	}
	// Automatic unlink would delete a replacement socket owned by another
	// instance. Cleanup below checks the filesystem identity instead.
	listener.SetUnlinkOnClose(false)
	socketInfo, err := os.Lstat(socketPath)
	if err != nil {
		_ = listener.Close()
		return nil, errors.New("inspect chat activation socket")
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		removeOwnedActivationPath(socketPath, socketInfo)
		return nil, errors.New("protect chat activation socket")
	}
	ctx, cancel := context.WithCancel(parent)
	receiver := &chatActivationReceiver{
		socketPath: socketPath, token: hex.EncodeToString(random[16:]), directory: directory,
		socketInfo: socketInfo, directoryInfo: directoryInfo, listener: listener,
		activations: make(chan tui.ChatActivationRequest, chatActivationQueueCapacity),
		tickets:     make(map[string]string, chatActivationTicketCapacity),
		ctx:         ctx, cancel: cancel, done: make(chan struct{}),
	}
	context.AfterFunc(ctx, receiver.closeTransport)
	keep = true
	go receiver.run()
	return receiver, nil
}

func (receiver *chatActivationReceiver) run() {
	defer func() {
		receiver.cancel()
		receiver.closeTransport()
		removeOwnedActivationPath(receiver.socketPath, receiver.socketInfo)
		removeOwnedActivationPath(receiver.directory, receiver.directoryInfo)
		receiver.mu.Lock()
		defer receiver.mu.Unlock()
		for {
			select {
			case <-receiver.activations:
			default:
				close(receiver.activations)
				close(receiver.done)
				return
			}
		}
	}()
	for receiver.ctx.Err() == nil {
		connection, err := receiver.listener.AcceptUnix()
		if err != nil {
			return
		}
		receiver.mu.Lock()
		if receiver.ctx.Err() != nil {
			receiver.mu.Unlock()
			_ = connection.Close()
			return
		}
		receiver.connection = connection
		receiver.mu.Unlock()
		receiver.receive(connection)
		_ = connection.Close()
		receiver.mu.Lock()
		receiver.connection = nil
		receiver.mu.Unlock()
	}
}

func (receiver *chatActivationReceiver) receive(connection *net.UnixConn) {
	if err := connection.SetReadDeadline(time.Now().Add(chatActivationReadTimeout)); err != nil {
		return
	}
	data, err := io.ReadAll(io.LimitReader(connection, chatActivationMaxRequestBytes+1))
	if err != nil || len(data) > chatActivationMaxRequestBytes || !utf8.Valid(data) {
		return
	}
	command, ok := decodeChatActivationCommand(data)
	if !ok {
		return
	}
	if command.ActivationID != "" {
		chatID, found := receiver.consumeTicket(command.ActivationID)
		if !found {
			return
		}
		receiver.admit(tui.ChatActivationRequest{ChatID: chatID})
		return
	}
	if subtle.ConstantTimeCompare([]byte(command.Token), []byte(receiver.token)) != 1 {
		return
	}
	receiver.admitChatID(command.ChatID)
}

func (receiver *chatActivationReceiver) admitChatID(chatID string) bool {
	if strings.TrimSpace(chatID) == "" {
		return false
	}
	id, err := model.NewChatID(chatID)
	if err != nil {
		return false
	}
	return receiver.admit(tui.ChatActivationRequest{ChatID: id.String()})
}

func (receiver *chatActivationReceiver) admit(request tui.ChatActivationRequest) bool {
	if receiver == nil {
		return false
	}
	// Admission and cancellation are serialized; the socket owner alone closes
	// activations after it has stopped producing, avoiding send-on-closed races.
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if receiver.ctx.Err() != nil {
		return false
	}
	select {
	case receiver.activations <- request:
		return true
	default:
		return false
	}
}

// issueTicket creates a bounded, one-time capability for one notification.
// Neither the stable WhatsApp ChatID nor the receiver-wide authentication
// token leaves Walite through the desktop notification service.
func (receiver *chatActivationReceiver) issueTicket(chatID string) (string, bool) {
	if receiver == nil || strings.TrimSpace(chatID) == "" {
		return "", false
	}
	id, err := model.NewChatID(chatID)
	if err != nil {
		return "", false
	}
	for attempt := 0; attempt < 4; attempt++ {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", false
		}
		ticket := hex.EncodeToString(random[:])
		receiver.mu.Lock()
		if receiver.ctx.Err() != nil {
			receiver.mu.Unlock()
			return "", false
		}
		if _, exists := receiver.tickets[ticket]; exists {
			receiver.mu.Unlock()
			continue
		}
		if len(receiver.ticketOrder) == chatActivationTicketCapacity {
			delete(receiver.tickets, receiver.ticketOrder[0])
			copy(receiver.ticketOrder, receiver.ticketOrder[1:])
			receiver.ticketOrder = receiver.ticketOrder[:len(receiver.ticketOrder)-1]
		}
		receiver.tickets[ticket] = id.String()
		receiver.ticketOrder = append(receiver.ticketOrder, ticket)
		receiver.mu.Unlock()
		return ticket, true
	}
	return "", false
}

func (receiver *chatActivationReceiver) consumeTicket(ticket string) (string, bool) {
	if len(ticket) != 64 {
		return "", false
	}
	if _, err := hex.DecodeString(ticket); err != nil {
		return "", false
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if receiver.ctx.Err() != nil {
		return "", false
	}
	chatID, ok := receiver.tickets[ticket]
	if !ok {
		return "", false
	}
	delete(receiver.tickets, ticket)
	for index, value := range receiver.ticketOrder {
		if subtle.ConstantTimeCompare([]byte(value), []byte(ticket)) == 1 {
			copy(receiver.ticketOrder[index:], receiver.ticketOrder[index+1:])
			receiver.ticketOrder = receiver.ticketOrder[:len(receiver.ticketOrder)-1]
			break
		}
	}
	return chatID, true
}

func decodeChatActivationCommand(data []byte) (chatActivationCommand, bool) {
	var command chatActivationCommand
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return command, false
	}
	// Read exact case-sensitive keys and reject duplicates, rather than letting
	// encoding/json silently overwrite authentication or identity fields.
	var seen [4]bool
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return command, false
		}
		var index int
		var target any
		switch key {
		case "version":
			index, target = 0, &command.Version
		case "chat_id":
			index, target = 1, &command.ChatID
		case "token":
			index, target = 2, &command.Token
		case "activation_id":
			index, target = 3, &command.ActivationID
		default:
			return command, false
		}
		if seen[index] || decoder.Decode(target) != nil {
			return command, false
		}
		seen[index] = true
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return command, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return command, false
	}
	if !seen[0] || command.Version != chatActivationVersion {
		return command, false
	}
	legacy := seen[1] && seen[2] && !seen[3]
	ticket := !seen[1] && !seen[2] && seen[3]
	return command, legacy || ticket
}

func (receiver *chatActivationReceiver) closeTransport() {
	receiver.transportOnce.Do(func() {
		receiver.mu.Lock()
		defer receiver.mu.Unlock()
		_ = receiver.listener.Close()
		if receiver.connection != nil {
			_ = receiver.connection.Close()
		}
	})
}

func (receiver *chatActivationReceiver) stop() {
	if receiver == nil {
		return
	}
	receiver.cancel()
	receiver.closeTransport()
	<-receiver.done
}

func removeOwnedActivationPath(path string, owned os.FileInfo) {
	current, err := os.Lstat(path)
	if err == nil && os.SameFile(current, owned) {
		// Nonrecursive: unrelated files and replacement sockets are preserved.
		_ = os.Remove(path)
	}
}
