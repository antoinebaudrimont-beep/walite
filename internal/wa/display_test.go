package wa

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestDisplayLocalContactPriorityAndAliases(t *testing.T) {
	pn := types.NewJID("12345", types.DefaultUserServer)
	lid := types.NewJID("98765", types.HiddenUserServer)
	aliases := &chatAliases{}
	pid, lidID := aliasID(t, pn.String()), aliasID(t, lid.String())
	stable, err := aliases.resolve(lidID, pid)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	resolver := newDisplayResolver(aliases, nil, func(_ context.Context, jid types.JID) (types.ContactInfo, error) {
		calls++
		if jid == pn {
			return types.ContactInfo{Found: true, FullName: "Jean Café 👋", PushName: "Phone push name"}, nil
		}
		return types.ContactInfo{Found: true, PushName: "Push"}, nil
	}, nil)
	resolver.observePerson(lid, types.EmptyJID, "Live push", model.DisplayPush)
	resolver.lookup(context.Background(), displayRequest{id: stable})
	for _, id := range []model.ChatID{pid, lidID} {
		if got := resolver.cached(id); got.Name() != "Jean Café 👋" || got.Quality() != model.DisplaySaved {
			t.Fatalf("name=%+v", got)
		}
	}
	resolver.observePerson(pn, lid, "Lower quality", model.DisplayPush)
	resolver.lookup(context.Background(), displayRequest{id: pid})
	if resolver.cached(lidID).Name() != "Jean Café 👋" || calls != 2 || aliases.count != 1 {
		t.Fatal("quality/lookup/identity bound changed")
	}
	if current, _ := aliases.resolve(pid, lidID); current != stable {
		t.Fatal("name lookup changed stable chat ID")
	}
	for _, test := range []struct {
		info    types.ContactInfo
		name    string
		quality model.DisplayQuality
	}{
		{types.ContactInfo{FullName: "Full", FirstName: "First", BusinessName: "Biz", PushName: "Push"}, "Full", model.DisplaySaved},
		{types.ContactInfo{FirstName: "First", BusinessName: "Biz"}, "First", model.DisplaySaved},
		{types.ContactInfo{BusinessName: "Biz", PushName: "Push"}, "Biz", model.DisplayBusiness},
		{types.ContactInfo{PushName: "Push"}, "Push", model.DisplayPush},
		{types.ContactInfo{}, "", model.DisplayOpaque},
	} {
		if name, quality := contactDisplayName(test.info); name != test.name || quality != test.quality {
			t.Fatal("priority")
		}
	}
}

func TestCachedDirectChatSeedRequestsBoundedLocalContactUpgrade(t *testing.T) {
	pn := types.NewJID("12345", types.DefaultUserServer)
	lid := types.NewJID("98765", types.HiddenUserServer)
	source := newRealtimeSource()
	source.display = newDisplayResolver(source.aliases, func(_ context.Context, jid types.JID) (types.JID, error) {
		if jid != lid {
			t.Fatalf("alternate lookup=%s", jid)
		}
		return pn, nil
	}, func(_ context.Context, jid types.JID) (types.ContactInfo, error) {
		if jid == pn {
			return types.ContactInfo{Found: true, FullName: "Saved Café 👋", PushName: "lower push"}, nil
		}
		return types.ContactInfo{}, nil
	}, nil)
	lidID := aliasID(t, lid.String())
	if err := source.SeedChatIDs([]model.ChatID{lidID}); err != nil {
		t.Fatal(err)
	}
	if source.display.count != 1 {
		t.Fatalf("queued lookups=%d", source.display.count)
	}
	request := source.display.requests[source.display.head]
	source.display.lookup(context.Background(), request)
	for _, id := range []model.ChatID{lidID, aliasID(t, pn.String())} {
		if got := source.display.cached(id); got.Name() != "Saved Café 👋" || got.Quality() != model.DisplaySaved {
			t.Fatalf("cached chat metadata=%+v", got)
		}
	}
	if source.aliases.count != 1 {
		t.Fatal("presentation lookup consumed another stable chat identity")
	}
}

