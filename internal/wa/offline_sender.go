package wa

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// OfflineTextSender deterministically owns outgoing IDs and obtains timestamps
// from an injected clock function. It performs no network operation.
type OfflineTextSender struct {
	mu     sync.Mutex
	now    func() time.Time
	nextID uint64
}

// NewOfflineTextSender constructs the offline-only B3 text transport.
func NewOfflineTextSender(now func() time.Time) (*OfflineTextSender, error) {
	if now == nil {
		return nil, errors.New("offline text sender rejected")
	}
	return &OfflineTextSender{now: now}, nil
}

// SendText returns one outgoing normalized event with transport-owned ID/time.
func (sender *OfflineTextSender) SendText(ctx context.Context, chatID model.ChatID, text string, quotes ...model.TextQuote) (model.Event, error) {
	if len(quotes) != 0 {
		return model.Event{}, errors.New("offline quoted replies unavailable")
	}
	if sender == nil || ctx == nil {
		return model.Event{}, errors.New("offline text send rejected")
	}
	if err := ctx.Err(); err != nil {
		return model.Event{}, err
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return model.Event{}, err
	}
	sentAt := sender.now()
	if sentAt.IsZero() {
		return model.Event{}, errors.New("offline text send rejected")
	}
	sender.nextID++
	message, err := model.NewMessage(model.MessageInput{
		ChatID: chatID.String(), MessageID: fmt.Sprintf("offline-outgoing-%06d", sender.nextID),
		SentAt: sentAt, FromMe: true, Text: text,
	})
	if err != nil {
		return model.Event{}, errors.New("offline text send rejected")
	}
	event, err := model.NewEvent(message, sentAt)
	if err != nil {
		return model.Event{}, errors.New("offline text send rejected")
	}
	return event, nil
}
