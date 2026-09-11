package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
	"github.com/gdamore/tcell/v2"
)

type applicationConnection interface {
	Linked() bool
	Updates() <-chan wa.ConnectionUpdate
	Run(context.Context) error
	Disconnect()
	Close() error
}

type authenticatedApplicationDependencies struct {
	configuration config.UIStore
	capabilities  *platformCapabilities
	newConnection func(context.Context) (applicationConnection, error)
	newService    func() (applicationService, error)
	runConnection func(context.Context, tcell.Screen, tui.ConnectionInput) error
	runTUI        func(context.Context, tcell.Screen, tui.Input) error
}

func runAuthenticatedApplication(
	parent context.Context,
	screen tcell.Screen,
	dependencies authenticatedApplicationDependencies,
) error {
	if parent == nil || screen == nil || dependencies.configuration == nil ||
		dependencies.newConnection == nil || dependencies.newService == nil ||
		dependencies.runConnection == nil || dependencies.runTUI == nil {
		return errors.New("authenticated application rejected")
	}
	settings, err := dependencies.configuration.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	connection, err := dependencies.newConnection(parent)
	if err != nil {
		return fmt.Errorf("construct WhatsApp connection: %w", err)
	}
	if connection == nil {
		return errors.New("construct WhatsApp connection: no connection")
	}
	defer connection.Close()

	if err := screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()

	runCtx, cancel := context.WithCancel(parent)
	defer cancel()
	connectionDone := make(chan error, 1)
	go func() {
		runErr := connection.Run(runCtx)
		connectionDone <- runErr
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			cancel()
		}
	}()

	presentationUpdates, presentationDone := forwardConnectionUpdates(runCtx, connection.Updates())
	viewErr := dependencies.runConnection(runCtx, screen, tui.ConnectionInput{
		Linked:  connection.Linked(),
		Updates: presentationUpdates,
	})
	if viewErr != nil {
		cancel()
		connectionErr := <-connectionDone
		<-presentationDone
		if errors.Is(viewErr, tui.ErrConnectionViewExit) {
			if parent.Err() != nil {
				return parent.Err()
			}
			return nil
		}
		if connectionErr != nil && !errors.Is(connectionErr, context.Canceled) {
			return fmt.Errorf("connect WhatsApp: %w", connectionErr)
		}
		if parent.Err() != nil {
			return parent.Err()
		}
		return fmt.Errorf("show WhatsApp connection: %w", viewErr)
	}

	serviceCore, err := dependencies.newService()
	if err != nil {
		cancel()
		<-connectionDone
		<-presentationDone
		return fmt.Errorf("construct service: %w", err)
	}
	if serviceCore == nil {
		cancel()
		<-connectionDone
		<-presentationDone
		return errors.New("construct service: no service")
	}

	viewExitedNormally := false
	capabilities := defaultPlatformCapabilities()
	if dependencies.capabilities != nil {
		capabilities = *dependencies.capabilities
	}
	applicationErr := runStartedApplicationWithCapabilities(
		runCtx,
		screen,
		optionsFromConfig(settings),
		serviceCore,
		func(ctx context.Context, screen tcell.Screen, input tui.Input) error {
			// Cancel the connection owner as soon as the view exits, before
			// joining its send worker. Disconnect releases upstream response
			// waiters even if a send is waiting behind an internal peer send.
			defer cancel()
			err := runConfiguredTUI(ctx, screen, input, dependencies.configuration, dependencies.runTUI)
			viewExitedNormally = err == nil
			return err
		},
		capabilities,
	)
	cancel()
	connectionErr := <-connectionDone
	<-presentationDone
	if connectionErr != nil && !errors.Is(connectionErr, context.Canceled) {
		return fmt.Errorf("WhatsApp connection stopped: %w", connectionErr)
	}
	if viewExitedNormally && parent.Err() == nil && applicationErr == context.Canceled {
		return nil // cancellation above is the normal user-quit shutdown signal
	}
	return applicationErr
}

func forwardConnectionUpdates(
	ctx context.Context,
	source <-chan wa.ConnectionUpdate,
) (<-chan tui.ConnectionUpdate, <-chan struct{}) {
	destination := make(chan tui.ConnectionUpdate, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(destination)
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-source:
				if !ok {
					return
				}
				presentation, err := adaptConnectionUpdate(update)
				if err != nil {
					continue
				}
				select {
				case <-destination:
				default:
				}
				select {
				case destination <- presentation:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return destination, done
}

func adaptConnectionUpdate(update wa.ConnectionUpdate) (tui.ConnectionUpdate, error) {
	presentation := tui.ConnectionUpdate{State: mapConnectionState(update.State())}
	qr, hasQR := update.QR()
	if !hasQR {
		return presentation, nil
	}
	size := qr.Size()
	cells := make([]bool, size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			cells[y*size+x] = qr.Cell(x, y)
		}
	}
	frame, err := tui.NewPairingQRFrame(size, cells)
	if err != nil {
		return tui.ConnectionUpdate{}, err
	}
	presentation.QR = frame
	presentation.HasQR = true
	return presentation, nil
}

func mapConnectionState(state wa.ConnectionState) tui.ConnectionState {
	switch state {
	case wa.ConnectionWaitingForQR:
		return tui.ConnectionWaitingForQR
	case wa.ConnectionConnecting:
		return tui.ConnectionConnecting
	case wa.ConnectionConnected:
		return tui.ConnectionConnected
	case wa.ConnectionFailed:
		return tui.ConnectionFailed
	default:
		return tui.ConnectionDisconnected
	}
}
