package wa

import (
	"context"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/types"
)

const (
	DisplayRequestCapacity    = DisplayContactLookupLimit
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

// requestPeople admits only transport-valid direct identities. It is used for
// cached summaries and bounded selected-chat pages; rendering never performs a
// lookup and overflow remains advisory.
func (resolver *displayResolver) requestPeople(ids []model.ChatID) {
	for _, id := range ids {
		if _, direct := directChatJID(id); direct {
			resolver.request(displayRequest{id: id})
		}
	}
}

type groupLookupRecord struct {
	id          model.ChatID
	liveSubject bool
}

type contactLookupRecord struct {
	jid       types.JID
	alternate types.JID
	name      string
	quality   model.DisplayQuality
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
	contacts         [DisplayContactLookupLimit]contactLookupRecord // fixed, no eviction
	contactCount     int
	aliases          *chatAliases
	alternate        alternateJIDLookup
	contact          func(context.Context, types.JID) (types.ContactInfo, error)
	group            func(context.Context, types.JID) (string, error)
}

func newDisplayResolver(aliases *chatAliases, alternate alternateJIDLookup, contact func(context.Context, types.JID) (types.ContactInfo, error), group func(context.Context, types.JID) (string, error)) *displayResolver {
	return &displayResolver{
		aliases: aliases, alternate: alternate, contact: contact, group: group,
		wake: make(chan struct{}, 1), updates: make(chan model.DisplayMetadata, model.DisplayMetadataCapacity),
	}
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
		// Drain discovered metadata before starting another local lookup. The
		// fixed presentation slots are intentionally smaller than the fixed
		// contact-read registry; prioritizing output prevents a one-shot startup
		// name from being overwritten before the application can persist it.
		if out != nil {
			select {
			case out <- slot.value:
				resolver.mu.Lock()
				if resolver.slots[index].revision == slot.revision {
					resolver.slots[index].dirty = false
				}
				resolver.outputNext = (index + 1) % len(resolver.slots)
				resolver.mu.Unlock()
			case <-ctx.Done():
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
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

func (resolver *displayResolver) contactRecord(jid types.JID) (contactLookupRecord, bool) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	for i := 0; i < resolver.contactCount; i++ {
		if resolver.contacts[i].jid == jid {
			return resolver.contacts[i], true
		}
	}
	return contactLookupRecord{}, false
}

func (resolver *displayResolver) contactCapacityAvailable() bool {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	return resolver.contactCount < len(resolver.contacts)
}

func (resolver *displayResolver) rememberContactLocked(jid, alternate types.JID, value model.DisplayMetadata, add bool) bool {
	index := -1
	for i := 0; i < resolver.contactCount; i++ {
		if resolver.contacts[i].jid == jid {
			index = i
			break
		}
	}
	if index < 0 {
		if !add || resolver.contactCount == len(resolver.contacts) {
			return false
		}
		index = resolver.contactCount
		resolver.contactCount++
		resolver.contacts[index].jid = jid
	}
	record := &resolver.contacts[index]
	if !alternate.IsEmpty() {
		record.alternate = alternate
	}
	current, _ := model.NewDisplayMetadata(jid.ToNonAD().String(), record.name, record.quality, false)
	next, _ := model.NewDisplayMetadata(jid.ToNonAD().String(), value.Name(), value.Quality(), false)
	merged := current.Merge(next)
	record.name, record.quality = merged.Name(), merged.Quality()
	return true
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
	record, seen := resolver.contactRecord(jid)
	if alt.String() == "" && seen && !record.alternate.IsEmpty() {
		alt, _ = model.NewChatID(record.alternate.ToNonAD().String())
	}
	if alt.String() == "" && !seen && resolver.contactCapacityAvailable() {
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
		record, seen := resolver.contactRecord(person)
		if seen {
			candidate, _ = model.NewDisplayMetadata(request.id.String(), record.name, record.quality, false)
			best = best.Merge(candidate)
			continue
		}
		if resolver.contact == nil || !resolver.contactCapacityAvailable() {
			continue
		}
		resolver.mu.Lock()
		reserved := resolver.rememberContactLocked(person, types.EmptyJID, model.DisplayMetadata{}, true)
		resolver.mu.Unlock()
		if !reserved {
			continue
		}
		contact, err := resolver.contact(ctx, person)
		if err != nil {
			continue
		}
		name, quality := contactDisplayName(contact)
		candidate, _ = model.NewDisplayMetadata(request.id.String(), name, quality, false)
		best = best.Merge(candidate)
		resolver.mu.Lock()
		resolver.rememberContactLocked(person, types.EmptyJID, candidate, false)
		resolver.mu.Unlock()
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
	if resolver.contact != nil {
		for index, id := range ids {
			person, valid := directChatJID(id)
			if !valid {
				continue
			}
			alternate := types.EmptyJID
			if other, valid := directChatJID(ids[1-index]); valid {
				alternate = other
			}
			resolver.rememberContactLocked(person, alternate, best, false)
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
