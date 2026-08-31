package main

import (
	"context"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type displaySource interface {
	DisplayUpdates() <-chan model.DisplayMetadata
}
type displayApplication interface {
	displaySource
	ApplyDisplayMetadata(context.Context, model.DisplayMetadata) (model.DisplayMetadata, error)
}

func forwardDisplayMetadata(ctx context.Context, application displayApplication, destination chan<- tui.DisplayMetadata) {
	defer close(destination)
	source := application.DisplayUpdates()
	if source == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case metadata, ok := <-source:
			if !ok {
				return
			}
			stored, err := application.ApplyDisplayMetadata(ctx, metadata)
			if err != nil {
				continue
			} // advisory failure cannot fail committed traffic
			value := tui.DisplayMetadata{ID: stored.ID().String(), Name: stored.Name(), Quality: uint8(stored.Quality()), IsGroup: stored.IsGroup()}
			select {
			case destination <- value:
			case <-ctx.Done():
				return
			}
		}
	}
}
