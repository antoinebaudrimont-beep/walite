package model

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"
)

func TestChatConstructionAndAccessors(t *testing.T) {
	t.Parallel()

	chat, err := NewChat(ChatInput{
		ID:          "chat-a",
		DisplayName: "Person A",
		Placeholder: true,
	})
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}
	if got := chat.ID().String(); got != "chat-a" {
		t.Errorf("Chat.ID() = %q, want chat-a", got)
	}
	if got := chat.DisplayName(); got != "Person A" {
		t.Errorf("Chat.DisplayName() = %q, want Person A", got)
	}
	if chat.NameTruncated() {
		t.Error("Chat.NameTruncated() = true, want false")
	}
	if !chat.Placeholder() {
		t.Error("Chat.Placeholder() = false, want true")
	}
}

func TestChatDisplayNameBoundsAndNormalization(t *testing.T) {
	t.Parallel()

	exact := strings.Repeat("n", MaxChatDisplayNameBytes)
	chat, err := NewChat(ChatInput{ID: "chat-a", DisplayName: exact})
	if err != nil {
		t.Fatalf("NewChat(exact name): %v", err)
	}
	if chat.NameTruncated() || chat.DisplayName() != exact {
		t.Fatalf("exact name = (%d bytes, truncated %t)", len(chat.DisplayName()), chat.NameTruncated())
	}
	if unsafe.StringData(chat.DisplayName()) == unsafe.StringData(exact) {
		t.Fatal("Chat retained exact display-name backing storage")
	}

	crossing := strings.Repeat("a", MaxChatDisplayNameBytes-1) + "€"
	chat, err = NewChat(ChatInput{ID: "chat-a", DisplayName: crossing})
	if err != nil {
		t.Fatalf("NewChat(crossing name): %v", err)
	}
	if !chat.NameTruncated() {
		t.Fatal("crossing multibyte name was not marked truncated")
	}
	if got := len(chat.DisplayName()); got != MaxChatDisplayNameBytes-1 {
		t.Fatalf("crossing name length = %d, want %d", got, MaxChatDisplayNameBytes-1)
	}
	if !utf8.ValidString(chat.DisplayName()) || strings.ContainsRune(chat.DisplayName(), '€') {
		t.Fatalf("crossing name is not a valid rune-boundary prefix: %q", chat.DisplayName())
	}

	malformed := string([]byte{'a', 0xff, 'b'})
	chat, err = NewChat(ChatInput{ID: "chat-a", DisplayName: malformed})
	if err != nil {
		t.Fatalf("NewChat(malformed name): %v", err)
	}
	if got, want := chat.DisplayName(), "a\uFFFDb"; got != want {
		t.Fatalf("malformed name = %q, want %q", got, want)
	}
	if chat.NameTruncated() {
		t.Fatal("short malformed name was incorrectly marked truncated")
	}

	huge := strings.Repeat("x", 1<<20)
	chat, err = NewChat(ChatInput{ID: "chat-a", DisplayName: huge})
	if err != nil {
		t.Fatalf("NewChat(huge name): %v", err)
	}
	if !chat.NameTruncated() || len(chat.DisplayName()) != MaxChatDisplayNameBytes {
		t.Fatalf("huge normalized name = %d bytes, truncated %t", len(chat.DisplayName()), chat.NameTruncated())
	}
	if unsafe.StringData(chat.DisplayName()) == unsafe.StringData(huge) {
		t.Fatal("Chat retained the huge source backing storage")
	}
}

func TestChatOwnsInputAndRejectsInvalidID(t *testing.T) {
	t.Parallel()

	idBytes := []byte("chat-a")
	nameBytes := []byte("Person A")
	chat, err := NewChat(ChatInput{
		ID:          mutableString(idBytes),
		DisplayName: mutableString(nameBytes),
	})
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}
	idBytes[0] = 'X'
	nameBytes[0] = 'X'
	if chat.ID().String() != "chat-a" || chat.DisplayName() != "Person A" {
		t.Fatalf("input mutation changed Chat: ID=%q name=%q", chat.ID().String(), chat.DisplayName())
	}

	_, err = NewChat(ChatInput{})
	requireValidationError(t, err, Required, "chat_id", 0)
	private := "private\nchat"
	_, err = NewChat(ChatInput{ID: private})
	validationErr := requireValidationError(
		t,
		err,
		ControlCharacter,
		"chat_id",
		MaxIdentifierBytes,
	)
	if strings.Contains(validationErr.Error(), private) || strings.Contains(validationErr.Error(), "private") {
		t.Fatalf("validation error disclosed chat ID: %q", validationErr.Error())
	}
}

