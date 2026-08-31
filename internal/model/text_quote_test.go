package model

import (
	"strings"
	"testing"
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
