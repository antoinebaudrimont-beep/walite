package wa

import (
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func (resolver *displayResolver) observePerson(jid, alternate types.JID, name string, quality model.DisplayQuality) {
	if resolver == nil {
		return
	}
	id, err := model.NewChatID(jid.ToNonAD().String())
	if err != nil {
		return
	}
	if _, direct := directChatJID(id); !direct {
		return
	}
	alt := authoritativeAlternate(id, alternate)
	if alt.String() == "" {
		alt = resolver.aliases.alternateFor(id)
	}
	value, _ := model.NewDisplayMetadata(id.String(), name, quality, false)
	resolver.offer(value, false)
	if alt.String() != "" {
		other, _ := model.NewDisplayMetadata(alt.String(), name, quality, false)
		resolver.offer(other, false)
	}
	resolver.request(displayRequest{id: id, alternate: alt})
}

func (resolver *displayResolver) observeGroup(jid types.JID, name string, live bool) {
	if resolver == nil || jid.Server != types.GroupServer {
		return
	}
	value, err := model.NewDisplayMetadata(jid.String(), name, model.DisplayGroup, true)
	if err != nil {
		return
	}
	resolver.offer(value, live)
	if name == "" {
		resolver.request(displayRequest{id: value.ID()})
	}
}

func (resolver *displayResolver) observeMessage(info types.MessageInfo) {
	if resolver == nil {
		return
	}
	if info.Chat.Server == types.GroupServer {
		resolver.observeGroup(info.Chat, "", false)
		if !info.IsFromMe {
			resolver.observePerson(info.Sender, info.SenderAlt, info.PushName, model.DisplayPush)
		}
	} else if info.IsFromMe {
		resolver.observePerson(info.Chat, info.RecipientAlt, "", model.DisplayOpaque)
	} else {
		resolver.observePerson(info.Chat, info.SenderAlt, info.PushName, model.DisplayPush)
	}
}

func (resolver *displayResolver) observeEvent(raw any) {
	if resolver == nil {
		return
	}
	switch event := raw.(type) {
	case *events.Contact:
		if event == nil || event.Action == nil || event.FromFullSync {
			return
		}
		name := event.Action.GetFullName()
		if name == "" {
			name = event.Action.GetFirstName()
		}
		resolver.observePerson(event.JID, types.EmptyJID, name, model.DisplaySaved)
	case *events.PushName:
		if event != nil {
			resolver.observePerson(event.JID, event.JIDAlt, event.NewPushName, model.DisplayPush)
		}
	case *events.BusinessName:
		if event != nil {
			resolver.observePerson(event.JID, types.EmptyJID, event.NewBusinessName, model.DisplayBusiness)
		}
	case *events.GroupInfo:
		if event != nil && event.Name != nil {
			resolver.observeGroup(event.JID, event.Name.Name, true)
		}
	case *events.JoinedGroup:
		if event != nil {
			resolver.observeGroup(event.JID, event.Name, true)
		}
	}
}