func TestResolvedContactSurvivesPresentationSlotEviction(t *testing.T) {
	pn := types.NewJID("12345", types.DefaultUserServer)
	lid := types.NewJID("98765", types.HiddenUserServer)
	alternateCalls, contactCalls := 0, 0
	resolver := newDisplayResolver(&chatAliases{}, func(_ context.Context, jid types.JID) (types.JID, error) {
		alternateCalls++
		if jid != lid {
			t.Fatalf("alternate lookup=%s", jid)
		}
		return pn, nil
	}, func(_ context.Context, jid types.JID) (types.ContactInfo, error) {
		contactCalls++
		if jid == pn {
			return types.ContactInfo{Found: true, FullName: "Saved after eviction 👋"}, nil
		}
		return types.ContactInfo{}, nil
	}, nil)
	lidID, pnID := aliasID(t, lid.String()), aliasID(t, pn.String())
	resolver.lookup(context.Background(), displayRequest{id: lidID})
	for index := 0; index < model.DisplayMetadataCapacity; index++ {
		value, _ := model.NewDisplayMetadata(fmt.Sprintf("eviction-%03d", index), "Synthetic", model.DisplayPush, false)
		resolver.offer(value, false)
	}
	if resolver.cached(lidID).Name() != "" || resolver.cached(pnID).Name() != "" {
		t.Fatal("fixture did not evict both alias presentation slots")
	}
	resolver.lookup(context.Background(), displayRequest{id: lidID})
	for _, id := range []model.ChatID{lidID, pnID} {
		if got := resolver.cached(id); got.Name() != "Saved after eviction 👋" || got.Quality() != model.DisplaySaved {
			t.Fatalf("restored metadata=%+v", got)
		}
	}
	if alternateCalls != 1 || contactCalls != 2 {
		t.Fatalf("local store was reread after eviction: alternate=%d contacts=%d", alternateCalls, contactCalls)
	}
}

func TestRealtimeNameImprovesRetainedPhoneFallbackAfterEviction(t *testing.T) {
	pn := types.NewJID("22222", types.DefaultUserServer)
	contactCalls := 0
	resolver := newDisplayResolver(&chatAliases{}, nil, func(context.Context, types.JID) (types.ContactInfo, error) {
		contactCalls++
		return types.ContactInfo{}, nil
	}, nil)
	id := aliasID(t, pn.String())
	resolver.lookup(context.Background(), displayRequest{id: id})
	if got := resolver.cached(id); got.Name() != "+22222" || got.Quality() != model.DisplayPhone {
		t.Fatalf("initial fallback=%+v", got)
	}
	resolver.observePerson(pn, types.EmptyJID, "Later push name", model.DisplayPush)
	for index := 0; index < model.DisplayMetadataCapacity; index++ {
		value, _ := model.NewDisplayMetadata(fmt.Sprintf("realtime-eviction-%03d", index), "Synthetic", model.DisplayPush, false)
		resolver.offer(value, false)
	}
	if resolver.cached(id).Name() != "" {
		t.Fatal("fixture did not evict improved presentation slot")
	}
	resolver.lookup(context.Background(), displayRequest{id: id})
	if got := resolver.cached(id); got.Name() != "Later push name" || got.Quality() != model.DisplayPush {
		t.Fatalf("restored realtime metadata=%+v", got)
	}
	if contactCalls != 1 {
		t.Fatalf("contact store reread %d times", contactCalls)
	}
}

func TestDisplayWorkerDrainsNamesBeforePresentationSlotReuse(t *testing.T) {
	resolver := newDisplayResolver(&chatAliases{}, nil, func(_ context.Context, jid types.JID) (types.ContactInfo, error) {
		return types.ContactInfo{Found: true, PushName: "Name " + jid.User}, nil
	}, nil)
	ids := make([]model.ChatID, model.DisplayMetadataCapacity+1)
	for index := range ids {
		ids[index] = aliasID(t, fmt.Sprintf("%d@s.whatsapp.net", 10000+index))
	}
	resolver.requestPeople(ids)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan struct{})
	go func() {
		resolver.run(ctx)
		close(done)
	}()
	seen := make(map[model.ChatID]struct{}, len(ids))
	for len(seen) < len(ids) {
		select {
		case value := <-resolver.updates:
			seen[value.ID()] = struct{}{}
		case <-ctx.Done():
			t.Fatalf("received %d/%d metadata updates", len(seen), len(ids))
		}
	}
	cancel()
	<-done
	if len(seen) != len(ids) {
		t.Fatalf("metadata updates=%d want=%d", len(seen), len(ids))
	}
}

