package wa

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	_ "modernc.org/sqlite"
)

type whatsmeowConnectionClient struct {
	container *sqlstore.Container
	client    *whatsmeow.Client
	events    chan protocolEvent
	realtime  *RealtimeSource
	now       func() time.Time
	eventMu   sync.Mutex
	handlerID uint32
	closeOnce sync.Once
	closeErr  error
	textReady atomic.Bool
}

func newWhatsmeowConnectionClient(ctx context.Context, sessionPath string) (*whatsmeowConnectionClient, error) {
	if ctx == nil || ctx.Err() != nil {
		var cause error
		if ctx != nil {
			cause = context.Cause(ctx)
		}
		return nil, &connectionError{kind: ErrConnectionRejected, cause: cause}
	}
	path, err := prepareSessionPath(sessionPath)
	if err != nil {
		return nil, &connectionError{kind: ErrConnectionRejected, cause: err}
	}
	databaseURL := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "_pragma=foreign_keys(1)&_pragma=busy_timeout(1000)",
	}).String()
	database, err := sql.Open("sqlite", databaseURL)
	if err != nil {
		return nil, &connectionError{kind: ErrConnectionRejected, cause: err}
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	fail := func(cause error) (*whatsmeowConnectionClient, error) {
		_ = database.Close()
		return nil, &connectionError{kind: ErrConnectionRejected, cause: cause}
	}
	if err := database.PingContext(ctx); err != nil {
		return fail(err)
	}
	container := sqlstore.NewWithDB(database, "sqlite", nil)
	if err := container.Upgrade(ctx); err != nil {
		_ = container.Close()
		return nil, &connectionError{kind: ErrConnectionRejected, cause: err}
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return nil, &connectionError{kind: ErrConnectionRejected, cause: err}
	}
	if err := tightenSessionPath(path); err != nil {
		_ = container.Close()
		return nil, &connectionError{kind: ErrConnectionRejected, cause: err}
	}
	wrapped := &whatsmeowConnectionClient{
		container: container,
		client:    whatsmeow.NewClient(device, nil),
		events:    make(chan protocolEvent, 1),
		realtime:  newRealtimeSource(),
		now:       time.Now,
	}
	wrapped.realtime.lookup = func(ctx context.Context, jid types.JID) (types.JID, error) {
		// Fresh unlinked devices have no initialized sub-stores. Pairing owns
		// initialization; this lookup only reads mappings already available.
		if device.LIDs == nil {
			return types.EmptyJID, nil
		}
		return device.GetAltJID(ctx, jid)
	}
	wrapped.realtime.display = newDisplayResolver(wrapped.realtime.aliases, wrapped.realtime.lookup,
		func(ctx context.Context, jid types.JID) (types.ContactInfo, error) {
			if device.Contacts == nil {
				return types.ContactInfo{}, nil
			}
			return device.Contacts.GetContact(ctx, jid)
		}, func(ctx context.Context, jid types.JID) (string, error) {
			info, err := wrapped.client.GetGroupInfo(ctx, jid)
			if err != nil {
				return "", err
			}
			if info == nil || info.JID != jid {
				return "", errors.New("group metadata identity mismatch")
			}
			return info.Name, nil
		})
	// A nil logger selects whatsmeow's no-op logger. This is mandatory during
	// pairing because the upstream QR helper debug-logs the raw QR token.
	wrapped.handlerID = wrapped.client.AddEventHandler(wrapped.handleEvent)
	return wrapped, nil
}

func (client *whatsmeowConnectionClient) Linked() bool {
	return client != nil && client.client != nil && client.client.Store != nil && client.client.Store.ID != nil
}

func (client *whatsmeowConnectionClient) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	if client == nil || client.client == nil {
		return nil, ErrConnectionClosed
	}
	return client.client.GetQRChannel(ctx)
}

func (client *whatsmeowConnectionClient) Connect(ctx context.Context) error {
	if client == nil || client.client == nil {
		return ErrConnectionClosed
	}
	return client.client.ConnectContext(ctx)
}

func (client *whatsmeowConnectionClient) Events() <-chan protocolEvent {
	if client == nil {
		return nil
	}
	return client.events
}

func (client *whatsmeowConnectionClient) RealtimeSource() *RealtimeSource {
	if client == nil {
		return nil
	}
	return client.realtime
}

func (client *whatsmeowConnectionClient) Disconnect() {
	if client != nil && client.client != nil {
		client.textReady.Store(false)
		client.client.Disconnect()
	}
}

func (client *whatsmeowConnectionClient) Close() error {
	if client == nil {
		return nil
	}
	client.closeOnce.Do(func() {
		if client.realtime != nil {
			client.realtime.closeAdmission()
		}
		if client.client != nil {
			client.client.RemoveEventHandler(client.handlerID)
		}
		client.eventMu.Lock()
		close(client.events)
		client.eventMu.Unlock()
		if client.container != nil {
			client.closeErr = client.container.Close()
		}
	})
	return client.closeErr
}

