package model

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"
)

func TestValidValues(t *testing.T) {
	t.Parallel()

	sentAt := time.Date(2026, time.August, 3, 10, 11, 12, 0, time.UTC)
	receivedAt := sentAt.Add(time.Second)

	chatID, err := NewChatID("chat-a")
	if err != nil {
		t.Fatalf("NewChatID: %v", err)
	}
	if got := chatID.String(); got != "chat-a" {
		t.Fatalf("ChatID.String() = %q, want %q", got, "chat-a")
	}

	messageID, err := NewMessageID("message-a")
	if err != nil {
		t.Fatalf("NewMessageID: %v", err)
	}
	if got := messageID.String(); got != "message-a" {
		t.Fatalf("MessageID.String() = %q, want %q", got, "message-a")
	}

	message, err := NewMessage(MessageInput{
		ChatID:    "chat-a",
		MessageID: "message-a",
		SentAt:    sentAt,
		FromMe:    true,
		Text:      "hello, 世界",
	})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if got := message.ChatID().String(); got != "chat-a" {
		t.Errorf("Message.ChatID() = %q, want %q", got, "chat-a")
	}
	if got := message.MessageID().String(); got != "message-a" {
		t.Errorf("Message.MessageID() = %q, want %q", got, "message-a")
	}
	if got := message.SentAt(); !got.Equal(sentAt) {
		t.Errorf("Message.SentAt() = %v, want %v", got, sentAt)
	}
	if !message.FromMe() {
		t.Error("Message.FromMe() = false, want true")
	}
	if got := message.Text(); got != "hello, 世界" {
		t.Errorf("Message.Text() = %q, want %q", got, "hello, 世界")
	}
	if message.BodyTruncated() {
		t.Error("Message.BodyTruncated() = true, want false")
	}
	if !message.BodyRetained() {
		t.Error("Message.BodyRetained() = false, want true")
	}
	wantMessageBytes := len("chat-a") + len("message-a") + len("hello, 世界")
	if got := message.ByteSize(); got != wantMessageBytes {
		t.Errorf("Message.ByteSize() = %d, want %d", got, wantMessageBytes)
	}

	event, err := NewEvent(message, receivedAt)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if got := event.Message(); got.Text() != message.Text() {
		t.Errorf("Event.Message().Text() = %q, want %q", got.Text(), message.Text())
	}
	if got := event.ReceivedAt(); !got.Equal(receivedAt) {
		t.Errorf("Event.ReceivedAt() = %v, want %v", got, receivedAt)
	}
	if got, want := event.ByteSize(), normalizedEventEnvelopeBytes+wantMessageBytes; got != want {
		t.Errorf("Event.ByteSize() = %d, want %d", got, want)
	}
}

