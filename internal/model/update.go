package model

const (
	// MaxNormalizedUpdateBytes is the hard conservative charge for one update.
	MaxNormalizedUpdateBytes = 32 * 1024

	normalizedUpdateEnvelopeBytes = 256
)

// UpdateKind is a closed observable-update category.
type UpdateKind uint8

const (
	UpdateReady UpdateKind = iota + 1
	UpdateLive
	UpdateHistory
	UpdateSummary
	UpdateDegraded
	UpdateStopping
	UpdateStopped
	UpdateFatal
)

// UpdateInput contains only bounded scalar metadata accepted by NewUpdate.
type UpdateInput struct {
	Kind                UpdateKind
	ChatID              ChatID
	MessageID           MessageID
	Accepted, Discarded uint64
	Degraded            bool
}

// Update is an immutable, body-free observable application update.
type Update struct {
	kind      UpdateKind
	chatID    ChatID
	messageID MessageID
	accepted  uint64
	discarded uint64
	degraded  bool
	byteSize  int
}

// NewUpdate validates the update kind and identifier relationship, takes fresh
// ownership of any identifiers, and applies the conservative update charge.
// Live updates require both chat and message IDs. Other kinds may omit both;
// any supplied message ID always requires its chat ID.
func NewUpdate(input UpdateInput) (Update, error) {
	if err := validateUpdateKind(input.Kind); err != nil {
		return Update{}, err
	}

	chatValue := input.ChatID.String()
	messageValue := input.MessageID.String()
	if input.Kind == UpdateLive && chatValue == "" {
		return Update{}, newValidationError(Required, "chat_id", 0)
	}
	if input.Kind == UpdateLive && messageValue == "" {
		return Update{}, newValidationError(Required, "message_id", 0)
	}
	if messageValue != "" && chatValue == "" {
		return Update{}, newValidationError(Required, "chat_id", 0)
	}

	var chatID ChatID
	var messageID MessageID
	var err error
	if chatValue != "" {
		chatID, err = NewChatID(chatValue)
		if err != nil {
			return Update{}, err
		}
	}
	if messageValue != "" {
		messageID, err = NewMessageID(messageValue)
		if err != nil {
			return Update{}, err
		}
	}

	byteSize, err := normalizedUpdateByteSize(len(chatID.value), len(messageID.value))
	if err != nil {
		return Update{}, err
	}

	return Update{
		kind:      input.Kind,
		chatID:    chatID,
		messageID: messageID,
		accepted:  input.Accepted,
		discarded: input.Discarded,
		degraded:  input.Degraded,
		byteSize:  byteSize,
	}, nil
}

// Kind returns the update category.
func (update Update) Kind() UpdateKind {
	return update.kind
}

// ChatID returns the optional chat identifier. Its zero value means absent.
func (update Update) ChatID() ChatID {
	return update.chatID
}

// MessageID returns the optional message identifier. Its zero value means absent.
func (update Update) MessageID() MessageID {
	return update.messageID
}

// Accepted returns the fixed-size accepted-item count.
func (update Update) Accepted() uint64 {
	return update.accepted
}

// Discarded returns the fixed-size discarded-item count.
func (update Update) Discarded() uint64 {
	return update.discarded
}

// Degraded reports whether this update observes degraded completeness.
func (update Update) Degraded() bool {
	return update.degraded
}

// ByteSize returns the update's conservative normalized byte charge.
func (update Update) ByteSize() int {
	return update.byteSize
}

func validateUpdateKind(kind UpdateKind) *ValidationError {
	if kind == 0 {
		return newValidationError(Required, "update_kind", 0)
	}
	switch kind {
	case UpdateReady,
		UpdateLive,
		UpdateHistory,
		UpdateSummary,
		UpdateDegraded,
		UpdateStopping,
		UpdateStopped,
		UpdateFatal:
		return nil
	default:
		return newValidationError(InvalidValue, "update_kind", 0)
	}
}

func normalizedUpdateByteSize(chatIDBytes, messageIDBytes int) (int, error) {
	byteSize, ok := checkedByteSum(
		normalizedUpdateEnvelopeBytes,
		chatIDBytes,
		messageIDBytes,
	)
	if !ok || byteSize > MaxNormalizedUpdateBytes {
		return 0, newValidationError(SizeExceeded, "update", MaxNormalizedUpdateBytes)
	}
	return byteSize, nil
}
