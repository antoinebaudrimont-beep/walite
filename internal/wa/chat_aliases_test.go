package wa

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/types"
)

func aliasID(t *testing.T, value string) model.ChatID {
	t.Helper()
	id, err := model.NewChatID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestChatAliasesRetainFirstIdentityBeforeAndAfterLearningAlternate(t *testing.T) {
	pn, lid := aliasID(t, "12345@s.whatsapp.net"), aliasID(t, "987654@lid")
	for _, primary := range []model.ChatID{pn, lid} {
		for _, learnLate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/late=%t", primary.String(), learnLate), func(t *testing.T) {
				alternate := lid
				if primary == lid {
					alternate = pn
				}
				aliases := &chatAliases{}
				if learnLate {
					if got, err := aliases.resolve(primary, model.ChatID{}); err != nil || got != primary {
						t.Fatalf("pin=%v %v", got, err)
					}
				}
				for i := 0; i < 5; i++ {
					first, second := primary, alternate
					if learnLate || i%2 != 0 {
						first, second = alternate, primary
					}
					if got, err := aliases.resolve(first, second); err != nil || got != primary {
						t.Fatalf("resolve=%v %v", got, err)
					}
				}
				if aliases.count != 1 {
					t.Fatalf("count=%d", aliases.count)
				}
			})
		}
	}
}

func TestChatAliasesFullNeverEvictsOrChangesExistingIdentity(t *testing.T) {
	aliases := &chatAliases{}
	for i := 0; i < ChatAliasCapacity; i++ {
		pn := aliasID(t, fmt.Sprintf("%d@s.whatsapp.net", i+1000))
		if _, err := aliases.resolve(pn, model.ChatID{}); err != nil {
			t.Fatal(err)
		}
	}
	before := aliases.entries
	for i := 0; i < 3; i++ {
		if _, err := aliases.resolve(aliasID(t, "99999@lid"), model.ChatID{}); !errors.Is(err, ErrChatAliasesFull) {
			t.Fatalf("full=%v", err)
		}
		if aliases.entries != before || aliases.count != ChatAliasCapacity {
			t.Fatal("full table changed existing routes")
		}
	}
	// Filling the alternate of an existing record needs no extra slot.
	primary := before[0].primary
	if got, err := aliases.resolve(aliasID(t, "88888@lid"), primary); err != nil || got != primary {
		t.Fatalf("learn at capacity=%v %v", got, err)
	}
}

func TestChatAliasesDoNotInferNumbersOrMergeEstablishedChats(t *testing.T) {
	aliases := &chatAliases{}
	pn, lid := aliasID(t, "12345@s.whatsapp.net"), aliasID(t, "12345@lid")
	for _, id := range []model.ChatID{pn, lid} {
		if got, err := aliases.resolve(id, model.ChatID{}); err != nil || got != id {
			t.Fatalf("unproven alias=%v %v", got, err)
		}
	}
	before := aliases.entries
	if _, err := aliases.resolve(pn, lid); !errors.Is(err, ErrChatAliasConflict) {
		t.Fatalf("late independent merge=%v", err)
	}
	if aliases.entries != before || aliases.count != 2 {
		t.Fatal("conflict mutated identities")
	}
}