func TestIdentifierLimits(t *testing.T) {
	t.Parallel()

	exact := strings.Repeat("i", MaxIdentifierBytes)
	chatID, err := NewChatID(exact)
	if err != nil {
		t.Fatalf("NewChatID(exact limit): %v", err)
	}
	if got := len(chatID.String()); got != MaxIdentifierBytes {
		t.Fatalf("len(ChatID) = %d, want %d", got, MaxIdentifierBytes)
	}
	messageID, err := NewMessageID(exact)
	if err != nil {
		t.Fatalf("NewMessageID(exact limit): %v", err)
	}
	if got := len(messageID.String()); got != MaxIdentifierBytes {
		t.Fatalf("len(MessageID) = %d, want %d", got, MaxIdentifierBytes)
	}

	tests := []struct {
		name      string
		construct func(string) error
		value     string
		code      ValidationCode
		field     string
		limit     int
	}{
		{
			name: "empty chat ID",
			construct: func(value string) error {
				_, err := NewChatID(value)
				return err
			},
			code:  Required,
			field: "chat_id",
		},
		{
			name: "empty message ID",
			construct: func(value string) error {
				_, err := NewMessageID(value)
				return err
			},
			code:  Required,
			field: "message_id",
		},
		{
			name: "oversized chat ID",
			construct: func(value string) error {
				_, err := NewChatID(value)
				return err
			},
			value: strings.Repeat("c", MaxIdentifierBytes+1),
			code:  TooLong,
			field: "chat_id",
			limit: MaxIdentifierBytes,
		},
		{
			name: "oversized message ID",
			construct: func(value string) error {
				_, err := NewMessageID(value)
				return err
			},
			value: strings.Repeat("m", MaxIdentifierBytes+1),
			code:  TooLong,
			field: "message_id",
			limit: MaxIdentifierBytes,
		},
		{
			name: "malformed UTF-8 chat ID",
			construct: func(value string) error {
				_, err := NewChatID(value)
				return err
			},
			value: string([]byte{'c', 0xff}),
			code:  InvalidUTF8,
			field: "chat_id",
			limit: MaxIdentifierBytes,
		},
		{
			name: "malformed UTF-8 message ID",
			construct: func(value string) error {
				_, err := NewMessageID(value)
				return err
			},
			value: string([]byte{'m', 0xff}),
			code:  InvalidUTF8,
			field: "message_id",
			limit: MaxIdentifierBytes,
		},
		{
			name: "NUL in chat ID",
			construct: func(value string) error {
				_, err := NewChatID(value)
				return err
			},
			value: "private\x00chat",
			code:  ControlCharacter,
			field: "chat_id",
			limit: MaxIdentifierBytes,
		},
		{
			name: "control in message ID",
			construct: func(value string) error {
				_, err := NewMessageID(value)
				return err
			},
			value: "private\nmessage",
			code:  ControlCharacter,
			field: "message_id",
			limit: MaxIdentifierBytes,
		},
		{
			name: "Unicode control in chat ID",
			construct: func(value string) error {
				_, err := NewChatID(value)
				return err
			},
			value: "private\u0085chat",
			code:  ControlCharacter,
			field: "chat_id",
			limit: MaxIdentifierBytes,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.construct(test.value)
			validationErr := requireValidationError(
				t,
				err,
				test.code,
				test.field,
				test.limit,
			)
			if test.value != "" && strings.Contains(validationErr.Error(), test.value) {
				t.Fatalf("validation error disclosed rejected value: %q", validationErr.Error())
			}
		})
	}
}

func TestNormalizeTextExactLimit(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("x", MaxRetainedTextBytes)
	text, truncated := NormalizeText(input)
	if truncated {
		t.Fatal("NormalizeText(exact limit) reported truncation")
	}
	if text != input {
		t.Fatal("NormalizeText(exact limit) changed text")
	}
	if got := len(text); got != MaxRetainedTextBytes {
		t.Fatalf("len(text) = %d, want %d", got, MaxRetainedTextBytes)
	}
	if unsafe.StringData(text) == unsafe.StringData(input) {
		t.Fatal("NormalizeText retained the input backing storage")
	}

	message, err := NewMessage(MessageInput{
		ChatID:    "chat-a",
		MessageID: "message-a",
		SentAt:    time.Unix(1, 0),
		Text:      input,
	})
	if err != nil {
		t.Fatalf("NewMessage(exact text limit): %v", err)
	}
	if message.BodyTruncated() {
		t.Fatal("Message.BodyTruncated() = true at exact text limit")
	}
	if got := len(message.Text()); got != MaxRetainedTextBytes {
		t.Fatalf("len(Message.Text()) = %d, want %d", got, MaxRetainedTextBytes)
	}
}

func TestNormalizeTextMultibyteBoundary(t *testing.T) {
	t.Parallel()

	exact := strings.Repeat("a", MaxRetainedTextBytes-len("€")) + "€"
	text, truncated := NormalizeText(exact)
	if truncated || text != exact {
		t.Fatalf("exact multibyte text = (%q, %t), want unchanged", text, truncated)
	}

	crossing := strings.Repeat("a", MaxRetainedTextBytes-1) + "€"
	text, truncated = NormalizeText(crossing)
	if !truncated {
		t.Fatal("NormalizeText(crossing rune) did not report truncation")
	}
	if !utf8.ValidString(text) {
		t.Fatal("NormalizeText(crossing rune) returned malformed UTF-8")
	}
	if got, want := len(text), MaxRetainedTextBytes-1; got != want {
		t.Fatalf("len(text) = %d, want %d", got, want)
	}
	if strings.ContainsRune(text, '€') {
		t.Fatal("NormalizeText retained a partial or over-limit rune")
	}
}

