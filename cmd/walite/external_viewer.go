package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type externalViewerKind uint8

const (
	externalViewerMPV externalViewerKind = iota + 1
	externalViewerZathura
	externalViewerMPVVideo
)

var (
	errExternalViewerBusy      = errors.New("external media viewer already active")
	errExternalViewerDuplicate = errors.New("external media preview already open")
	errMPVUnavailable          = errors.New("mpv unavailable")
	errZathuraUnavailable      = errors.New("zathura unavailable")
)

type externalPreview struct {
	kind              externalViewerKind
	path              string
	chatID, messageID string
}

func (preview externalPreview) identity() string {
	return preview.chatID + "\x00" + preview.messageID
}

type externalMediaPreviewer interface {
	Show(context.Context, externalPreview) error
	Close() error
	Active() bool
	StopActive()
}

// processExternalPreviewer owns at most one external viewer and one fixed
// reaper goroutine. Preview requests never create goroutines and a child is
// always waited for, so long-lived viewers cannot block the media worker or
// become zombies.
type processExternalPreviewer struct {
	lookup func(string) (string, error)
	start  func(string, ...string) (*exec.Cmd, error)

	mu       sync.Mutex
	active   *exec.Cmd
	identity string
	closed   bool
	reap     chan *exec.Cmd
	done     chan struct{}
	reaped   chan struct{}
}

func newProcessExternalPreviewer() *processExternalPreviewer {
	previewer := &processExternalPreviewer{
		lookup: exec.LookPath,
		start: func(name string, args ...string) (*exec.Cmd, error) {
			command := exec.Command(name, args...)
			if err := command.Start(); err != nil {
				return nil, err
			}
			return command, nil
		},
		reap: make(chan *exec.Cmd, 1),
		done: make(chan struct{}),
	}
	go previewer.run()
	return previewer
}

func (previewer *processExternalPreviewer) Show(ctx context.Context, preview externalPreview) error {
	if previewer == nil || ctx == nil {
		return errors.New("external preview rejected")
	}
	binary, args, err := externalViewerCommand(preview.kind, preview.path)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved, err := previewer.lookup(binary)
	if err != nil {
		if preview.kind == externalViewerMPV || preview.kind == externalViewerMPVVideo {
			return fmt.Errorf("%w: %v", errMPVUnavailable, err)
		}
		return fmt.Errorf("%w: %v", errZathuraUnavailable, err)
	}

	previewer.mu.Lock()
	defer previewer.mu.Unlock()
	if previewer.closed {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if previewer.active != nil {
		if previewer.identity == preview.identity() {
			return errExternalViewerDuplicate
		}
		return errExternalViewerBusy
	}
	command, err := previewer.start(resolved, args...)
	if err != nil {
		return err
	}
	previewer.active = command
	previewer.reaped = make(chan struct{})
	previewer.identity = preview.identity()
	previewer.reap <- command
	return nil
}

func (previewer *processExternalPreviewer) run() {
	defer close(previewer.done)
	for command := range previewer.reap {
		_ = command.Wait()
		previewer.mu.Lock()
		if previewer.active == command {
			previewer.active = nil
			previewer.identity = ""
			close(previewer.reaped)
		}
		previewer.mu.Unlock()
	}
}

func (previewer *processExternalPreviewer) Active() bool {
	previewer.mu.Lock()
	defer previewer.mu.Unlock()
	return previewer.active != nil
}

// Called by the media worker, never the terminal event loop. The fixed reaper
// owns Wait; this path allows termination one second before escalating.
func (previewer *processExternalPreviewer) StopActive() {
	previewer.mu.Lock()
	command, reaped := previewer.active, previewer.reaped
	if command == nil {
		previewer.mu.Unlock()
		return
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	previewer.mu.Unlock()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-reaped:
		return
	case <-timer.C:
		_ = command.Process.Kill()
		<-reaped
	}
}

func (previewer *processExternalPreviewer) Close() error {
	if previewer == nil {
		return nil
	}
	previewer.mu.Lock()
	if previewer.closed {
		previewer.mu.Unlock()
		<-previewer.done
		return nil
	}
	previewer.closed = true
	previewer.mu.Unlock()
	previewer.StopActive()
	previewer.mu.Lock()
	close(previewer.reap)
	previewer.mu.Unlock()
	<-previewer.done
	return nil
}

func externalViewerCommand(kind externalViewerKind, path string) (string, []string, error) {
	if !filepath.IsAbs(path) {
		return "", nil, errors.New("external preview path rejected")
	}
	switch kind {
	case externalViewerMPVVideo:
		return "mpv", []string{"--loop-file=inf", "--", path}, nil
	case externalViewerMPV:
		// -- ensures even a hostile-looking path remains data, never an option.
		return "mpv", []string{"--", path}, nil
	case externalViewerZathura:
		// Cache paths are absolute, hash-derived names and cannot begin with '-'.
		return "zathura", []string{path}, nil
	default:
		return "", nil, errors.New("external preview kind rejected")
	}
}
