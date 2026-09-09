package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
	"github.com/gdamore/tcell/v2"
)

var errServiceStopped = errors.New("service stopped before application shutdown")

const livePresentationCapacity = service.LiveEventCapacity

type applicationService interface {
	Run(context.Context) error
	Updates() <-chan model.Update
	LiveEvents() <-chan model.LiveEvent
	InitialChats(context.Context, int) ([]model.Chat, error)
	InitialMessages(context.Context, model.ChatID, int) ([]model.Message, error)
}

type applicationTextSender interface {
	SendText(context.Context, service.SendTextRequest) error
}

type cacheUpdateApplication interface {
	CacheUpdates() <-chan struct{}
}

type applicationDependencies struct {
	configuration config.UIStore
	newService    func() (applicationService, error)
	runTUI        func(context.Context, tcell.Screen, tui.Input) error
}

func run(ctx context.Context, screen tcell.Screen) error {
	configurationStore, err := config.NewDefaultUIStore()
	if err != nil {
		return fmt.Errorf("configuration store: %w", err)
	}
	sessionPath, err := config.DefaultWhatsAppSessionPath()
	if err != nil {
		return fmt.Errorf("WhatsApp session path: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("application executable: %w", err)
	}
	return runWithPairingWindow(ctx, screen, pairingStartupDependencies{
		sessionLinked: func(ctx context.Context) (bool, error) {
			return wa.SessionLinked(ctx, sessionPath)
		},
		launchPairing: func(ctx context.Context) error {
			return launchXFCEPairingWindow(ctx, executable, runPairingCommand)
		},
		runLinked: func(ctx context.Context, screen tcell.Screen) error {
			cachePath, err := config.DefaultApplicationCachePath()
			if err != nil {
				return fmt.Errorf("application cache path: %w", err)
			}
			cache, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: cachePath})
			if err != nil {
				return fmt.Errorf("open application cache: %w", err)
			}
			defer cache.Close()
			var realtimeSource *wa.RealtimeSource
			var textSender wa.TextSender
			var readReceiptSender wa.ReadReceiptSender
			var mediaDownloader wa.MediaDownloader
			return runAuthenticatedApplication(ctx, screen, authenticatedApplicationDependencies{
				configuration: configurationStore,
				newConnection: func(ctx context.Context) (applicationConnection, error) {
					connection, err := wa.NewConnection(ctx, sessionPath)
					if err != nil {
						return nil, err
					}
					if !connection.Linked() {
						_ = connection.Close()
						return nil, errPairingSessionUnlinked
					}
					realtimeSource = connection.RealtimeSource()
					textSender = connection.TextSender()
					readReceiptSender = connection.ReadReceiptSender()
					mediaDownloader = connection.MediaDownloader()
					if realtimeSource == nil {
						_ = connection.Close()
						return nil, errors.New("WhatsApp realtime source unavailable")
					}
					cachedChats, err := cache.ListChats(ctx, model.MaxChatSummaries)
					if err != nil {
						_ = connection.Close()
						return nil, fmt.Errorf("load cached chat identities: %w", err)
					}
					cachedIDs := make([]model.ChatID, len(cachedChats))
					for index, chat := range cachedChats {
						cachedIDs[index] = chat.ID()
					}
					if err := realtimeSource.SeedChatIDs(cachedIDs); err != nil {
						_ = connection.Close()
						return nil, fmt.Errorf("seed cached chat identities: %w", err)
					}
					return connection, nil
				},
				newService: func() (applicationService, error) {
					return newConnectedApplicationServiceWithMedia(realtimeSource, textSender, readReceiptSender, mediaDownloader, cache)
				},
				runConnection: tui.RunConnectionInitialized,
				runTUI:        tui.RunInitialized,
			})
		},
	})
}

func newOfflineApplicationService() (applicationService, error) {
	scenario, err := newDemoScenario(config.DefaultValues())
	if err != nil {
		return nil, err
	}
	chats, err := seedOfflineInitialSnapshot(context.Background(), scenario.store)
	if err != nil {
		return nil, err
	}
	return &offlineApplicationService{core: scenario.core, store: scenario.store, chats: chats}, nil
}

func runApplication(ctx context.Context, screen tcell.Screen, dependencies applicationDependencies) error {
	if ctx == nil || screen == nil || dependencies.configuration == nil ||
		dependencies.newService == nil || dependencies.runTUI == nil {
		return errors.New("application rejected")
	}
	settings, err := dependencies.configuration.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	serviceCore, err := dependencies.newService()
	if err != nil {
		return fmt.Errorf("construct service: %w", err)
	}
	if serviceCore == nil {
		return errors.New("construct service: no service")
	}
	return runStartedApplication(ctx, screen, optionsFromConfig(settings), serviceCore,
		func(ctx context.Context, screen tcell.Screen, input tui.Input) error {
			return runConfiguredTUI(ctx, screen, input, dependencies.configuration, dependencies.runTUI)
		})
}