func TestMessageWithoutBodyIsImmutable(t *testing.T) {
	t.Parallel()

	sentAt := time.Unix(100, 0).UTC()
	message := storeTestMessage(t, "message-a", "retained text", true, sentAt)
	originalBytes := message.ByteSize()
	withoutBody := message.WithoutBody()

	if message.Text() != "retained text" || !message.BodyRetained() || message.ByteSize() != originalBytes {
		t.Fatalf("WithoutBody mutated original: text=%q retained=%t bytes=%d", message.Text(), message.BodyRetained(), message.ByteSize())
	}
	if withoutBody.Text() != "" || withoutBody.BodyRetained() {
		t.Fatalf("WithoutBody result: text=%q retained=%t", withoutBody.Text(), withoutBody.BodyRetained())
	}
	if withoutBody.ChatID() != message.ChatID() ||
		withoutBody.MessageID() != message.MessageID() ||
		!withoutBody.SentAt().Equal(message.SentAt()) ||
		withoutBody.FromMe() != message.FromMe() {
		t.Fatal("WithoutBody changed identity, time, or direction")
	}
	if got, want := withoutBody.ByteSize(), len("chat-a")+len("message-a"); got != want {
		t.Fatalf("WithoutBody.ByteSize() = %d, want %d", got, want)
	}
}

func TestCursorContract(t *testing.T) {
	t.Parallel()

	none := NoCursor()
	if !none.IsZero() || !none.SentAt().IsZero() || none.MessageID().String() != "" {
		t.Fatalf("NoCursor = (%t, %v, %q)", none.IsZero(), none.SentAt(), none.MessageID().String())
	}

	idBytes := []byte("message-a")
	sentAt := time.Unix(200, 0).UTC()
	cursor, err := NewCursor(sentAt, MessageID{value: mutableString(idBytes)})
	if err != nil {
		t.Fatalf("NewCursor: %v", err)
	}
	idBytes[0] = 'X'
	if cursor.IsZero() || !cursor.SentAt().Equal(sentAt) || cursor.MessageID().String() != "message-a" {
		t.Fatalf("Cursor = (%t, %v, %q)", cursor.IsZero(), cursor.SentAt(), cursor.MessageID().String())
	}

	messageID, err := NewMessageID("message-a")
	if err != nil {
		t.Fatalf("NewMessageID: %v", err)
	}
	_, err = NewCursor(time.Time{}, messageID)
	requireValidationError(t, err, Required, "sent_at", 0)
	_, err = NewCursor(sentAt, MessageID{})
	requireValidationError(t, err, Required, "message_id", 0)
	_, err = NewCursor(sentAt, MessageID{value: "message\na"})
	requireValidationError(t, err, ControlCharacter, "message_id", MaxIdentifierBytes)

	partial := Cursor{sentAt: sentAt}
	if partial.IsZero() {
		t.Fatal("partially initialized Cursor was mistaken for NoCursor")
	}
}

func TestMessageAnchorValidationAndOwnership(t *testing.T) {
	t.Parallel()

	chatBytes := []byte("chat-a")
	messageBytes := []byte("message-a")
	sentAt := time.Unix(300, 0).UTC()
	anchor, err := NewMessageAnchor(
		ChatID{value: mutableString(chatBytes)},
		MessageID{value: mutableString(messageBytes)},
		sentAt,
		true,
	)
	if err != nil {
		t.Fatalf("NewMessageAnchor: %v", err)
	}
	chatBytes[0] = 'X'
	messageBytes[0] = 'X'
	if anchor.ChatID().String() != "chat-a" ||
		anchor.MessageID().String() != "message-a" ||
		!anchor.SentAt().Equal(sentAt) || !anchor.FromMe() {
		t.Fatalf("MessageAnchor = (%q, %q, %v, %t)", anchor.ChatID().String(), anchor.MessageID().String(), anchor.SentAt(), anchor.FromMe())
	}

	validChat, _ := NewChatID("chat-a")
	validMessage, _ := NewMessageID("message-a")
	_, err = NewMessageAnchor(ChatID{}, validMessage, sentAt, false)
	requireValidationError(t, err, Required, "chat_id", 0)
	_, err = NewMessageAnchor(validChat, MessageID{}, sentAt, false)
	requireValidationError(t, err, Required, "message_id", 0)
	_, err = NewMessageAnchor(validChat, validMessage, time.Time{}, false)
	requireValidationError(t, err, Required, "sent_at", 0)
}

