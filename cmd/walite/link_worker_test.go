package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

func TestLinkWorkerUsesExactDirectArgumentsAndStdin(t *testing.T) {
	type call struct {
		name, stdin string
		args        []string
	}
	calls := make(chan call, 2)
	runner := func(_ context.Context, name string, args []string, stdin io.Reader) error {
		body := ""
		if stdin != nil {
			data, _ := io.ReadAll(stdin)
			body = string(data)
		}
		calls <- call{name: name, args: append([]string(nil), args...), stdin: body}
		return nil
	}
	worker := newLinkWorker(context.Background(), "/usr/bin/xdg-open", "/usr/bin/xclip", []string{"-selection", "clipboard", "-in"}, runner)
	want := "https://example.test/$(touch-pwned)?x=;&y=日本語"
	for _, action := range []tui.LinkAction{tui.LinkOpen, tui.LinkCopy} {
		if !worker.admit(tui.LinkRequest{Action: action, URL: want}) {
			worker.stop()
			t.Fatal("request rejected")
		}
		<-worker.results
	}
	worker.stop()
	opened, copied := <-calls, <-calls
	if opened.name != "/usr/bin/xdg-open" || len(opened.args) != 1 || opened.args[0] != want || opened.stdin != "" {
		t.Fatalf("open=%+v", opened)
	}
	if copied.name != "/usr/bin/xclip" || strings.Join(copied.args, "\x00") != "-selection\x00clipboard\x00-in" || copied.stdin != want {
		t.Fatalf("copy=%+v", copied)
	}
}

func TestLinkWorkerReportsMissingHelpers(t *testing.T) {
	worker := newLinkWorker(context.Background(), "", "", nil, func(context.Context, string, []string, io.Reader) error {
		t.Fatal("missing helper invoked")
		return nil
	})
	for _, action := range []tui.LinkAction{tui.LinkOpen, tui.LinkCopy} {
		if !worker.admit(tui.LinkRequest{Action: action, URL: "https://example.test"}) {
			worker.stop()
			t.Fatal("request rejected")
		}
		if result := <-worker.results; !strings.Contains(result.Status, "requires") {
			t.Fatalf("result=%+v", result)
		}
	}
	worker.stop()
}
