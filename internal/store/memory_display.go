package store

import (
	"context"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// ApplyDisplayMetadata is advisory and never inserts chats/messages or changes
// unread/activity. The fixed FIFO cache also covers people seen in groups.
func (memory *Memory) ApplyDisplayMetadata(ctx context.Context, metadata model.DisplayMetadata) (model.DisplayMetadata, error) {
	if err := checkContext(ctx); err != nil {
		return model.DisplayMetadata{}, err
	}
	owned, err := model.NewDisplayMetadata(metadata.ID().String(), metadata.Name(), metadata.Quality(), metadata.IsGroup())
	if err != nil {
		return model.DisplayMetadata{}, &Error{kind: rejected}
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	index := -1
	for i, entry := range memory.display {
		if entry.ID() == owned.ID() {
			index = i
			break
		}
	}
	if index < 0 {
		index = memory.displayNext
		memory.displayNext = (index + 1) % len(memory.display)
		memory.display[index] = model.DisplayMetadata{}
	}
	owned = memory.display[index].Merge(owned)
	if chat := memory.chats[owned.ID().String()]; chat != nil {
		if owned.Quality() < chat.displayQuality {
			owned, _ = model.NewDisplayMetadata(owned.ID().String(), chat.chat.DisplayName(), chat.displayQuality, chat.chat.IsGroup() || owned.IsGroup())
		}
		chat.chat = chat.chat.WithDisplayMetadata(owned)
		if owned.Name() != "" {
			chat.displayQuality = owned.Quality()
		}
	}
	memory.display[index] = owned
	return owned, nil
}

func (memory *Memory) displayFor(id model.ChatID) model.DisplayMetadata {
	for _, metadata := range memory.display {
		if metadata.ID() == id {
			return metadata
		}
	}
	return model.DisplayMetadata{}
}