func TestDisplayLookupBoundsAndUnknownFallback(t *testing.T) {
	contacts, groups := 0, 0
	resolver := newDisplayResolver(&chatAliases{}, nil, func(context.Context, types.JID) (types.ContactInfo, error) {
		contacts++
		return types.ContactInfo{}, nil
	}, func(context.Context, types.JID) (string, error) { groups++; return "", errors.New("unavailable") })
	for i := 0; i < DisplayContactLookupLimit+10; i++ {
		resolver.lookup(context.Background(), displayRequest{id: aliasID(t, fmt.Sprintf("%d@lid", 10000+i))})
	}
	for i := 0; i < DisplayGroupLookupLimit+10; i++ {
		request := displayRequest{id: aliasID(t, fmt.Sprintf("123-%d@g.us", i))}
		resolver.lookup(context.Background(), request)
		resolver.lookup(context.Background(), request)
	}
	if contacts != DisplayContactLookupLimit || groups != DisplayGroupLookupLimit || resolver.aliases.count != 0 {
		t.Fatal("lookups unbounded or consumed identity registry")
	}
	if len(resolver.slots) != model.DisplayMetadataCapacity || cap(resolver.updates) != model.DisplayMetadataCapacity ||
		len(resolver.contacts) != DisplayContactLookupLimit || resolver.cached(aliasID(t, "10000@lid")).Name() != "" {
		t.Fatal("resolver bounds changed or unknown LID invented a name")
	}
	queue := newDisplayResolver(nil, nil, nil, nil)
	for i := 0; i < DisplayRequestCapacity+10; i++ {
		request := displayRequest{id: aliasID(t, fmt.Sprintf("%d@lid", i+1))}
		queue.request(request)
		queue.request(request)
	}
	if queue.count != DisplayRequestCapacity {
		t.Fatal("request queue bound/dedup")
	}
}

