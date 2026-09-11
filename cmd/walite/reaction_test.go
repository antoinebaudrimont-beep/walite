package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type fakeApplicationReactionSender struct {
	calls                atomic.Int32
	validateErr, sendErr error
	at                   time.Time
}

type reactionEventSource struct {
	service.EventSource
	reactions <-chan model.Reaction
}

func (source *reactionEventSource) ReactionEvents() <-chan model.Reaction { return source.reactions }

func (sender *fakeApplicationReactionSender) ValidateReaction(model.SendReactionRequest) error {
	return sender.validateErr
}
func (sender *fakeApplicationReactionSender) SendReaction(_ context.Context, request model.SendReactionRequest) (model.Reaction, error) {
	sender.calls.Add(1)
	if sender.sendErr != nil {
		return model.Reaction{}, sender.sendErr
	}
	return model.NewReaction(model.ReactionInput{
		ChatID: request.ChatID().String(), TargetMessageID: request.TargetMessageID().String(),
		ReactorID: model.SelfReactorID, Emoji: request.Emoji(), UpdatedAt: sender.at,
	})
}

type reactionWorkerApplication struct {
	*workerTestApplication
	sender *fakeApplicationReactionSender
}

func (application *reactionWorkerApplication) ValidateReaction(request model.SendReactionRequest) error {
	return application.sender.ValidateReaction(request)
}
func (application *reactionWorkerApplication) SendReaction(ctx context.Context, request model.SendReactionRequest) error {
	_, err := application.sender.SendReaction(ctx, request)
	return err
}

func TestSendWorkerReactionCallsTransportExactlyOnce(t *testing.T) {
	application := &reactionWorkerApplication{workerTestApplication: newWorkerTestApplication(), sender: &fakeApplicationReactionSender{at: time.Now().UTC()}}
	worker := newSendWorker(context.Background(), application)
	request := tui.SendRequest{ChatID: "123@lid", ReactionTargetID: "target", ReactionEmoji: "👍🏽"}
	if err := worker.admit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	result := <-worker.results
	worker.stop()
	if result.Failed || !result.Reaction || application.sender.calls.Load() != 1 || application.calls.Load() != 0 {
		t.Fatalf("result=%+v reaction calls=%d text calls=%d", result, application.sender.calls.Load(), application.calls.Load())
	}
}

func TestConnectedReactionSuccessPersistsAndRestartReconstructsPresentation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	cache, err := store.OpenSQLite(context.Background(), store.SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	message, _ := model.NewMessage(model.MessageInput{ChatID: "123@lid", MessageID: "target", SentAt: at.Add(-time.Minute), Text: "hello"})
	if err := cache.PutMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	sender := &fakeApplicationReactionSender{at: at}
	application := &connectedApplicationService{store: cache, reactions: sender, reactionUpdates: make(chan model.ReactionSummary, 64)}
	request, _ := model.NewSendReactionRequest("123@lid", "target", "", false, false, "❤️")
	if err := application.SendReaction(context.Background(), request); err != nil || sender.calls.Load() != 1 {
		t.Fatalf("send err=%v calls=%d", err, sender.calls.Load())
	}
	if update := <-application.reactionUpdates; update.Len() != 1 {
		t.Fatalf("live groups=%d", update.Len())
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	cache, err = store.OpenSQLite(context.Background(), store.SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	application.store = cache
	loaded, err := buildChatLoadResult(context.Background(), application, tui.ChatLoadRequest{ChatID: "123@lid"})
	if err != nil || len(loaded.Messages) != 1 || len(loaded.Messages[0].Reactions) != 1 || loaded.Messages[0].Reactions[0].Emoji != "❤️" || !loaded.Messages[0].Reactions[0].Own {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestConnectedReactionTransportFailureDoesNotCommit(t *testing.T) {
	cache, err := store.OpenSQLite(context.Background(), store.SQLiteOptions{Path: filepath.Join(t.TempDir(), "cache.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	sender := &fakeApplicationReactionSender{sendErr: errors.New("synthetic transport failure")}
	application := &connectedApplicationService{store: cache, reactions: sender, reactionUpdates: make(chan model.ReactionSummary, 64)}
	request, _ := model.NewSendReactionRequest("123@lid", "target", "", false, false, "👍")
	if err := application.SendReaction(context.Background(), request); err == nil {
		t.Fatal("transport failure accepted")
	}
	summary, err := cache.ReactionSummary(context.Background(), request.ChatID(), request.TargetMessageID())
	if err != nil || summary.Len() != 0 || sender.calls.Load() != 1 {
		t.Fatalf("summary=%d calls=%d err=%v", summary.Len(), sender.calls.Load(), err)
	}
}

func TestConnectedIncomingReactionStreamAppliesLatestValueWithoutDuplicates(t *testing.T) {
	cache, err := store.OpenSQLite(context.Background(), store.SQLiteOptions{Path: filepath.Join(t.TempDir(), "cache.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	message, _ := model.NewMessage(model.MessageInput{ChatID: "123@lid", MessageID: "target", SentAt: base, Text: "hello"})
	if err := cache.PutMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	reactions := make(chan model.Reaction, 4)
	for _, input := range []model.ReactionInput{
		{ChatID: "123@lid", TargetMessageID: "target", ReactorID: "peer@lid", Emoji: "👍", UpdatedAt: base.Add(time.Second)},
		{ChatID: "123@lid", TargetMessageID: "target", ReactorID: "peer@lid", Emoji: "👍", UpdatedAt: base.Add(time.Second)},
		{ChatID: "123@lid", TargetMessageID: "target", ReactorID: "peer@lid", Emoji: "❤️", UpdatedAt: base.Add(2 * time.Second)},
		{ChatID: "123@lid", TargetMessageID: "target", ReactorID: "peer@lid", Emoji: "", UpdatedAt: base.Add(3 * time.Second)},
	} {
		reaction, reactionErr := model.NewReaction(input)
		if reactionErr != nil {
			t.Fatal(reactionErr)
		}
		reactions <- reaction
	}
	close(reactions)
	application := &connectedApplicationService{
		store: cache, source: &reactionEventSource{reactions: reactions}, reactionUpdates: make(chan model.ReactionSummary, 4),
	}
	if err := application.runReactions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(application.reactionUpdates) != 4 {
		t.Fatalf("updates=%d", len(application.reactionUpdates))
	}
	for index, wantEmoji := range []string{"👍", "👍", "❤️", ""} {
		update := <-application.reactionUpdates
		if wantEmoji == "" {
			if update.Len() != 0 {
				t.Fatalf("update %d groups=%d", index, update.Len())
			}
			continue
		}
		group, ok := update.At(0)
		if update.Len() != 1 || !ok || group.Emoji() != wantEmoji || group.Count() != 1 {
			t.Fatalf("update %d group=%+v ok=%t", index, group, ok)
		}
	}
	summary, err := cache.ReactionSummary(context.Background(), message.ChatID(), message.MessageID())
	if err != nil || summary.Len() != 0 {
		t.Fatalf("groups=%d err=%v", summary.Len(), err)
	}
}
