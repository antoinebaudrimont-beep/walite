package wa

import (
	"context"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/types"
)

const (
	DisplayRequestCapacity    = 32
	DisplayGroupLookupLimit   = 32
	DisplayContactLookupLimit = 256
	displayLookupTimeout      = 3 * time.Second
)

type displayRequest struct{ id, alternate model.ChatID }
type displaySlot struct {
	value    model.DisplayMetadata
	dirty    bool
	revision uint64
}
type groupLookupRecord struct {
	id          model.ChatID
	liveSubject bool
}

// displayResolver is a single owned worker, a fixed coalescing request ring,
// and a FIFO advisory-name cache. It never participates in message admission.
type displayResolver struct {
	mu               sync.Mutex
	slots            [model.DisplayMetadataCapacity]displaySlot
	next, outputNext int
	revision         uint64
	requests         [DisplayRequestCapacity]displayRequest
	head, count      int
	inflight         displayRequest
	wake             chan struct{}
	updates          chan model.DisplayMetadata
	groups           [DisplayGroupLookupLimit]groupLookupRecord
	groupCount       int
	contacts         [DisplayContactLookupLimit]types.JID // worker-owned, no eviction
	contactCount     int
	aliases          *chatAliases
	alternate        alternateJIDLookup
	contact          func(context.Context, types.JID) (types.ContactInfo, error)
	group            func(context.Context, types.JID) (string, error)
}

func newDisplayResolver(aliases *chatAliases, alternate alternateJIDLookup, contact func(context.Context, types.JID) (types.ContactInfo, error), group func(context.Context, types.JID) (string, error)) *displayResolver {
	return &displayResolver{aliases: aliases, alternate: alternate, contact: contact, group: group, wake: make(chan struct{}, 1), updates: make(chan model.DisplayMetadata, 1)}
}