func TestGroupLookupDoesNotBlockMessagesAndLiveSubjectWins(t *testing.T) {
	source := newRealtimeSource()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	source.display = newDisplayResolver(source.aliases, nil, nil, func(ctx context.Context, _ types.JID) (string, error) {
		calls.Add(1)
		close(started)
		if _, ok := ctx.Deadline(); !ok {
			t.Error("lookup has no deadline")
		}
		select {
		case <-release:
			return "Stale fetched subject", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- source.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	group := types.NewJID("123-456", types.GroupServer)
	person := types.NewJID("98765", types.HiddenUserServer)
	text := "Group body é 👋"
	message := upstreamTextMessage("unused", "group-message", time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), false, &waE2E.Message{Conversation: &text})
	message.Info.Chat, message.Info.Sender, message.Info.IsGroup, message.Info.PushName = group, person, true, "Elena"
	client := &whatsmeowConnectionClient{realtime: source}
	client.handleEvent(message)
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("group lookup did not start")
	}
	select {
	case event := <-source.RealtimeEvents():
		if event.Message().SenderID().String() != person.String() || !event.Message().IsGroup() || event.Message().Text() != text {
			t.Fatal("sender/body lost")
		}
	case <-ctx.Done():
		t.Fatal("group lookup blocked message delivery")
	}
	client.handleEvent(&events.GroupInfo{JID: group, Name: &types.GroupName{Name: "Family 家族"}})
	close(release)
	for {
		select {
		case value := <-source.DisplayUpdates():
			if value.ID().String() == person.String() && value.Name() == "Elena" {
				if source.display.cached(aliasID(t, group.String())).Name() != "Family 家族" || calls.Load() != 1 {
					t.Fatal("stale network result replaced live subject")
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("metadata worker stopped progressing")
		}
	}
}

func TestDisplayWorkerCancellationJoinsLookup(t *testing.T) {
	source := newRealtimeSource()
	started := make(chan struct{})
	finished := make(chan struct{})
	source.display = newDisplayResolver(source.aliases, nil, nil, func(ctx context.Context, _ types.JID) (string, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return "", ctx.Err()
	})
	source.display.observeGroup(types.NewJID("1-2", types.GroupServer), "", false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("lookup worker leaked")
	}
}

func TestDisplayRequestCoalescingPreservesReversedAliases(t *testing.T) {
	pn, lid := aliasID(t, "12345@s.whatsapp.net"), aliasID(t, "98765@lid")
	resolver := newDisplayResolver(nil, nil, nil, nil)
	resolver.request(displayRequest{id: lid})
	resolver.request(displayRequest{id: pn, alternate: lid})
	resolver.request(displayRequest{id: lid, alternate: pn})
	if resolver.count != 1 || resolver.requests[0] != (displayRequest{id: lid, alternate: pn}) {
		t.Fatalf("coalesced request lost pair: %+v", resolver.requests[0])
	}
	// Learning an alternate after a lookup started schedules one bounded
	// follow-up; repeatedly observing either spelling does not add more work.
	resolver = newDisplayResolver(nil, nil, nil, nil)
	resolver.inflight = displayRequest{id: lid}
	resolver.request(displayRequest{id: lid})
	if resolver.count != 0 {
		t.Fatal("repeated in-flight lookup")
	}
	resolver.request(displayRequest{id: pn, alternate: lid})
	resolver.request(displayRequest{id: lid, alternate: pn})
	if resolver.count != 1 || !resolver.requests[0].covers(displayRequest{id: lid, alternate: pn}) {
		t.Fatal("new alternate lost or duplicated")
	}
	resolver = newDisplayResolver(nil, nil, nil, nil)
	resolver.inflight = displayRequest{id: lid, alternate: pn}
	resolver.request(displayRequest{id: pn, alternate: lid})
	if resolver.count != 0 {
		t.Fatal("fully known in-flight pair was requeued")
	}
}

func TestDisplayLiveContactWinsInFlightLookupForBothAliases(t *testing.T) {
	pn, lid := types.NewJID("12345", types.DefaultUserServer), types.NewJID("98765", types.HiddenUserServer)
	var resolver *displayResolver
	name := "Updated Café 👋"
	resolver = newDisplayResolver(nil, nil, func(_ context.Context, jid types.JID) (types.ContactInfo, error) {
		if jid == pn {
			resolver.observeEvent(&events.Contact{JID: pn, Action: &waSyncAction.ContactAction{FullName: &name}})
			return types.ContactInfo{Found: true, FullName: "Stale saved contact"}, nil
		}
		return types.ContactInfo{}, nil
	}, nil)
	resolver.lookup(context.Background(), displayRequest{id: aliasID(t, lid.String()), alternate: aliasID(t, pn.String())})
	for _, jid := range []types.JID{pn, lid} {
		if got := resolver.cached(aliasID(t, jid.String())); got.Name() != name || got.Quality() != model.DisplaySaved {
			t.Fatalf("stale alias label: %+v", got)
		}
	}
}

func TestDisplayLocalAlternatePhoneAndOpaqueFallbacks(t *testing.T) {
	pn, lid := types.NewJID("12345", types.DefaultUserServer), types.NewJID("98765", types.HiddenUserServer)
	resolver := newDisplayResolver(nil, func(_ context.Context, jid types.JID) (types.JID, error) {
		if jid == lid {
			return pn, nil
		}
		return types.EmptyJID, nil
	}, func(context.Context, types.JID) (types.ContactInfo, error) {
		return types.ContactInfo{RedactedPhone: "12•••"}, nil
	}, nil)
	resolver.lookup(context.Background(), displayRequest{id: aliasID(t, lid.String())})
	for _, jid := range []types.JID{pn, lid} {
		if got := resolver.cached(aliasID(t, jid.String())); got.Name() != "+12345" || got.Quality() != model.DisplayPhone {
			t.Fatalf("safe PN fallback missing: %+v", got)
		}
	}
	unknown := types.NewJID("77777", types.HiddenUserServer)
	resolver.lookup(context.Background(), displayRequest{id: aliasID(t, unknown.String())})
	if resolver.cached(aliasID(t, unknown.String())).Name() != "" {
		t.Fatal("LID digits treated as phone number")
	}
}

func TestGroupParticipantDisplayUsesPushPhoneAndOpaqueFallbacks(t *testing.T) {
	for _, test := range []struct {
		name    string
		id      string
		contact types.ContactInfo
		want    string
		quality model.DisplayQuality
	}{
		{name: "push name", id: "11111@s.whatsapp.net", contact: types.ContactInfo{Found: true, PushName: "Push Person 👋"}, want: "Push Person 👋", quality: model.DisplayPush},
		{name: "PN phone", id: "22222@s.whatsapp.net", contact: types.ContactInfo{}, want: "+22222", quality: model.DisplayPhone},
		{name: "opaque LID", id: "33333@lid", contact: types.ContactInfo{}, want: "", quality: model.DisplayOpaque},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := newDisplayResolver(nil, nil, func(context.Context, types.JID) (types.ContactInfo, error) {
				return test.contact, nil
			}, nil)
			id := aliasID(t, test.id)
			resolver.requestPeople([]model.ChatID{id, id, id})
			if resolver.count != 1 {
				t.Fatalf("repeated participant queued %d lookups", resolver.count)
			}
			resolver.lookup(context.Background(), resolver.requests[resolver.head])
			got := resolver.cached(id)
			if got.Name() != test.want || got.Quality() != test.quality {
				t.Fatalf("participant metadata=%+v want name=%q quality=%d", got, test.want, test.quality)
			}
		})
	}
}

func TestReadableChatFallbackValidatesPNAndKeepsOpaqueIdentity(t *testing.T) {
	for _, test := range []struct {
		id, want string
	}{
		{id: "12086708856@s.whatsapp.net", want: "+12086708856"},
		{id: "218699835404531@lid", want: "218699835404531@lid"},
		{id: "not-a-phone@s.whatsapp.net", want: "not-a-phone@s.whatsapp.net"},
		{id: "family@g.us", want: "family@g.us"},
	} {
		id := aliasID(t, test.id)
		if got := ReadableChatFallback(id); got != test.want {
			t.Fatalf("id=%q fallback=%q want=%q", test.id, got, test.want)
		}
	}
}

func TestDisplayEqualQualityContactLookupIsAliasOrderIndependent(t *testing.T) {
	pn, lid := types.NewJID("12345", types.DefaultUserServer), types.NewJID("98765", types.HiddenUserServer)
	for _, pair := range [][2]types.JID{{pn, lid}, {lid, pn}} {
		resolver := newDisplayResolver(nil, nil, func(_ context.Context, jid types.JID) (types.ContactInfo, error) {
			name := "LID saved"
			if jid == pn {
				name = "PN saved"
			}
			return types.ContactInfo{FullName: name}, nil
		}, nil)
		resolver.lookup(context.Background(), displayRequest{id: aliasID(t, pair[0].String()), alternate: aliasID(t, pair[1].String())})
		for _, jid := range pair {
			if resolver.cached(aliasID(t, jid.String())).Name() != "PN saved" {
				t.Fatal("lookup order changed label")
			}
		}
	}
}

func TestDisplayGroupSubjectLookupAndKnownSubjectSkip(t *testing.T) {
	group := types.NewJID("123-456", types.GroupServer)
	calls := 0
	resolver := newDisplayResolver(nil, nil, nil, func(context.Context, types.JID) (string, error) { calls++; return "Family 家族", nil })
	request := displayRequest{id: aliasID(t, group.String())}
	resolver.lookup(context.Background(), request)
	resolver.lookup(context.Background(), request)
	if calls != 1 || resolver.cached(request.id).Name() != "Family 家族" {
		t.Fatal("group lookup missing or repeated")
	}
	resolver = newDisplayResolver(nil, nil, nil, func(context.Context, types.JID) (string, error) { t.Fatal("looked up known subject"); return "", nil })
	resolver.observeEvent(&events.JoinedGroup{GroupInfo: types.GroupInfo{JID: group, GroupName: types.GroupName{Name: "Known subject"}}})
	resolver.lookup(context.Background(), request)
	if resolver.cached(request.id).Name() != "Known subject" || resolver.groupCount != 0 {
		t.Fatal("known live subject consumed lookup budget")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resolver.lookup(ctx, displayRequest{id: aliasID(t, "777-888@g.us")})
	if resolver.groupCount != 0 {
		t.Fatal("cancelled lookup consumed budget")
	}
}
