package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProcessExternalPreviewerBoundsAndReapsOneChild(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	previewer := newProcessExternalPreviewer()
	t.Cleanup(func() { _ = previewer.Close() })
	previewer.lookup = func(string) (string, error) { return os.Args[0], nil }
	var started *exec.Cmd
	previewer.start = func(string, ...string) (*exec.Cmd, error) {
		command := exec.Command(os.Args[0], "-test.run=^TestExternalViewerHelperProcess$")
		command.Env = append(os.Environ(), "WALITE_EXTERNAL_VIEWER_HELPER=1")
		command.Stdin = reader
		if err := command.Start(); err != nil {
			return nil, err
		}
		started = command
		return command, nil
	}

	first := externalPreview{kind: externalViewerMPV, path: filepath.Join(t.TempDir(), "video.mp4"), chatID: "chat", messageID: "one"}
	if err := previewer.Show(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := previewer.Show(context.Background(), first); !errors.Is(err, errExternalViewerDuplicate) {
		t.Fatalf("duplicate err=%v", err)
	}
	second := externalPreview{kind: externalViewerMPV, path: filepath.Join(t.TempDir(), "other.mp4"), chatID: "chat", messageID: "two"}
	if err := previewer.Show(context.Background(), second); !errors.Is(err, errExternalViewerBusy) {
		t.Fatalf("second err=%v", err)
	}
	previewer.StopActive()
	if started == nil || started.ProcessState == nil {
		t.Fatal("external viewer child was not waited for")
	}
	if previewer.Active() || previewer.identity != "" {
		t.Fatal("viewer state remained active after stop")
	}
	for _, kind := range []externalViewerKind{externalViewerMPVVideo, externalViewerMPV, externalViewerZathura} {
		first.kind = kind
		if err := previewer.Show(context.Background(), first); err != nil {
			t.Fatalf("same media reopen: %v", err)
		}
		if !previewer.Active() {
			t.Fatal("reopened viewer not active")
		}
		previewer.StopActive()
		if previewer.Active() || started.ProcessState == nil {
			t.Fatal("reopened child not reaped and cleared")
		}
	}
}

func TestExternalVideoLoopArgumentsLeaveAudioUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preview.mp4")
	for _, label := range []string{"video", "GifPlayback MP4"} {
		t.Run(label, func(t *testing.T) {
			binary, args, err := externalViewerCommand(externalViewerMPVVideo, path)
			if err != nil || binary != "mpv" || !reflect.DeepEqual(args, []string{"--loop-file=inf", "--", path}) {
				t.Fatalf("binary=%q args=%q err=%v", binary, args, err)
			}
		})
	}
	_, args, err := externalViewerCommand(externalViewerMPV, path)
	if err != nil || !reflect.DeepEqual(args, []string{"--", path}) {
		t.Fatalf("audio args=%q err=%v", args, err)
	}
}

func TestProcessExternalPreviewerReportsMissingBackend(t *testing.T) {
	previewer := newProcessExternalPreviewer()
	defer previewer.Close()
	previewer.lookup = func(string) (string, error) { return "", exec.ErrNotFound }
	path := filepath.Join(t.TempDir(), "media")
	if err := previewer.Show(context.Background(), externalPreview{kind: externalViewerMPV, path: path}); !errors.Is(err, errMPVUnavailable) {
		t.Fatalf("mpv err=%v", err)
	}
	if err := previewer.Show(context.Background(), externalPreview{kind: externalViewerZathura, path: path}); !errors.Is(err, errZathuraUnavailable) {
		t.Fatalf("zathura err=%v", err)
	}
}

func TestExternalViewerHelperProcess(t *testing.T) {
	if os.Getenv("WALITE_EXTERNAL_VIEWER_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