func optionsFromConfig(settings config.UI) tui.Options {
	return tui.Options{
		Theme:            string(settings.Theme),
		ShowTimestamps:   settings.ShowTimestamps,
		ConfirmQuit:      settings.ConfirmQuit,
		SendReadReceipts: settings.SendReadReceipts,
	}
}

func runStartedApplication(
	parent context.Context,
	screen tcell.Screen,
	options tui.Options,
	serviceCore applicationService,
	runTUI func(context.Context, tcell.Screen, tui.Input) error,
) error {
	runCtx, cancel := context.WithCancel(parent)
	defer cancel()
	media, err := newDefaultMediaWorker(runCtx, serviceCore)
	if err != nil {
		return fmt.Errorf("construct media worker: %w", err)
	}
	defer media.stop()

	serviceDone := make(chan error, 1)
	go func() { serviceDone <- serviceCore.Run(runCtx) }()
	if err := awaitServiceReady(serviceCore.Updates(), serviceDone); err != nil {
		cancel()
		return err
	}
	initialState, err := buildInitialTUIState(runCtx, serviceCore)
	if err != nil {
		cancel()
		serviceErr := <-serviceDone
		if serviceErr != nil && !errors.Is(serviceErr, context.Canceled) {
			return errors.Join(fmt.Errorf("load initial snapshot: %w", err), fmt.Errorf("stop service: %w", serviceErr))
		}
		return fmt.Errorf("load initial snapshot: %w", err)
	}
	loader := newCacheLoader(serviceCore)
	loaderDone := make(chan struct{})
	go func() {
		defer close(loaderDone)
		loader.run(runCtx)
	}()
	cacheUpdatesDone := make(chan struct{})
	go func() {
		defer close(cacheUpdatesDone)
		application, ok := serviceCore.(cacheUpdateApplication)
		if !ok || application.CacheUpdates() == nil {
			return
		}
		for {
			select {
			case <-runCtx.Done():
				return
			case _, ok := <-application.CacheUpdates():
				if !ok {
					return
				}
				loader.requestSummary()
			}
		}
	}()

	updatesDone := make(chan struct{})
	go func() {
		defer close(updatesDone)
		for range serviceCore.Updates() {
		}
	}()
	liveMessages := make(chan tui.LiveMessage, livePresentationCapacity)
	liveDone := make(chan struct{})
	serviceLiveEvents := serviceCore.LiveEvents()
	go func() {
		defer close(liveDone)
		forwardLiveMessages(runCtx, serviceLiveEvents, liveMessages)
	}()

	sender := newSendWorker(runCtx, serviceCore)
	defer sender.stop()
	readReceipts := newReadReceiptWorker(runCtx, serviceCore)
	defer readReceipts.stop()
	var displayUpdates chan tui.DisplayMetadata
	if display, ok := serviceCore.(displayApplication); ok {
		displayUpdates = make(chan tui.DisplayMetadata, 1)
		done := make(chan struct{})
		go func() { defer close(done); forwardDisplayMetadata(runCtx, display, displayUpdates) }()
		defer func() { cancel(); <-done }()
	}
	tuiDone := make(chan error, 1)
	go func() {
		tuiDone <- runTUI(runCtx, screen, tui.Input{
			Options: options, InitialState: initialState, LiveEvents: liveMessages,
			Send: sender.admit, SendResults: sender.results,
			DisplayUpdates: displayUpdates,
			SummaryUpdates: loader.summaries, ChatLoads: loader.chats, LoadChat: loader.requestChat,
			PersistLocalRead: loader.requestLocalRead,
			SendReadReceipt:  readReceipts.admit,
			Media:            media.admit, MediaResults: media.results, CloseMedia: media.close,
			CloseExternalPreview: media.closeExternal,
		})
	}()

	select {
	case tuiErr := <-tuiDone:
		sender.stop()
		readReceipts.stop()
		cancel()
		serviceErr := <-serviceDone
		<-liveDone
		<-updatesDone
		<-loaderDone
		<-cacheUpdatesDone
		return applicationResultAfterTUI(parent, tuiErr, serviceErr)
	case serviceErr := <-serviceDone:
		var tuiErr error
		select {
		case <-liveDone:
			cancel()
			tuiErr = <-tuiDone
		case tuiErr = <-tuiDone:
			cancel()
			<-liveDone
		}
		<-updatesDone
		<-loaderDone
		<-cacheUpdatesDone
		return applicationResultAfterService(parent, serviceErr, tuiErr)
	}
}

func sendTextFromTUI(ctx context.Context, application applicationService, request tui.SendRequest) error {
	sender, ok := application.(applicationTextSender)
	if !ok {
		return errors.New("application send unavailable")
	}
	serviceRequest, err := sendRequestFromTUI(request)
	if err != nil {
		return err
	}
	return sender.SendText(ctx, serviceRequest)
}