func (resolver *displayResolver) offer(value model.DisplayMetadata, liveSubject bool) {
	if resolver == nil || value.ID().String() == "" {
		return
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.offerLocked(value, liveSubject)
}

func (resolver *displayResolver) offerLocked(value model.DisplayMetadata, liveSubject bool) {
	if liveSubject && value.Name() != "" {
		for i := 0; i < resolver.groupCount; i++ {
			if resolver.groups[i].id == value.ID() {
				resolver.groups[i].liveSubject = true
			}
		}
	}
	index := resolver.find(value.ID())
	if index < 0 {
		index = resolver.next
		resolver.next = (index + 1) % len(resolver.slots)
		resolver.slots[index] = displaySlot{}
	}
	slot := &resolver.slots[index]
	merged := slot.value.Merge(value)
	if merged == slot.value {
		return
	}
	resolver.revision++
	*slot = displaySlot{value: merged, dirty: true, revision: resolver.revision}
	signalRealtimeSource(resolver.wake)
}

func (resolver *displayResolver) find(id model.ChatID) int {
	for i, slot := range resolver.slots {
		if slot.value.ID() == id {
			return i
		}
	}
	return -1
}

func (resolver *displayResolver) cached(id model.ChatID) model.DisplayMetadata {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if i := resolver.find(id); i >= 0 {
		return resolver.slots[i].value
	}
	return model.DisplayMetadata{}
}

func sameDisplayRequest(a, b displayRequest) bool {
	return a.id == b.id || (a.alternate.String() != "" && (a.alternate == b.id || a.alternate == b.alternate)) || (b.alternate.String() != "" && b.alternate == a.id)
}

func (request displayRequest) covers(other displayRequest) bool {
	contains := func(id model.ChatID) bool { return id.String() == "" || id == request.id || id == request.alternate }
	return contains(other.id) && contains(other.alternate)
}

func (resolver *displayResolver) request(request displayRequest) {
	if resolver == nil || request.id.String() == "" {
		return
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if resolver.inflight.covers(request) {
		return
	}
	for i := 0; i < resolver.count; i++ {
		index := (resolver.head + i) % len(resolver.requests)
		if sameDisplayRequest(resolver.requests[index], request) {
			pending := &resolver.requests[index]
			if pending.alternate.String() == "" {
				if pending.id == request.id {
					pending.alternate = request.alternate
				} else {
					pending.alternate = request.id
				}
			}
			return
		}
	}
	// Advisory lookup overflow keeps older pending work. It cannot drop or
	// delay a committed message; a subsequent observation may request again.
	if resolver.count == len(resolver.requests) {
		return
	}
	resolver.requests[(resolver.head+resolver.count)%len(resolver.requests)] = request
	resolver.count++
	signalRealtimeSource(resolver.wake)
}

func (resolver *displayResolver) run(ctx context.Context) {
	defer close(resolver.updates)
	for {
		resolver.mu.Lock()
		index := -1
		for n := 0; n < len(resolver.slots); n++ {
			i := (resolver.outputNext + n) % len(resolver.slots)
			if resolver.slots[i].dirty {
				index = i
				break
			}
		}
		var out chan model.DisplayMetadata
		var slot displaySlot
		if index >= 0 {
			out = resolver.updates
			slot = resolver.slots[index]
		}
		resolver.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case out <- slot.value:
			resolver.mu.Lock()
			if resolver.slots[index].revision == slot.revision {
				resolver.slots[index].dirty = false
			}
			resolver.outputNext = (index + 1) % len(resolver.slots)
			resolver.mu.Unlock()
		case <-resolver.wake:
			resolver.mu.Lock()
			if resolver.count == 0 {
				resolver.mu.Unlock()
				continue
			}
			request := resolver.requests[resolver.head]
			resolver.requests[resolver.head] = displayRequest{}
			resolver.head = (resolver.head + 1) % len(resolver.requests)
			resolver.count--
			resolver.inflight = request
			resolver.mu.Unlock()
			resolver.lookup(ctx, request)
			resolver.mu.Lock()
			resolver.inflight = displayRequest{}
			if resolver.count > 0 {
				signalRealtimeSource(resolver.wake)
			}
			resolver.mu.Unlock()
		}
	}
}

func (resolver *displayResolver) contactSeen(jid types.JID) bool {
	for i := 0; i < resolver.contactCount; i++ {
		if resolver.contacts[i] == jid {
			return true
		}
	}
	return false
}

func (resolver *displayResolver) lookup(parent context.Context, request displayRequest) {
	if parent.Err() != nil {
		return
	}
	jid, err := types.ParseJID(request.id.String())
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, displayLookupTimeout)
	defer cancel()
	if jid.Server == types.GroupServer {
		resolver.lookupGroup(ctx, request.id, jid)
		return
	}
	if _, direct := directChatJID(request.id); !direct {
		return
	}
	alt := request.alternate
	if alt.String() == "" {
		alt = resolver.aliases.alternateFor(request.id)
	}
	if alt.String() == "" && !resolver.contactSeen(jid) && resolver.contactCount < len(resolver.contacts) {
		alt, _ = lookupAlternate(ctx, request.id, alt, resolver.alternate)
	}
	ids := [2]model.ChatID{request.id, alt}
	// Prefer the PN contact-store record on equal quality, independently of
	// which authoritative spelling triggered the lookup.
	if jid.Server == types.DefaultUserServer && alt.String() != "" {
		ids[0], ids[1] = ids[1], ids[0]
	}
	before := [2]model.DisplayMetadata{resolver.cached(ids[0]), resolver.cached(ids[1])}
	best, _ := model.NewDisplayMetadata(request.id.String(), "", model.DisplayOpaque, false)
	for _, id := range ids {
		if id.String() == "" {
			continue
		}
		known := resolver.cached(id)
		candidate, _ := model.NewDisplayMetadata(request.id.String(), known.Name(), known.Quality(), false)
		best = best.Merge(candidate)
		person, valid := directChatJID(id)
		if !valid {
			continue
		}
		if fallback := readablePhoneFallback(id); fallback != "" {
			candidate, _ = model.NewDisplayMetadata(request.id.String(), fallback, model.DisplayPhone, false)
			best = best.Merge(candidate)
		}
		if resolver.contact == nil || resolver.contactSeen(person) || resolver.contactCount == len(resolver.contacts) {
			continue
		}
		resolver.contacts[resolver.contactCount] = person
		resolver.contactCount++
		contact, err := resolver.contact(ctx, person)
		if err != nil {
			continue
		}
		name, quality := contactDisplayName(contact)
		candidate, _ = model.NewDisplayMetadata(request.id.String(), name, quality, false)
		best = best.Merge(candidate)
	}
	// Publish labels for both authoritative spellings, not another alias
	// registry. This also updates existing group messages carrying either ID.
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	for index, id := range ids {
		if id.String() == "" {
			continue
		}
		if i := resolver.find(id); i >= 0 && resolver.slots[i].value != before[index] {
			// A newer live observation wins over an in-flight local-store read
			// for both aliases, even when that observation knew only one ID.
			current := resolver.slots[i].value
			value, _ := model.NewDisplayMetadata(request.id.String(), current.Name(), current.Quality(), false)
			best = best.Merge(value)
		}
	}
	for _, id := range ids {
		if id.String() == "" {
			continue
		}
		value, _ := model.NewDisplayMetadata(id.String(), best.Name(), best.Quality(), false)
		resolver.offerLocked(value, false)
	}
}

