package model

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTextQuoteBoundedExactIdentityAndUnicode(t *testing.T) {
	for _, fromMe := range []bool{false, true} {
		for _, text := range []string{"quoted é 日本語 🐧", strings.Repeat("x", MaxQuoteTextBytes-1) + "🐧 more"} {
			quote, err := NewTextQuote("exact-quoted-ID", text, fromMe)
			if err != nil {
				t.Fatal(err)
			}
			if quote.MessageID().String() != "exact-quoted-ID" || quote.FromMe() != fromMe || !utf8.ValidString(quote.Text()) || len(quote.Text()) > MaxQuoteTextBytes || !strings.HasPrefix(text, quote.Text()) {
				t.Fatalf("quote=%+v", quote)
			}
			if len(text) <= MaxQuoteTextBytes && quote.Text() != text {
				t.Fatal("short Unicode quote changed")
			}
		}
	}
}

func TestTextQuoteRejectsUnavailableOrInvalidData(t *testing.T) {
	for _, input := range [][2]string{{"", "text"}, {"id", ""}, {"id", "\xff"}, {strings.Repeat("i", MaxIdentifierBytes+1), "text"}, {"id", strings.Repeat("x", MaxRetainedTextBytes+1)}} {
		if _, err := NewTextQuote(input[0], input[1], false); err == nil {
			t.Fatal("invalid quote accepted")
		}
	}
}

func TestTextQuoteOwnsBoundedGroupParticipant(t *testing.T) {
	quote, _ := NewTextQuote("quoted", "text", false)
	quote, err := quote.WithParticipant("55555@lid")
	if err != nil || quote.ParticipantID().String() != "55555@lid" {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
	message, err := NewMessage(MessageInput{ChatID: "123-456@g.us", MessageID: "reply", SentAt: time.Unix(1, 0).UTC(), FromMe: true, Text: "reply", Quote: quote})
	if err != nil || message.Quote() != quote {
		t.Fatalf("message=%+v err=%v", message, err)
	}
	if _, err := quote.WithParticipant(strings.Repeat("x", MaxIdentifierBytes+1)); err == nil {
		t.Fatal("oversized participant accepted")
	}
}