func sendRequestFromTUI(request tui.SendRequest) (service.SendTextRequest, error) {
	if request.ReplyToID == "" {
		return service.NewSendTextRequest(request.ChatID, request.Text)
	}
	var quote model.TextQuote
	var err error
	if request.ReplyMediaKind == "" {
		quote, err = model.NewTextQuote(request.ReplyToID, request.ReplyToText, request.ReplyToFromMe)
	} else {
		kind, ok := presentationMediaKind(request.ReplyMediaKind)
		if !ok {
			return service.SendTextRequest{}, errors.New("application media quote rejected")
		}
		media, mediaErr := model.NewMedia(kind, request.ReplyMediaName, request.ReplyMediaMIME)
		if mediaErr != nil {
			return service.SendTextRequest{}, mediaErr
		}
		quote, err = model.NewMediaQuote(request.ReplyToID, request.ReplyToText, request.ReplyToFromMe, media)
	}
	if err != nil {
		return service.SendTextRequest{}, err
	}
	return service.NewSendTextRequest(request.ChatID, request.Text, quote)
}

func presentationMediaKind(value string) (model.MediaKind, bool) {
	switch value {
	case model.MediaImage.String():
		return model.MediaImage, true
	case model.MediaVideo.String():
		return model.MediaVideo, true
	case model.MediaDocument.String():
		return model.MediaDocument, true
	case model.MediaAudio.String():
		return model.MediaAudio, true
	case model.MediaSticker.String():
		return model.MediaSticker, true
	default:
		return 0, false
	}
}

func forwardLiveMessages(ctx context.Context, source <-chan model.LiveEvent, destination chan<- tui.LiveMessage) {
	defer close(destination)
	if source == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-source:
			if !ok {
				return
			}
			presentation, ok := adaptLiveMessage(event)
			if !ok {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case destination <- presentation:
			}
		}
	}
}

func adaptLiveMessage(event model.LiveEvent) (tui.LiveMessage, bool) {
	if event.Kind() != model.LiveMessageCommitted {
		return tui.LiveMessage{}, false
	}
	message := event.Message()
	chatID := strings.Clone(message.ChatID().String())
	messageID := strings.Clone(message.MessageID().String())
	text := strings.Clone(message.Text())
	if !message.BodyRetained() {
		text = ""
	}
	if chatID == "" || messageID == "" || message.SentAt().IsZero() || event.ActivityTime().IsZero() || event.ActivityTime().Before(message.SentAt()) {
		return tui.LiveMessage{}, false
	}
	return tui.LiveMessage{
		ChatID: chatID, MessageID: messageID, SentAt: message.SentAt(), FromMe: message.FromMe(),
		Text: text, BodyRetained: message.BodyRetained(), UnreadCount: event.UnreadCount(), ActivityTime: event.ActivityTime(),
		MediaKind: message.Media().Kind().String(), MediaName: message.Media().Name(), MediaMIME: message.Media().MIMEType(),
		ReplyToID: message.Quote().MessageID().String(), ReplyToText: message.Quote().Text(), ReplyToFromMe: message.Quote().FromMe(),
		ReplyMediaKind: message.Quote().Media().Kind().String(), ReplyMediaName: message.Quote().Media().Name(), ReplyMediaMIME: message.Quote().Media().MIMEType(),
		SenderID: message.SenderID().String(), IsGroup: message.IsGroup(),
	}, true
}

func awaitServiceReady(updates <-chan model.Update, serviceDone <-chan error) error {
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				serviceErr := <-serviceDone
				if serviceErr == nil {
					serviceErr = errServiceStopped
				}
				return fmt.Errorf("start service: %w", serviceErr)
			}
			if update.Kind() == model.UpdateReady {
				return nil
			}
		case serviceErr := <-serviceDone:
			if serviceErr == nil {
				serviceErr = errServiceStopped
			}
			return fmt.Errorf("start service: %w", serviceErr)
		}
	}
}

func applicationResultAfterTUI(parent context.Context, tuiErr, serviceErr error) error {
	if tuiErr != nil && !errors.Is(tuiErr, context.Canceled) {
		if serviceErr != nil && !errors.Is(serviceErr, context.Canceled) {
			return errors.Join(fmt.Errorf("run tui: %w", tuiErr), fmt.Errorf("stop service: %w", serviceErr))
		}
		return fmt.Errorf("run tui: %w", tuiErr)
	}
	if serviceErr != nil && !errors.Is(serviceErr, context.Canceled) {
		return fmt.Errorf("stop service: %w", serviceErr)
	}
	if err := parent.Err(); err != nil {
		return err
	}
	return nil
}

func applicationResultAfterService(parent context.Context, serviceErr, tuiErr error) error {
	if err := parent.Err(); err != nil && errors.Is(serviceErr, context.Canceled) {
		return err
	}
	if serviceErr == nil {
		serviceErr = errServiceStopped
	}
	serviceFailure := fmt.Errorf("run service: %w", serviceErr)
	if tuiErr != nil && !errors.Is(tuiErr, context.Canceled) {
		return errors.Join(serviceFailure, fmt.Errorf("run tui: %w", tuiErr))
	}
	return serviceFailure
}
