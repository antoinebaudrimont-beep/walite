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

var errPairingCancelled = errors.New("WhatsApp pairing cancelled")

func runPairMode(ctx context.Context) error {
	sessionPath, err := config.DefaultWhatsAppSessionPath()
	if err != nil {
		return fmt.Errorf("WhatsApp session path: %w", err)
	}
	connection, err := wa.NewConnection(ctx, sessionPath)
	if err != nil {
		return fmt.Errorf("construct WhatsApp pairing connection: %w", err)
	}
	screen, err := tcell.NewScreen()
	if err != nil {
		_ = connection.Close()
		return err
	}
	return runPairingHelper(ctx, screen, connection, tui.RunConnection)
}

func runPairingHelper(
	parent context.Context,
	screen tcell.Screen,
	connection applicationConnection,
	runView func(context.Context, tcell.Screen, tui.ConnectionInput) error,
) error {
	if parent == nil || screen == nil || connection == nil || runView == nil {
		return errors.New("pairing helper rejected")
	}
	defer connection.Close()
	if connection.Linked() {
		return errPairingNotRequired
	}

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
	viewErr := runView(runCtx, screen, tui.ConnectionInput{
		Linked:  false,
		Updates: presentationUpdates,
	})
	cancel()
	connectionErr := <-connectionDone
	<-presentationDone

	if connectionErr != nil && !errors.Is(connectionErr, context.Canceled) {
		return fmt.Errorf("pair WhatsApp: %w", connectionErr)
	}
	if viewErr == nil {
		return nil
	}
	if errors.Is(viewErr, tui.ErrConnectionViewExit) {
		return errPairingCancelled
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return fmt.Errorf("show WhatsApp pairing: %w", viewErr)
}