func TestSendRouteReconcilesOnlyAuthoritativeSingletonPair(t *testing.T) {
	pn, lid := aliasID(t, "12345@s.whatsapp.net"), aliasID(t, "987654@lid")
	aliases := &chatAliases{}
	for _, id := range []model.ChatID{pn, lid} {
		if _, err := aliases.resolve(id, model.ChatID{}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := aliases.resolveForSend(lid, pn); err != nil || got != lid || aliases.count != 1 || aliases.entries[0] != (chatAlias{primary: lid, alternate: pn}) {
		t.Fatalf("resolved=%q entries=%d first=%+v err=%v", got.String(), aliases.count, aliases.entries[0], err)
	}
	if got, err := aliases.resolveForSend(pn, lid); err != nil || got != pn || aliases.count != 1 || aliases.entries[0] != (chatAlias{primary: pn, alternate: lid}) {
		t.Fatalf("reoriented=%q entries=%d first=%+v err=%v", got.String(), aliases.count, aliases.entries[0], err)
	}

	otherPN := aliasID(t, "22222@s.whatsapp.net")
	conflict := &chatAliases{}
	_, _ = conflict.resolve(pn, lid)
	_, _ = conflict.resolve(otherPN, model.ChatID{})
	before := conflict.entries
	if _, err := conflict.resolveForSend(otherPN, lid); !errors.Is(err, ErrChatAliasConflict) {
		t.Fatalf("established relationship conflict=%v", err)
	}
	if conflict.entries != before || conflict.count != 2 {
		t.Fatal("send reconciliation changed a non-singleton relationship")
	}
}

func TestChatAliasesRejectConflictingAuthoritativePair(t *testing.T) {
	aliases := &chatAliases{}
	pn, lid := aliasID(t, "12345@s.whatsapp.net"), aliasID(t, "987654@lid")
	_, _ = aliases.resolve(pn, lid)
	before := aliases.entries
	if _, err := aliases.resolve(lid, aliasID(t, "22222@s.whatsapp.net")); !errors.Is(err, ErrChatAliasConflict) {
		t.Fatalf("conflict=%v", err)
	}
	if aliases.entries != before {
		t.Fatal("conflict replaced a proven pair")
	}
}

func TestMessageChatAlternateUsesConversationNotGroupOrOwnSender(t *testing.T) {
	pn := types.NewJID("12345", types.DefaultUserServer)
	lid := types.NewJID("987654", types.HiddenUserServer)
	for _, fromMe := range []bool{false, true} {
		info := types.MessageInfo{MessageSource: types.MessageSource{Chat: lid, IsFromMe: fromMe, Sender: lid, SenderAlt: pn, RecipientAlt: pn}}
		info.Sender.Device = 2
		info.SenderAlt.Device = 2
		if got := messageChatAlternate(info); got.String() != pn.String() {
			t.Fatalf("alt=%v", got)
		}
		info.IsGroup = true
		info.Chat = types.NewJID("123-456", types.GroupServer)
		if got := messageChatAlternate(info); got.String() != "" {
			t.Fatal("group participant became chat alias")
		}
	}
	info := types.MessageInfo{MessageSource: types.MessageSource{Chat: lid, IsFromMe: true, SenderAlt: pn}}
	if got := messageChatAlternate(info); got.String() != "" {
		t.Fatal("own sender alias became recipient alias")
	}
}

func TestChatAliasesConcurrentResolution(t *testing.T) {
	aliases := &chatAliases{}
	pn, lid := aliasID(t, "12345@s.whatsapp.net"), aliasID(t, "987654@lid")
	_, _ = aliases.resolve(pn, model.ChatID{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				if got, err := aliases.resolve(lid, pn); err != nil || got != pn {
					t.Errorf("resolve=%v %v", got, err)
				}
			}
		}()
	}
	wg.Wait()
	if aliases.count != 1 {
		t.Fatalf("count=%d", aliases.count)
	}
}

func TestChatAliasEventMetadataAvoidsLookupAndExistingDeviceStoreProvidesFallback(t *testing.T) {
	client, err := newWhatsmeowConnectionClient(context.Background(), filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close() // no Connect, no network
	pn := types.NewJID("12345", types.DefaultUserServer)
	lid := types.NewJID("987654", types.HiddenUserServer)
	if other, err := client.realtime.lookup(context.Background(), pn); err != nil || !other.IsEmpty() {
		t.Fatalf("fresh unlinked device lookup=%v %v", other, err)
	}
	// Model the sub-store initialization performed when pairing saves a
	// device, without pairing, connecting, or creating authentication keys.
	client.client.Store.LIDs = client.container.LIDMap
	if err := client.client.Store.LIDs.PutLIDMapping(context.Background(), lid, pn); err != nil {
		t.Fatal(err)
	}
	primary := aliasID(t, pn.String())
	_, _ = client.realtime.aliases.resolve(primary, model.ChatID{})
	message, err := model.NewMessage(model.MessageInput{ChatID: lid.String(), MessageID: "id", SentAt: time.Now().UTC(), FromMe: true, Text: "é 日本語 🐧"})
	if err != nil {
		t.Fatal(err)
	}
	event, _ := model.NewEvent(message, message.SentAt())
	for _, explicit := range []bool{false, true} {
		entry := realtimeEntry{event: event}
		if explicit {
			entry.alternate = primary
			client.realtime.lookup = func(context.Context, types.JID) (types.JID, error) {
				t.Error("event metadata triggered lookup")
				return types.JID{}, errors.New("unexpected")
			}
		}
		resolved, err := client.realtime.resolveEntry(context.Background(), entry)
		if err != nil || resolved.Message().ChatID() != primary || resolved.Message().Text() != message.Text() {
			t.Fatalf("resolve=%+v err=%v", resolved, err)
		}
	}
}

func TestAliasCapacityOrLookupFailureStopsIncomingWithoutSilentDrop(t *testing.T) {
	for _, capacity := range []bool{false, true} {
		source := newRealtimeSource()
		want := ErrChatAliasLookup
		if capacity {
			want = ErrChatAliasesFull
			for i := 0; i < ChatAliasCapacity; i++ {
				_, _ = source.aliases.resolve(aliasID(t, fmt.Sprintf("%d@lid", i+1)), model.ChatID{})
			}
		} else {
			source.lookup = func(context.Context, types.JID) (types.JID, error) {
				return types.JID{}, errors.New("private db error")
			}
		}
		if !source.admit(sourceTestEvent(t, 0, time.Now().UTC())) {
			t.Fatal("admission")
		}
		if err := source.Run(context.Background()); !errors.Is(err, want) {
			t.Fatalf("source error=%v", err)
		}
		if _, ok := <-source.RealtimeEvents(); ok {
			t.Fatal("unresolved event escaped")
		}
		if source.admit(sourceTestEvent(t, 1, time.Now().UTC())) {
			t.Fatal("failed source remained accepting")
		}
	}
}

func TestSendAliasFailureBeforeTransport(t *testing.T) {
	for _, capacity := range []bool{false, true} {
		aliases := &chatAliases{}
		client := &fakeTextClient{connected: true, loggedIn: true}
		sender := &realTextSender{client: client, aliases: aliases}
		if capacity {
			for i := 0; i < ChatAliasCapacity; i++ {
				_, _ = aliases.resolve(aliasID(t, fmt.Sprintf("%d@lid", i+1)), model.ChatID{})
			}
		}
		if !capacity {
			sender.lookup = func(context.Context, types.JID) (types.JID, error) { return types.JID{}, errors.New("lookup error") }
		}
		if _, err := sender.SendText(context.Background(), aliasID(t, "12345@s.whatsapp.net"), "kept draft"); err == nil {
			t.Fatal("identity error accepted")
		}
		if client.calls.Load() != 0 {
			t.Fatal("identity failure called transport")
		}
	}
}

func TestChatAliasLookupCancellationPreservedBeforeTransport(t *testing.T) {
	client := &fakeTextClient{connected: true, loggedIn: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender := &realTextSender{client: client, aliases: &chatAliases{}, lookup: func(context.Context, types.JID) (types.JID, error) {
		cancel()
		return types.JID{}, errors.New("interrupted local query")
	}}
	if _, err := sender.SendText(ctx, aliasID(t, "12345@s.whatsapp.net"), "not sent"); !errors.Is(err, context.Canceled) {
		t.Fatalf("send error=%v, want cancellation", err)
	}
	if client.calls.Load() != 0 {
		t.Fatal("cancelled lookup called transport")
	}
}
