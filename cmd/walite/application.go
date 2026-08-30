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
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
	"github.com/gdamore/tcell/v2"
)

var errServiceStopped = errors.New("service stopped before application shutdown")
var errReplySendUnsupported = errors.New("reply send is not supported by the current message model")

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
					return connection, nil
				},
				newService:    newOfflineApplicationService,
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
	return runStartedApplication(ctx, screen, optionsFromConfig(settings), serviceCore, dependencies.runTUI)
}

func optionsFromConfig(settings config.UI) tui.Options {
	return tui.Options{
		Theme:          string(settings.Theme),
		ShowTimestamps: settings.ShowTimestamps,
		ConfirmQuit:    settings.ConfirmQuit,
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

	tuiDone := make(chan error, 1)
	go func() {
		tuiDone <- runTUI(runCtx, screen, tui.Input{
			Options: options, InitialState: initialState, LiveEvents: liveMessages,
			Send: func(ctx context.Context, request tui.SendRequest) error {
				return sendTextFromTUI(ctx, serviceCore, request)
			},
		})
	}()

	select {
	case tuiErr := <-tuiDone:
		cancel()
		serviceErr := <-serviceDone
		<-liveDone
		<-updatesDone
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
		return applicationResultAfterService(parent, serviceErr, tuiErr)
	}
}

func sendTextFromTUI(ctx context.Context, application applicationService, request tui.SendRequest) error {
	if request.ReplyToID != "" {
		return errReplySendUnsupported
	}
	sender, ok := application.(applicationTextSender)
	if !ok {
		return errors.New("application send unavailable")
	}
	serviceRequest, err := service.NewSendTextRequest(request.ChatID, request.Text)
	if err != nil {
		return err
	}
	return sender.SendText(ctx, serviceRequest)
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
