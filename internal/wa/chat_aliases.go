package wa

import (
	"context"
	"errors"
	"sync"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/types"
)

// ChatAliasCapacity bounds session routing, not contacts or message IDs. No
// eviction is safe without knowing whether a chat is still visible/retained.
const ChatAliasCapacity = 128

var (
	ErrChatAliasesFull   = errors.New("WhatsApp session chat identity capacity reached")
	ErrChatAliasConflict = errors.New("WhatsApp chat identity conflict; merging established chats is not supported")
	ErrChatAliasLookup   = errors.New("WhatsApp chat identity lookup failed")
)

type alternateJIDLookup func(context.Context, types.JID) (types.JID, error)
type chatAlias struct{ primary, alternate model.ChatID }
type chatAliases struct {
	mu      sync.Mutex
	entries [ChatAliasCapacity]chatAlias
	count   int
}

func directChatJID(id model.ChatID) (types.JID, bool) {
	jid, err := textRecipient(id)
	return jid, err == nil && (jid.Server == types.DefaultUserServer || jid.Server == types.HiddenUserServer)
}

// authoritativeAlternate accepts only a PN/LID pair for the conversation,
// never a group's participant or a similar-looking numeric identifier.
func authoritativeAlternate(primary model.ChatID, alternate types.JID) model.ChatID {
	jid, direct := directChatJID(primary)
	if !direct {
		return model.ChatID{}
	}
	other, err := model.NewChatID(alternate.ToNonAD().String())
	if err != nil {
		return model.ChatID{}
	}
	altJID, altDirect := directChatJID(other)
	if !altDirect || jid.Server == altJID.Server {
		return model.ChatID{}
	}
	return other
}

func (aliases *chatAliases) resolve(primary, alternate model.ChatID) (model.ChatID, error) {
	if aliases == nil {
		return primary, nil
	} // isolated transport-only fixtures
	if _, direct := directChatJID(primary); !direct {
		return primary, nil
	}
	if alternate.String() != "" {
		jid, _ := types.ParseJID(alternate.String())
		alternate = authoritativeAlternate(primary, jid)
	}
	aliases.mu.Lock()
	defer aliases.mu.Unlock()
	first, second := -1, -1
	for index := 0; index < aliases.count; index++ {
		entry := aliases.entries[index]
		if entry.primary == primary || entry.alternate == primary {
			first = index
		}
		if alternate.String() != "" && (entry.primary == alternate || entry.alternate == alternate) {
			second = index
		}
	}
	if first >= 0 && second >= 0 && first != second {
		return model.ChatID{}, ErrChatAliasConflict
	}
	index := first
	if index < 0 {
		index = second
	}
	if index < 0 {
		if aliases.count == len(aliases.entries) {
			return model.ChatID{}, ErrChatAliasesFull
		}
		aliases.entries[aliases.count] = chatAlias{primary: primary, alternate: alternate}
		aliases.count++
		return primary, nil
	}
	entry := &aliases.entries[index]
	for _, id := range [2]model.ChatID{primary, alternate} {
		if id.String() == "" || id == entry.primary || id == entry.alternate {
			continue
		}
		if entry.alternate.String() != "" {
			return model.ChatID{}, ErrChatAliasConflict
		}
		entry.alternate = id
	}
	return entry.primary, nil
}

// lookupAlternate runs only on an existing source/sender worker. The network
// callback and UI never perform this session-store lookup. Explicit event
// metadata wins and avoids the lookup entirely.
func lookupAlternate(ctx context.Context, primary, alternate model.ChatID, lookup alternateJIDLookup) (model.ChatID, error) {
	jid, direct := directChatJID(primary)
	if alternate.String() != "" || !direct || lookup == nil {
		return alternate, nil
	}
	other, err := lookup(ctx, jid)
	if err != nil {
		if ctx.Err() != nil {
			return model.ChatID{}, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return model.ChatID{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return model.ChatID{}, context.DeadlineExceeded
		}
		return model.ChatID{}, ErrChatAliasLookup
	}
	return authoritativeAlternate(primary, other), nil
}
