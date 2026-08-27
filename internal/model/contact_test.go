package model

import (
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestContactConstructionOwnershipAndAccessors(t *testing.T) {
	t.Parallel()
	idBytes := []byte("contact-opaque")
	nameBytes := []byte("Café Contact")
	updatedAt := time.UnixMilli(123).UTC()
	contact, err := NewContact(ContactInput{
		ID:          mutableString(idBytes),
		DisplayName: mutableString(nameBytes),
		UpdatedAt:   updatedAt,
		IngestSeq:   7,
	})
	if err != nil {
		t.Fatal(err)
	}
	idBytes[0] = 'X'
	nameBytes[0] = 'X'
	if contact.ID().String() != "contact-opaque" || contact.DisplayName() != "Café Contact" ||
		!contact.UpdatedAt().Equal(updatedAt) || contact.IngestSeq() != 7 || contact.NameTruncated() {
		t.Fatalf("contact=(%q,%q,%v,%d,%t)", contact.ID().String(), contact.DisplayName(), contact.UpdatedAt(), contact.IngestSeq(), contact.NameTruncated())
	}
}

func TestContactNameBoundsAndValidation(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("x", MaxContactDisplayNameBytes+1)
	contact, err := NewContact(ContactInput{ID: "contact-a", DisplayName: huge})
	if err != nil {
		t.Fatal(err)
	}
	if !contact.NameTruncated() || len(contact.DisplayName()) != MaxContactDisplayNameBytes {
		t.Fatalf("contact name=(%d bytes,truncated=%t)", len(contact.DisplayName()), contact.NameTruncated())
	}
	if unsafe.StringData(contact.DisplayName()) == unsafe.StringData(huge) {
		t.Fatal("Contact retained oversized display-name backing storage")
	}

	_, err = NewContact(ContactInput{})
	requireValidationError(t, err, Required, "contact_id", 0)
	_, err = NewContact(ContactInput{ID: "private\ncontact"})
	requireValidationError(t, err, ControlCharacter, "contact_id", MaxIdentifierBytes)
	_, err = NewContact(ContactInput{ID: "contact-a", IngestSeq: ^uint64(0)})
	requireValidationError(t, err, InvalidValue, "ingest_seq", 0)
}

func TestChatExtendedMetadataAccessors(t *testing.T) {
	t.Parallel()
	lastMessageAt := time.UnixMilli(100).UTC()
	updatedAt := time.UnixMilli(200).UTC()
	chat, err := NewChat(ChatInput{
		ID:            "chat-a",
		ContactID:     "contact-a",
		DisplayName:   "Project Room",
		IsGroup:       true,
		LastMessageAt: lastMessageAt,
		UnreadCount:   12,
		Muted:         true,
		Archived:      true,
		UpdatedAt:     updatedAt,
		IngestSeq:     9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !chat.HasContact() || chat.ContactID().String() != "contact-a" || !chat.IsGroup() ||
		!chat.LastMessageAt().Equal(lastMessageAt) || chat.UnreadCount() != 12 ||
		!chat.Muted() || !chat.Archived() || chat.Placeholder() ||
		!chat.UpdatedAt().Equal(updatedAt) || chat.IngestSeq() != 9 {
		t.Fatalf("extended chat metadata was not preserved: %+v", chat)
	}

	withoutContact, err := NewChat(ChatInput{ID: "group-a", IsGroup: true})
	if err != nil {
		t.Fatal(err)
	}
	if withoutContact.HasContact() || withoutContact.ContactID().String() != "" {
		t.Fatalf("contact-free group has contact %q", withoutContact.ContactID().String())
	}

	_, err = NewChat(ChatInput{ID: "chat-a", ContactID: "private\ncontact"})
	requireValidationError(t, err, ControlCharacter, "contact_id", MaxIdentifierBytes)
	_, err = NewChat(ChatInput{ID: "chat-a", IngestSeq: ^uint64(0)})
	requireValidationError(t, err, InvalidValue, "ingest_seq", 0)
}
