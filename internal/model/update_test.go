package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestUpdateKinds(t *testing.T) {
	t.Parallel()

	chatID, messageID := updateTestIDs(t)
	kinds := []UpdateKind{
		UpdateReady,
		UpdateLive,
		UpdateHistory,
		UpdateSummary,
		UpdateDegraded,
		UpdateStopping,
		UpdateStopped,
		UpdateFatal,
	}
	for _, kind := range kinds {
		input := UpdateInput{Kind: kind}
		if kind == UpdateLive {
			input.ChatID = chatID
			input.MessageID = messageID
		}
		update, err := NewUpdate(input)
		if err != nil {
			t.Fatalf("NewUpdate(kind %d): %v", kind, err)
		}
		if got := update.Kind(); got != kind {
			t.Errorf("Update.Kind() = %d, want %d", got, kind)
		}
	}
}

func TestUpdateRejectsInvalidKinds(t *testing.T) {
	t.Parallel()

	requireValidationError(
		t,
		newUpdateError(UpdateInput{}),
		Required,
		"update_kind",
		0,
	)
	requireValidationError(
		t,
		newUpdateError(UpdateInput{Kind: UpdateKind(255)}),
		InvalidValue,
		"update_kind",
		0,
	)
}

func TestUpdateIdentifierRules(t *testing.T) {
	t.Parallel()

	chatID, messageID := updateTestIDs(t)
	tests := []struct {
		name  string
		input UpdateInput
		field string
	}{
		{
			name:  "live requires chat ID",
			input: UpdateInput{Kind: UpdateLive, MessageID: messageID},
			field: "chat_id",
		},
		{
			name:  "live requires message ID",
			input: UpdateInput{Kind: UpdateLive, ChatID: chatID},
			field: "message_id",
		},
		{
			name:  "message ID always requires chat ID",
			input: UpdateInput{Kind: UpdateHistory, MessageID: messageID},
			field: "chat_id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireValidationError(
				t,
				newUpdateError(test.input),
				Required,
				test.field,
				0,
			)
		})
	}

	optional := []UpdateInput{
		{Kind: UpdateReady},
		{Kind: UpdateHistory, ChatID: chatID},
		{Kind: UpdateSummary, ChatID: chatID, MessageID: messageID},
	}
	for _, input := range optional {
		if _, err := NewUpdate(input); err != nil {
			t.Errorf("NewUpdate(optional IDs for kind %d): %v", input.Kind, err)
		}
	}
}

func TestUpdateAccessors(t *testing.T) {
	t.Parallel()

	chatID, messageID := updateTestIDs(t)
	update, err := NewUpdate(UpdateInput{
		Kind:      UpdateLive,
		ChatID:    chatID,
		MessageID: messageID,
		Accepted:  17,
		Discarded: 23,
		Degraded:  true,
	})
	if err != nil {
		t.Fatalf("NewUpdate: %v", err)
	}
	if got := update.ChatID().String(); got != "chat-a" {
		t.Errorf("Update.ChatID() = %q, want %q", got, "chat-a")
	}
	if got := update.MessageID().String(); got != "message-a" {
		t.Errorf("Update.MessageID() = %q, want %q", got, "message-a")
	}
	if got := update.Accepted(); got != 17 {
		t.Errorf("Update.Accepted() = %d, want 17", got)
	}
	if got := update.Discarded(); got != 23 {
		t.Errorf("Update.Discarded() = %d, want 23", got)
	}
	if !update.Degraded() {
		t.Error("Update.Degraded() = false, want true")
	}
}

func TestUpdateOwnsIdentifiers(t *testing.T) {
	t.Parallel()

	chatBytes := []byte("chat-a")
	messageBytes := []byte("message-a")
	update, err := NewUpdate(UpdateInput{
		Kind:      UpdateLive,
		ChatID:    ChatID{value: mutableString(chatBytes)},
		MessageID: MessageID{value: mutableString(messageBytes)},
	})
	if err != nil {
		t.Fatalf("NewUpdate: %v", err)
	}
	chatBytes[0] = 'X'
	messageBytes[0] = 'X'

	if got := update.ChatID().String(); got != "chat-a" {
		t.Fatalf("Update.ChatID() after mutation = %q, want %q", got, "chat-a")
	}
	if got := update.MessageID().String(); got != "message-a" {
		t.Fatalf("Update.MessageID() after mutation = %q, want %q", got, "message-a")
	}
}

