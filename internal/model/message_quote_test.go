package model

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMessageQuoteSurvivesNormalizedCopiesAndByteAccounting(t *testing.T) {
	now := time.Date(2100, 8, 1, 12, 0, 0, 0, time.UTC)
	for _, fromMe := range []bool{false, true} {
		for _, text := range []string{"synthetic Café 日本語 👋", strings.Repeat("x", MaxQuoteTextBytes-1) + "👋 tail"} {
			quote, err := NewTextQuote("original-ID", text, fromMe)
			if err != nil {
				t.Fatal(err)
			}
			message, err := NewMessage(MessageInput{ChatID: "chat", MessageID: "reply-ID", SentAt: now, FromMe: true, Text: "reply 👋", Quote: quote})
			if err != nil {
				t.Fatal(err)
			}
			wantBytes := len("chat") + len("reply-ID") + len("reply 👋") + len("original-ID") + len(quote.Text())
			if message.ByteSize() != wantBytes || message.Quote() != quote || len(quote.Text()) > MaxQuoteTextBytes || !utf8.ValidString(quote.Text()) {
				t.Fatal("quote fidelity or charge lost")
			}
			event, err := NewEvent(message, now)
			if err != nil {
				t.Fatal(err)
			}
			batch, err := NewWriteBatch(WriteRealtime, []Message{event.Message()})
			if err != nil {
				t.Fatal(err)
			}
			copied, _ := batch.At(0)
			live, err := NewLiveMessageCommitted(copied, 3, now)
			if err != nil {
				t.Fatal(err)
			}
			liveBatch, err := NewLiveEventBatch([]LiveEvent{live})
			if err != nil {
				t.Fatal(err)
			}
			committed, _ := liveBatch.At(0)
			if committed.Message() != message || event.ByteSize() != wantBytes+normalizedEventEnvelopeBytes || live.ByteSize() <= wantBytes {
				t.Fatal("normalized/committed copy lost quote or byte charge")
			}
			other, _ := NewChatID("longer-chat")
			aliased, err := message.WithChatID(other)
			if err != nil || aliased.Quote() != quote || aliased.ByteSize() != wantBytes+len("longer-chat")-len("chat") {
				t.Fatal("identity copy lost quote or byte charge")
			}
			if pruned := message.WithoutBody(); pruned.Quote() != (TextQuote{}) || pruned.Text() != "" || pruned.ByteSize() != len("chat")+len("reply-ID") || message.Quote() != quote {
				t.Fatal("body pruning retained private excerpt or mutated original")
			}
		}
	}
}

func TestMessageQuoteKeepsMaximumBatchBounded(t *testing.T) {
	id := strings.Repeat("i", MaxIdentifierBytes)
	quoteMedia, _ := NewMedia(MediaDocument, strings.Repeat("r", MaxMediaNameBytes), strings.Repeat("t", MaxMediaMIMEBytes))
	quote, _ := NewMediaQuote(id, strings.Repeat("q", MaxQuoteTextBytes), true, quoteMedia)
	media, _ := NewDownloadableMedia(MediaDocument, strings.Repeat("n", MaxMediaNameBytes), strings.Repeat("m", MaxMediaMIMEBytes),
		"/"+strings.Repeat("p", MaxMediaDirectPathBytes-1), []byte(strings.Repeat("k", MaxMediaKeyBytes)),
		[]byte(strings.Repeat("h", MaxMediaHashBytes)), []byte(strings.Repeat("e", MaxMediaHashBytes)), 1)
	message, err := NewMessage(MessageInput{ChatID: id, MessageID: id, SenderID: id, IsGroup: true, SentAt: time.Unix(1, 0), Text: strings.Repeat("b", MaxRetainedTextBytes), Quote: quote, Media: media})
	if err != nil {
		t.Fatal(err)
	}
	event, err := NewEvent(message, message.SentAt())
	if err != nil || event.ByteSize() > MaxNormalizedEventBytes {
		t.Fatal("quoted event exceeds reserved charge")
	}
	var messages [MaxWriteBatchMessages]Message
	for i := range messages {
		messages[i] = message
	}
	batch, err := NewWriteBatch(WriteRealtime, messages[:])
	if err != nil || batch.Len() != MaxWriteBatchMessages || batch.ByteSize() != maxWriteBatchBytes {
		t.Fatalf("batch bound: %d %v", batch.ByteSize(), err)
	}
	if _, err := NewWriteBatch(WriteRealtime, append(messages[:], message)); err == nil {
		t.Fatal("oversized batch accepted")
	}
}

func TestMessageRejectsMalformedQuote(t *testing.T) {
	id, _ := NewMessageID("original")
	for _, quote := range []TextQuote{{id: id}, {text: "no ID"}, {id: id, text: "\xff"}, {id: id, text: strings.Repeat("q", MaxQuoteTextBytes+1)}} {
		message, _ := NewMessage(MessageInput{ChatID: "chat", MessageID: "reply", SentAt: time.Unix(1, 0), Text: "body"})
		if _, err := NewEvent(message.WithQuote(quote), message.SentAt()); err == nil {
			t.Fatal("malformed message quote accepted")
		}
	}
}