func (client *whatsmeowConnectionClient) handleEvent(raw any) {
	if client != nil && client.realtime != nil {
		client.realtime.display.observeEvent(raw)
	}
	if message, ok := raw.(*events.Message); ok {
		now := time.Now
		if client != nil && client.now != nil {
			now = client.now
		}
		if event, recognized := adaptTextMessage(message, now().UTC()); recognized && client.realtime != nil {
			client.realtime.display.observeMessage(message.Info)
			client.realtime.admitWithAlternate(event, messageChatAlternate(message.Info))
		}
		return
	}
	var event protocolEvent
	switch raw.(type) {
	case *events.Connected:
		client.textReady.Store(true)
		event.kind = protocolConnected
	case *events.Disconnected:
		client.textReady.Store(false)
		event.kind = protocolDisconnected
	case *events.ConnectFailure:
		client.textReady.Store(false)
		event = protocolEvent{kind: protocolFailed, cause: ErrConnectionFailed}
	case *events.LoggedOut:
		client.textReady.Store(false)
		event = protocolEvent{kind: protocolLoggedOut, cause: ErrConnectionFailed}
	default:
		// History, receipt, media, typing, and all other protocol events are not
		// part of the Milestone 4A realtime text boundary.
		return
	}
	client.eventMu.Lock()
	defer client.eventMu.Unlock()
	select {
	case <-client.events:
	default:
	}
	select {
	case client.events <- event:
	default:
	}
}

func messageChatAlternate(info types.MessageInfo) model.ChatID {
	if info.IsGroup {
		return model.ChatID{}
	}
	primary, err := model.NewChatID(info.Chat.String())
	if err != nil {
		return model.ChatID{}
	}
	if info.IsFromMe {
		return authoritativeAlternate(primary, info.RecipientAlt)
	}
	if info.Sender.ToNonAD() == info.Chat.ToNonAD() {
		return authoritativeAlternate(primary, info.SenderAlt)
	}
	return model.ChatID{}
}

func adaptTextMessage(incoming *events.Message, receivedAt time.Time) (model.Event, bool) {
	if incoming == nil || incoming.Message == nil || incoming.SourceWebMsg != nil ||
		incoming.IsEphemeral || incoming.IsViewOnce || incoming.IsViewOnceV2 ||
		incoming.IsViewOnceV2Extension || incoming.IsDocumentWithCaption ||
		incoming.IsLottieSticker || incoming.IsBotInvoke || incoming.IsEdit ||
		incoming.NewsletterMeta != nil || incoming.Message.GetProtocolMessage() != nil {
		return model.Event{}, false
	}

	text := ""
	if incoming.Message.Conversation != nil {
		text = incoming.Message.GetConversation()
	} else if extended := incoming.Message.GetExtendedTextMessage(); extended != nil && extended.Text != nil {
		text = extended.GetText()
	} else {
		return model.Event{}, false
	}
	if text == "" {
		return model.Event{}, false
	}
	senderID := ""
	group := incoming.Info.Chat.Server == types.GroupServer
	if group && !incoming.Info.Sender.IsEmpty() {
		senderID = incoming.Info.Sender.ToNonAD().String()
	}
	message, err := model.NewMessage(model.MessageInput{
		ChatID:    incoming.Info.Chat.String(),
		MessageID: string(incoming.Info.ID),
		SentAt:    incoming.Info.Timestamp,
		FromMe:    incoming.Info.IsFromMe,
		Text:      text,
		SenderID:  senderID,
		IsGroup:   group,
	})
	if err != nil {
		return model.Event{}, false
	}
	event, err := model.NewEvent(message, receivedAt)
	if err != nil {
		return model.Event{}, false
	}
	return event, true
}

func prepareSessionPath(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", ErrConnectionRejected
	}
	path = filepath.Clean(path)
	directory := filepath.Dir(path)
	if directory == path || filepath.Base(path) == "." {
		return "", ErrConnectionRejected
	}
	if err := rejectSessionSymlinks(directory); err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if err := rejectSessionSymlinks(directory); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
		return "", ErrConnectionRejected
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}

	info, err = os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
			return "", ErrConnectionRejected
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", err
		}
		return path, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func rejectSessionSymlinks(path string) error {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	current := volume + string(os.PathSeparator)
	for _, part := range strings.Split(strings.TrimPrefix(remainder, string(os.PathSeparator)), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrConnectionRejected
		}
	}
	return nil
}

func tightenSessionPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
		return ErrConnectionRejected
	}
	return os.Chmod(path, 0o600)
}

func ownedByCurrentUser(info os.FileInfo) bool {
	status, ok := info.Sys().(*syscall.Stat_t)
	return ok && status.Uid == uint32(os.Geteuid())
}