func TestUpdateAPICarriesNoBodyOrText(t *testing.T) {
	t.Parallel()

	inputType := reflect.TypeOf(UpdateInput{})
	wantInputFields := []string{
		"Kind",
		"ChatID",
		"MessageID",
		"Accepted",
		"Discarded",
		"Degraded",
	}
	if inputType.NumField() != len(wantInputFields) {
		t.Fatalf(
			"UpdateInput field count = %d, want %d",
			inputType.NumField(),
			len(wantInputFields),
		)
	}
	for index, want := range wantInputFields {
		if got := inputType.Field(index).Name; got != want {
			t.Errorf("UpdateInput field %d = %q, want %q", index, got, want)
		}
	}

	for _, value := range []any{UpdateInput{}, Update{}} {
		typeOf := reflect.TypeOf(value)
		for index := 0; index < typeOf.NumField(); index++ {
			field := typeOf.Field(index)
			name := strings.ToLower(field.Name)
			if strings.Contains(name, "text") || strings.Contains(name, "body") {
				t.Errorf("%s exposes body/text field %q", typeOf.Name(), field.Name)
			}
		}
		for index := 0; index < typeOf.NumMethod(); index++ {
			method := typeOf.Method(index)
			name := strings.ToLower(method.Name)
			if strings.Contains(name, "text") || strings.Contains(name, "body") {
				t.Errorf("%s exposes body/text method %q", typeOf.Name(), method.Name)
			}
		}
	}
}

func TestUpdateByteSizeAccounting(t *testing.T) {
	t.Parallel()

	chatID, err := NewChatID(strings.Repeat("c", MaxIdentifierBytes))
	if err != nil {
		t.Fatalf("NewChatID(maximum): %v", err)
	}
	messageID, err := NewMessageID(strings.Repeat("m", MaxIdentifierBytes))
	if err != nil {
		t.Fatalf("NewMessageID(maximum): %v", err)
	}
	update, err := NewUpdate(UpdateInput{
		Kind:      UpdateLive,
		ChatID:    chatID,
		MessageID: messageID,
	})
	if err != nil {
		t.Fatalf("NewUpdate(maximum IDs): %v", err)
	}
	want := normalizedUpdateEnvelopeBytes + 2*MaxIdentifierBytes
	if got := update.ByteSize(); got != want {
		t.Fatalf("Update.ByteSize() = %d, want %d", got, want)
	}
	if update.ByteSize() > MaxNormalizedUpdateBytes {
		t.Fatalf(
			"Update.ByteSize() = %d, exceeds %d",
			update.ByteSize(),
			MaxNormalizedUpdateBytes,
		)
	}

	exact, err := normalizedUpdateByteSize(
		MaxNormalizedUpdateBytes-normalizedUpdateEnvelopeBytes,
		0,
	)
	if err != nil {
		t.Fatalf("normalizedUpdateByteSize(exact limit): %v", err)
	}
	if exact != MaxNormalizedUpdateBytes {
		t.Fatalf(
			"exact update charge = %d, want %d",
			exact,
			MaxNormalizedUpdateBytes,
		)
	}
	_, err = normalizedUpdateByteSize(
		MaxNormalizedUpdateBytes-normalizedUpdateEnvelopeBytes+1,
		0,
	)
	requireValidationError(t, err, SizeExceeded, "update", MaxNormalizedUpdateBytes)

	const maxInt = int(^uint(0) >> 1)
	_, err = normalizedUpdateByteSize(maxInt, 1)
	requireValidationError(t, err, SizeExceeded, "update", MaxNormalizedUpdateBytes)
	_, err = normalizedUpdateByteSize(-1, 0)
	requireValidationError(t, err, SizeExceeded, "update", MaxNormalizedUpdateBytes)
}

func TestUpdateRejectsMalformedOwnedIdentifier(t *testing.T) {
	t.Parallel()

	private := "do-not-disclose\nchat-a"
	_, err := NewUpdate(UpdateInput{
		Kind:   UpdateSummary,
		ChatID: ChatID{value: private},
	})
	validationErr := requireValidationError(
		t,
		err,
		ControlCharacter,
		"chat_id",
		MaxIdentifierBytes,
	)
	if strings.Contains(validationErr.Error(), private) ||
		strings.Contains(validationErr.Error(), "do-not-disclose") {
		t.Fatalf("ValidationError disclosed rejected content: %q", validationErr.Error())
	}
}

func updateTestIDs(t *testing.T) (ChatID, MessageID) {
	t.Helper()
	chatID, err := NewChatID("chat-a")
	if err != nil {
		t.Fatalf("NewChatID: %v", err)
	}
	messageID, err := NewMessageID("message-a")
	if err != nil {
		t.Fatalf("NewMessageID: %v", err)
	}
	return chatID, messageID
}

func newUpdateError(input UpdateInput) error {
	_, err := NewUpdate(input)
	return err
}
