package main

import (
	"context"
	"sync"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type localReadApplication interface {
	MarkChatLocallyRead(context.Context, model.ChatID, time.Time) error
}

// cacheLoader is one owned worker with a one-value wake mailbox. Summary
// refreshes coalesce, and only the newest pending selection request is kept.
type cacheLoader struct {
	source applicationService

	mu             sync.Mutex
	summaryPending bool
	chatPending    bool
	chatRequest    tui.ChatLoadRequest
	localReader    localReadApplication
	readQueue      [tui.ChatWorkingSetCapacity]tui.LocalReadRequest
	readHead       int
	readCount      int
	wake           chan struct{}
	summaries      chan tui.InitialState
	chats          chan tui.ChatLoadResult
}

func newCacheLoader(source applicationService) *cacheLoader {
	localReader, _ := source.(localReadApplication)
	return &cacheLoader{
		source: source, localReader: localReader, wake: make(chan struct{}, 1),
		summaries: make(chan tui.InitialState, 1), chats: make(chan tui.ChatLoadResult, 1),
	}
}

func (loader *cacheLoader) requestLocalRead(request tui.LocalReadRequest) bool {
	if loader == nil || loader.localReader == nil {
		return false
	}
	id, err := model.NewChatID(request.ChatID)
	if err != nil {
		return false
	}
	request.ChatID = id.String()
	loader.mu.Lock()
	for offset := 0; offset < loader.readCount; offset++ {
		index := (loader.readHead + offset) % len(loader.readQueue)
		if loader.readQueue[index].ChatID == request.ChatID {
			if request.ActivityTime.After(loader.readQueue[index].ActivityTime) {
				loader.readQueue[index].ActivityTime = request.ActivityTime
			}
			loader.mu.Unlock()
			return true
		}
	}
	if loader.readCount == len(loader.readQueue) {
		loader.mu.Unlock()
		return false
	}
	index := (loader.readHead + loader.readCount) % len(loader.readQueue)
	loader.readQueue[index] = request
	loader.readCount++
	loader.mu.Unlock()
	loader.signal()
	return true
}

func (loader *cacheLoader) requestSummary() {
	if loader == nil {
		return
	}
	loader.mu.Lock()
	loader.summaryPending = true
	loader.mu.Unlock()
	loader.signal()
}

func (loader *cacheLoader) requestChat(request tui.ChatLoadRequest) bool {
	if loader == nil || request.ChatID == "" {
		return false
	}
	loader.mu.Lock()
	loader.chatRequest = request
	loader.chatPending = true
	loader.mu.Unlock()
	loader.signal()
	return true
}

func (loader *cacheLoader) signal() {
	select {
	case loader.wake <- struct{}{}:
	default:
	}
}

func (loader *cacheLoader) take() (summary bool, request tui.ChatLoadRequest, chat bool, localRead tui.LocalReadRequest) {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	if loader.readCount > 0 {
		localRead = loader.readQueue[loader.readHead]
		loader.readQueue[loader.readHead] = tui.LocalReadRequest{}
		loader.readHead = (loader.readHead + 1) % len(loader.readQueue)
		loader.readCount--
		return false, tui.ChatLoadRequest{}, false, localRead
	}
	if loader.summaryPending {
		loader.summaryPending = false
		return true, tui.ChatLoadRequest{}, false, tui.LocalReadRequest{}
	}
	if loader.chatPending {
		loader.chatPending = false
		return false, loader.chatRequest, true, tui.LocalReadRequest{}
	}
	return false, tui.ChatLoadRequest{}, false, tui.LocalReadRequest{}
}

func (loader *cacheLoader) persistLocalRead(request tui.LocalReadRequest) {
	if loader.localReader == nil || request.ChatID == "" {
		return
	}
	id, err := model.NewChatID(request.ChatID)
	if err == nil {
		_ = loader.localReader.MarkChatLocallyRead(context.Background(), id, request.ActivityTime)
	}
}

func (loader *cacheLoader) drainLocalReads() {
	for {
		_, _, _, request := loader.take()
		if request.ChatID == "" {
			return
		}
		loader.persistLocalRead(request)
	}
}

func (loader *cacheLoader) run(ctx context.Context) {
	defer close(loader.summaries)
	defer close(loader.chats)
	for {
		select {
		case <-ctx.Done():
			loader.drainLocalReads()
			return
		case <-loader.wake:
		}
		for {
			summary, request, chat, localRead := loader.take()
			if !summary && !chat && localRead.ChatID == "" {
				break
			}
			if localRead.ChatID != "" {
				loader.persistLocalRead(localRead)
				continue
			}
			if summary {
				state, err := buildInitialTUIState(ctx, loader.source)
				if err == nil {
					replaceSummary(loader.summaries, state)
				}
				continue
			}
			result, err := buildChatLoadResult(ctx, loader.source, request)
			if err == nil {
				replaceChatLoad(loader.chats, result)
			}
		}
	}
}

func replaceSummary(destination chan tui.InitialState, value tui.InitialState) {
	select {
	case <-destination:
	default:
	}
	select {
	case destination <- value:
	default:
	}
}

func replaceChatLoad(destination chan tui.ChatLoadResult, value tui.ChatLoadResult) {
	select {
	case <-destination:
	default:
	}
	select {
	case destination <- value:
	default:
	}
}
