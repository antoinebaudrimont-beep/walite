package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

const (
	chatStateVersion  = 1
	maxChatStateBytes = 4 * 1024 * 1024
	maxChatTitleBytes = 4 * 1024
	maxTimestampBytes = 256
)

var errCorruptChatState = errors.New("corrupt chat state")

// ChatStateStore separates the TUI lifecycle from the state storage format.
type ChatStateStore interface {
	Load() (*chatState, error)
	Save(*chatState) error
}

type fileChatStateStore struct {
	path       string
	renameFile func(string, string) error
}

type chatStateFile struct {
	Version       int        `json:"version"`
	SelectedChat  int        `json:"selectedChat"`
	NextMessageID uint32     `json:"nextMessageId"`
	NextActivity  uint64     `json:"nextActivity"`
	Chats         []chatFile `json:"chats"`
}

type chatFile struct {
	Title       string        `json:"title"`
	UnreadCount uint16        `json:"unreadCount"`
	Activity    uint64        `json:"activity"`
	Messages    []messageFile `json:"messages"`
}

type messageFile struct {
	ID        uint32 `json:"id"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
	ReplyToID uint32 `json:"replyToId,omitempty"`
	HasReply  bool   `json:"hasReply,omitempty"`
}

func newFileChatStateStore(path string) ChatStateStore {
	return &fileChatStateStore{path: path, renameFile: os.Rename}
}

func newDefaultChatStateStore() (ChatStateStore, error) {
	path, err := defaultChatStatePath()
	if err != nil {
		return nil, err
	}
	return newFileChatStateStore(path), nil
}

func defaultChatStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "walite", "state.json"), nil
}

func loadChatStateOrDemo(store ChatStateStore) (*chatState, error) {
	if store == nil {
		return newDemoChatState(), nil
	}
	state, err := store.Load()
	if errors.Is(err, os.ErrNotExist) {
		return newDemoChatState(), nil
	}
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("%w: store returned no state", errCorruptChatState)
	}
	return state, nil
}

func (store *fileChatStateStore) Load() (*chatState, error) {
	if store == nil || store.path == "" {
		return nil, errors.New("invalid chat state path")
	}
	file, err := os.Open(store.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxChatStateBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxChatStateBytes {
		return nil, fmt.Errorf("%w: file exceeds %d bytes", errCorruptChatState, maxChatStateBytes)
	}
	var saved chatStateFile
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("%w: %v", errCorruptChatState, err)
	}
	state, err := saved.chatState()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errCorruptChatState, err)
	}
	return state, nil
}

func (store *fileChatStateStore) Save(state *chatState) error {
	if store == nil || store.path == "" {
		return errors.New("invalid chat state path")
	}
	saved, err := newChatStateFile(state)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxChatStateBytes {
		return fmt.Errorf("chat state exceeds %d bytes", maxChatStateBytes)
	}
	renameFile := store.renameFile
	if renameFile == nil {
		renameFile = os.Rename
	}
	return writeAtomicStateFile(store.path, data, renameFile)
}

func newChatStateFile(state *chatState) (chatStateFile, error) {
	if err := validateChatState(state); err != nil {
		return chatStateFile{}, err
	}
	saved := chatStateFile{
		Version:       chatStateVersion,
		SelectedChat:  state.selected,
		NextMessageID: uint32(state.nextMessageID),
		NextActivity:  state.nextActivity,
		Chats:         make([]chatFile, 0, state.chatCount),
	}
	for chatIndex := 0; chatIndex < state.chatCount; chatIndex++ {
		chat := &state.chats[chatIndex]
		savedChat := chatFile{
			Title:       chat.title,
			UnreadCount: chat.unreadCount,
			Activity:    chat.activity,
			Messages:    make([]messageFile, 0, chat.messageCount),
		}
		for messageIndex := 0; messageIndex < chat.messageCount; messageIndex++ {
			message := chat.messages[messageIndex]
			savedChat.Messages = append(savedChat.Messages, messageFile{
				ID:        uint32(message.id),
				Timestamp: message.time,
				Text:      message.text,
				ReplyToID: uint32(message.replyToID),
				HasReply:  message.hasReply,
			})
		}
		saved.Chats = append(saved.Chats, savedChat)
	}
	return saved, nil
}

func (saved chatStateFile) chatState() (*chatState, error) {
	if saved.Version != chatStateVersion {
		return nil, fmt.Errorf("unsupported version %d", saved.Version)
	}
	if len(saved.Chats) < 1 || len(saved.Chats) > maxChats {
		return nil, fmt.Errorf("chat count %d is out of range", len(saved.Chats))
	}
	state := &chatState{
		chatCount:     len(saved.Chats),
		selected:      saved.SelectedChat,
		nextMessageID: messageID(saved.NextMessageID),
		nextActivity:  saved.NextActivity,
	}
	for chatIndex, savedChat := range saved.Chats {
		if len(savedChat.Messages) > maxMessages {
			return nil, fmt.Errorf("chat %d has too many messages", chatIndex)
		}
		chat := &state.chats[chatIndex]
		chat.title = savedChat.Title
		chat.unreadCount = savedChat.UnreadCount
		chat.activity = savedChat.Activity
		chat.messageCount = len(savedChat.Messages)
		for messageIndex, savedMessage := range savedChat.Messages {
			chat.messages[messageIndex] = messageView{
				id:        messageID(savedMessage.ID),
				time:      savedMessage.Timestamp,
				text:      savedMessage.Text,
				replyToID: messageID(savedMessage.ReplyToID),
				hasReply:  savedMessage.HasReply,
			}
		}
	}
	if err := validateChatState(state); err != nil {
		return nil, err
	}
	return state, nil
}

func validateChatState(state *chatState) error {
	if state == nil {
		return errors.New("invalid nil chat state")
	}
	if state.chatCount < 1 || state.chatCount > len(state.chats) {
		return fmt.Errorf("invalid chat count %d", state.chatCount)
	}
	if state.selected < 0 || state.selected >= state.chatCount {
		return fmt.Errorf("invalid selected chat %d", state.selected)
	}
	seenIDs := make(map[messageID]struct{})
	var largestID messageID
	var largestActivity uint64
	for chatIndex := 0; chatIndex < state.chatCount; chatIndex++ {
		chat := &state.chats[chatIndex]
		if chat.title == "" || len(chat.title) > maxChatTitleBytes || !utf8.ValidString(chat.title) {
			return fmt.Errorf("invalid title for chat %d", chatIndex)
		}
		if chat.messageCount < 0 || chat.messageCount > len(chat.messages) {
			return fmt.Errorf("invalid message count for chat %d", chatIndex)
		}
		if chat.activity == 0 || chat.activity >= state.nextActivity {
			return fmt.Errorf("invalid activity for chat %d", chatIndex)
		}
		if chatIndex > 0 && state.chats[chatIndex-1].activity <= chat.activity {
			return fmt.Errorf("invalid activity order at chat %d", chatIndex)
		}
		if chat.activity > largestActivity {
			largestActivity = chat.activity
		}
		for messageIndex := 0; messageIndex < chat.messageCount; messageIndex++ {
			message := chat.messages[messageIndex]
			if message.id == 0 {
				return fmt.Errorf("missing message ID at chat %d message %d", chatIndex, messageIndex)
			}
			if _, exists := seenIDs[message.id]; exists {
				return fmt.Errorf("duplicate message ID %d", message.id)
			}
			seenIDs[message.id] = struct{}{}
			if message.id > largestID {
				largestID = message.id
			}
			if len(message.time) > maxTimestampBytes || !utf8.ValidString(message.time) {
				return fmt.Errorf("invalid timestamp at chat %d message %d", chatIndex, messageIndex)
			}
			if len(message.text) > maxDraftBytes || !utf8.ValidString(message.text) {
				return fmt.Errorf("invalid text at chat %d message %d", chatIndex, messageIndex)
			}
			if message.hasReply != (message.replyToID != 0) {
				return fmt.Errorf("invalid reply at chat %d message %d", chatIndex, messageIndex)
			}
		}
	}
	if state.nextMessageID == 0 || state.nextMessageID <= largestID {
		return fmt.Errorf("invalid next message ID %d", state.nextMessageID)
	}
	if state.nextActivity == 0 || state.nextActivity <= largestActivity {
		return fmt.Errorf("invalid next activity %d", state.nextActivity)
	}
	return nil
}

func writeAtomicStateFile(path string, data []byte, renameFile func(string, string) error) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".state-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := renameFile(temporaryPath, path); err != nil {
		return err
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryFile.Close()
	return directoryFile.Sync()
}