func TestNormalizeTextMalformedUTF8(t *testing.T) {
	t.Parallel()

	input := string([]byte{'a', 0xff, 'b'})
	text, truncated := NormalizeText(input)
	if truncated {
		t.Fatal("NormalizeText(malformed short input) reported truncation")
	}
	if got, want := text, "a\uFFFDb"; got != want {
		t.Fatalf("NormalizeText(malformed) = %q, want %q", got, want)
	}
	if !utf8.ValidString(text) {
		t.Fatal("NormalizeText(malformed) returned malformed UTF-8")
	}

	nearLimit := strings.Repeat("a", MaxRetainedTextBytes-2) + string([]byte{0xff})
	text, truncated = NormalizeText(nearLimit)
	if !truncated {
		t.Fatal("NormalizeText(over-limit replacement) did not report truncation")
	}
	if got, want := len(text), MaxRetainedTextBytes-2; got != want {
		t.Fatalf("len(text) = %d, want %d", got, want)
	}
	if !utf8.ValidString(text) {
		t.Fatal("NormalizeText(over-limit replacement) returned malformed UTF-8")
	}
}

func TestZeroTimestamps(t *testing.T) {
	t.Parallel()

	_, err := NewMessage(MessageInput{
		ChatID:    "chat-a",
		MessageID: "message-a",
	})
	requireValidationError(t, err, Required, "sent_at", 0)

	message, err := NewMessage(MessageInput{
		ChatID:    "chat-a",
		MessageID: "message-a",
		SentAt:    time.Unix(1, 0),
	})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	_, err = NewEvent(message, time.Time{})
	requireValidationError(t, err, Required, "received_at", 0)
}

func TestHugeTextInputsRemainBounded(t *testing.T) {
	t.Parallel()

	for _, size := range []int{1 << 20, 8 << 20, 32 << 20} {
		t.Run(byteCountName(size), func(t *testing.T) {
			input := strings.Repeat("x", size)
			message, err := NewMessage(MessageInput{
				ChatID:    "chat-a",
				MessageID: "message-a",
				SentAt:    time.Unix(1, 0),
				Text:      input,
			})
			if err != nil {
				t.Fatalf("NewMessage(%d-byte text): %v", size, err)
			}
			if !message.BodyTruncated() {
				t.Fatal("Message.BodyTruncated() = false, want true")
			}
			if got := len(message.Text()); got != MaxRetainedTextBytes {
				t.Fatalf("len(Message.Text()) = %d, want %d", got, MaxRetainedTextBytes)
			}
			if unsafe.StringData(message.Text()) == unsafe.StringData(input) {
				t.Fatal("message retained the huge input backing storage")
			}
			if !utf8.ValidString(message.Text()) {
				t.Fatal("Message.Text() is not valid UTF-8")
			}

			event, err := NewEvent(message, time.Unix(2, 0))
			if err != nil {
				t.Fatalf("NewEvent: %v", err)
			}
			if event.ByteSize() > MaxNormalizedEventBytes {
				t.Fatalf(
					"Event.ByteSize() = %d, exceeds %d",
					event.ByteSize(),
					MaxNormalizedEventBytes,
				)
			}
		})
	}
}

func TestImmutableOwnership(t *testing.T) {
	t.Parallel()

	chatBytes := []byte("chat-a")
	messageBytes := []byte("message-a")
	textBytes := []byte("private text")
	input := MessageInput{
		ChatID:    mutableString(chatBytes),
		MessageID: mutableString(messageBytes),
		SentAt:    time.Unix(1, 0),
		Text:      mutableString(textBytes),
	}

	message, err := NewMessage(input)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	event, err := NewEvent(message, time.Unix(2, 0))
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}

	chatBytes[0] = 'X'
	messageBytes[0] = 'X'
	textBytes[0] = 'X'
	input.ChatID = "changed-chat"
	input.MessageID = "changed-message"
	input.Text = "changed text"

	assertOwnedMessage(t, message)
	assertOwnedMessage(t, event.Message())
}