func TestWriteOriginsAndEmptyBatches(t *testing.T) {
	t.Parallel()

	for _, origin := range []WriteOrigin{WriteRealtime, WriteHistory} {
		batch, err := NewWriteBatch(origin, nil)
		if err != nil {
			t.Fatalf("NewWriteBatch(origin %d): %v", origin, err)
		}
		if batch.Origin() != origin || batch.Len() != 0 || batch.ByteSize() != 0 {
			t.Fatalf("empty batch = origin %d len %d bytes %d", batch.Origin(), batch.Len(), batch.ByteSize())
		}
		if _, ok := batch.At(0); ok {
			t.Fatal("empty WriteBatch.At(0) reported a value")
		}
	}
	requireValidationError(t, writeBatchError(0, nil), Required, "write_origin", 0)
	requireValidationError(t, writeBatchError(WriteOrigin(255), nil), InvalidValue, "write_origin", 0)
}

func TestWriteBatchBoundsCopyAndAccounting(t *testing.T) {
	t.Parallel()

	messages := make([]Message, MaxWriteBatchMessages)
	wantBytes := 0
	for index := range messages {
		messages[index] = storeTestMessage(
			t,
			"message-"+twoDigit(index),
			"text",
			index%2 == 0,
			time.Unix(int64(index+1), 0).UTC(),
		)
		wantBytes += messages[index].ByteSize() + writeBatchRecordBytes
	}
	first := messages[0]
	batch, err := NewWriteBatch(WriteRealtime, messages)
	if err != nil {
		t.Fatalf("NewWriteBatch(50): %v", err)
	}
	if batch.Len() != MaxWriteBatchMessages || batch.ByteSize() != wantBytes {
		t.Fatalf("batch = len %d bytes %d, want %d/%d", batch.Len(), batch.ByteSize(), MaxWriteBatchMessages, wantBytes)
	}
	messages[0] = storeTestMessage(t, "message-other", "other", false, time.Unix(999, 0))
	got, ok := batch.At(0)
	if !ok || got.MessageID() != first.MessageID() || got.Text() != first.Text() {
		t.Fatalf("source mutation changed batch: (%q, %q, %t)", got.MessageID().String(), got.Text(), ok)
	}
	got.text = "mutated returned copy"
	again, _ := batch.At(0)
	if again.Text() != first.Text() {
		t.Fatal("mutating At result changed WriteBatch")
	}
	for _, index := range []int{-1, MaxWriteBatchMessages} {
		if _, ok := batch.At(index); ok {
			t.Errorf("WriteBatch.At(%d) reported a value", index)
		}
	}

	tooMany := append(messages, messages[0])
	_, err = NewWriteBatch(WriteHistory, tooMany)
	requireValidationError(t, err, SizeExceeded, "write_batch_messages", MaxWriteBatchMessages)
	_, err = NewWriteBatch(WriteHistory, []Message{{}})
	requireValidationError(t, err, Required, "chat_id", 0)
}

func TestWriteBatchAccountingOverflow(t *testing.T) {
	t.Parallel()

	exact, err := writeBatchByteSize([]int{maxWriteBatchBytes - writeBatchRecordBytes})
	if err != nil || exact != maxWriteBatchBytes {
		t.Fatalf("writeBatchByteSize(exact) = %d, %v", exact, err)
	}
	const maxInt = int(^uint(0) >> 1)
	for _, charge := range []int{-1, maxInt} {
		_, err := writeBatchByteSize([]int{charge})
		requireValidationError(t, err, SizeExceeded, "write_batch", maxWriteBatchBytes)
	}
}

func TestMetadataLimitKindIsClosed(t *testing.T) {
	t.Parallel()

	if MetadataChatLimit == 0 || validateMetadataLimitKind(MetadataChatLimit) != nil {
		t.Fatal("MetadataChatLimit is not the sole valid nonzero kind")
	}
	requireValidationError(t, validateMetadataLimitKind(0), Required, "metadata_limit_kind", 0)
	requireValidationError(
		t,
		validateMetadataLimitKind(MetadataLimitKind(255)),
		InvalidValue,
		"metadata_limit_kind",
		0,
	)
}

func storeTestMessage(
	t *testing.T,
	messageID string,
	text string,
	fromMe bool,
	sentAt time.Time,
) Message {
	t.Helper()
	message, err := NewMessage(MessageInput{
		ChatID:    "chat-a",
		MessageID: messageID,
		SentAt:    sentAt,
		FromMe:    fromMe,
		Text:      text,
	})
	if err != nil {
		t.Fatalf("NewMessage(%q): %v", messageID, err)
	}
	return message
}

func writeBatchError(origin WriteOrigin, messages []Message) error {
	_, err := NewWriteBatch(origin, messages)
	return err
}

func twoDigit(value int) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}