func (resolver *displayResolver) lookupGroup(ctx context.Context, id model.ChatID, jid types.JID) {
	if resolver.group == nil {
		return
	}
	resolver.mu.Lock()
	// Checking the cache and registering the attempt must be atomic with live
	// subject observations, including one arriving just before lookup admission.
	if i := resolver.find(id); i >= 0 && resolver.slots[i].value.Quality() == model.DisplayGroup {
		resolver.mu.Unlock()
		return
	}
	for i := 0; i < resolver.groupCount; i++ {
		if resolver.groups[i].id == id {
			resolver.mu.Unlock()
			return
		}
	}
	if resolver.groupCount == len(resolver.groups) {
		resolver.mu.Unlock()
		return
	}
	index := resolver.groupCount
	resolver.groupCount++
	resolver.groups[index] = groupLookupRecord{id: id}
	resolver.mu.Unlock()
	name, err := resolver.group(ctx, jid)
	if err != nil {
		return
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if !resolver.groups[index].liveSubject {
		value, _ := model.NewDisplayMetadata(id.String(), name, model.DisplayGroup, true)
		resolver.offerLocked(value, false)
	}
}

func contactDisplayName(contact types.ContactInfo) (string, model.DisplayQuality) {
	for _, entry := range []struct {
		name    string
		quality model.DisplayQuality
	}{{contact.FullName, model.DisplaySaved}, {contact.FirstName, model.DisplaySaved}, {contact.BusinessName, model.DisplayBusiness}, {contact.PushName, model.DisplayPush}} {
		value, _ := model.NewDisplayMetadata("contact", entry.name, entry.quality, false)
		if value.Name() != "" {
			return value.Name(), value.Quality()
		}
	}
	return "", model.DisplayOpaque
}

func readablePhoneFallback(ids ...model.ChatID) string {
	for _, id := range ids {
		jid, direct := directChatJID(id)
		if direct && jid.Server == types.DefaultUserServer && phoneUser(jid.User) {
			return "+" + jid.User
		}
	}
	return ""
}

// ReadableChatFallback keeps WhatsApp JID parsing inside the transport
// boundary while allowing an already-persisted empty-name PN chat to render a
// validated phone number. Opaque identities, including unmapped LIDs, remain
// unchanged and can still be improved by later display metadata.
func ReadableChatFallback(id model.ChatID) string {
	if phone := readablePhoneFallback(id); phone != "" {
		return phone
	}
	return id.String()
}

func phoneUser(user string) bool {
	if len(user) == 0 || len(user) > 15 {
		return false
	}
	for _, r := range user {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