func TestEventByteSizeAccounting(t *testing.T) {
	t.Parallel()

	message, err := NewMessage(MessageInput{
		ChatID:    strings.Repeat("c", MaxIdentifierBytes),
		MessageID: strings.Repeat("m", MaxIdentifierBytes),
		SentAt:    time.Unix(1, 0),
		Text:      strings.Repeat("t", MaxRetainedTextBytes),
	})
	if err != nil {
		t.Fatalf("NewMessage(maximum fields): %v", err)
	}
	wantMessageBytes := 2*MaxIdentifierBytes + MaxRetainedTextBytes
	if got := message.ByteSize(); got != wantMessageBytes {
		t.Fatalf("Message.ByteSize() = %d, want %d", got, wantMessageBytes)
	}
	event, err := NewEvent(message, time.Unix(2, 0))
	if err != nil {
		t.Fatalf("NewEvent(maximum fields): %v", err)
	}
	wantEventBytes := normalizedEventEnvelopeBytes + wantMessageBytes
	if got := event.ByteSize(); got != wantEventBytes {
		t.Fatalf("Event.ByteSize() = %d, want %d", got, wantEventBytes)
	}
	if event.ByteSize() >= MaxNormalizedEventBytes {
		t.Fatalf(
			"legal maximum event charge = %d, want below %d",
			event.ByteSize(),
			MaxNormalizedEventBytes,
		)
	}

	exactPayloadBytes := MaxNormalizedEventBytes - normalizedEventEnvelopeBytes
	exact, err := normalizedEventByteSize(exactPayloadBytes)
	if err != nil {
		t.Fatalf("normalizedEventByteSize(exact limit): %v", err)
	}
	if exact != MaxNormalizedEventBytes {
		t.Fatalf("exact event charge = %d, want %d", exact, MaxNormalizedEventBytes)
	}

	_, err = normalizedEventByteSize(exactPayloadBytes + 1)
	requireValidationError(
		t,
		err,
		SizeExceeded,
		"event",
		MaxNormalizedEventBytes,
	)

	const maxInt = int(^uint(0) >> 1)
	_, err = normalizedEventByteSize(maxInt)
	requireValidationError(
		t,
		err,
		SizeExceeded,
		"event",
		MaxNormalizedEventBytes,
	)
	if _, ok := checkedByteSum(maxInt, 1); ok {
		t.Fatal("checkedByteSum accepted integer overflow")
	}
}

func TestValidationErrorDoesNotContainPrivateContent(t *testing.T) {
	t.Parallel()

	privateID := "do-not-log\nprivate-id"
	_, err := NewChatID(privateID)
	validationErr := requireValidationError(
		t,
		err,
		ControlCharacter,
		"chat_id",
		MaxIdentifierBytes,
	)
	if strings.Contains(validationErr.Error(), privateID) ||
		strings.Contains(validationErr.Error(), "do-not-log") {
		t.Fatalf("ValidationError disclosed private input: %q", validationErr.Error())
	}
}

func requireValidationError(
	t *testing.T,
	err error,
	wantCode ValidationCode,
	wantField string,
	wantLimit int,
) *ValidationError {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want *ValidationError")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if validationErr.Code != wantCode {
		t.Errorf("ValidationError.Code = %q, want %q", validationErr.Code, wantCode)
	}
	if validationErr.Field != wantField {
		t.Errorf("ValidationError.Field = %q, want %q", validationErr.Field, wantField)
	}
	if validationErr.Limit != wantLimit {
		t.Errorf("ValidationError.Limit = %d, want %d", validationErr.Limit, wantLimit)
	}
	return validationErr
}

func mutableString(value []byte) string {
	return unsafe.String(unsafe.SliceData(value), len(value))
}

func assertOwnedMessage(t *testing.T, message Message) {
	t.Helper()
	if got := message.ChatID().String(); got != "chat-a" {
		t.Errorf("Message.ChatID() after input mutation = %q, want %q", got, "chat-a")
	}
	if got := message.MessageID().String(); got != "message-a" {
		t.Errorf(
			"Message.MessageID() after input mutation = %q, want %q",
			got,
			"message-a",
		)
	}
	if got := message.Text(); got != "private text" {
		t.Errorf("Message.Text() after input mutation = %q, want %q", got, "private text")
	}
}

func byteCountName(size int) string {
	switch size {
	case 1 << 20:
		return "1_MiB"
	case 8 << 20:
		return "8_MiB"
	case 32 << 20:
		return "32_MiB"
	default:
		return "other"
	}
}
