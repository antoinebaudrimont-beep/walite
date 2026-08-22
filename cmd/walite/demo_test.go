package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
)

func TestDemoConstructionAndWiring(t *testing.T) {
	values := config.DefaultValues()
	if err := values.Validate(); err != nil {
		t.Fatal(err)
	}
	scenario, err := newDemoScenario(values)
	if err != nil {
		t.Fatal(err)
	}
	wantLimit := values.Retention.MessagesPerChat + values.Batch.MaxOperations + 1
	if scenario.retentionSnapshotLimit != wantLimit {
		t.Fatalf("retention snapshot limit=%d", scenario.retentionSnapshotLimit)
	}
	var _ service.MessageStore = scenario.gated
	var _ service.EventSource = scenario.source
	var _ service.RetentionPolicy = scenario.policy

	invalid := values
	invalid.Retention.MaxChats = 0
	if malformed, err := newDemoScenario(invalid); err == nil || malformed.core != nil {
		t.Fatalf("malformed=%+v err=%v", malformed, err)
	}
}

func TestDemoNaturalOutputOrderingAndRetention(t *testing.T) {
	scenario, err := newDemoScenario(config.DefaultValues())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runDemo(context.Background(), &output, scenario); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, required := range []string{"walite", "ready", "live", "degraded", "history", "summary", "stopped", "demo-chat", "demo-live-1", "Synthetic"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %q in %q", required, text)
		}
	}
	for _, forbidden := range []string{"@s.whatsapp.net", "whatsapp.com"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("forbidden %q in output", forbidden)
		}
	}
	ready := strings.Index(text, "ready\n")
	live := strings.Index(text, "live ")
	history := strings.Index(text, "history ")
	summary := strings.Index(text, "summary ")
	stopped := strings.Index(text, "stopped\n")
	if !(ready >= 0 && ready < live && live < history && history < summary && summary < stopped) {
		t.Fatalf("output ordering:\n%s", text)
	}
	degraded := strings.Index(text, "degraded ")
	if degraded < 0 || degraded > summary || strings.Count(text, "degraded ") != 1 {
		t.Fatalf("degraded output:\n%s", text)
	}
	status := scenario.source.Status()
	if !status.Degraded() || status.BulkDropped() != 1 || status.MetadataDropped() != 0 || status.OnDemandDropped() != 0 || status.UnknownDropped() != 0 {
		t.Fatalf("status metadata=%d on-demand=%d bulk=%d unknown=%d degraded=%t", status.MetadataDropped(), status.OnDemandDropped(), status.BulkDropped(), status.UnknownDropped(), status.Degraded())
	}
	if output.Len() > 16*1024 {
		t.Fatalf("output bytes=%d", output.Len())
	}
	usage, err := scenario.store.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	values := config.DefaultValues()
	if usage.Bodies() > values.Retention.MessagesPerChat || usage.Messages() >= demoHistoryCount {
		t.Fatalf("usage bodies=%d messages=%d", usage.Bodies(), usage.Messages())
	}
}

type readyBlockingWriter struct {
	buffer  bytes.Buffer
	ready   chan struct{}
	release chan struct{}
}

func (writer *readyBlockingWriter) Write(data []byte) (int, error) {
	if bytes.Equal(data, []byte("ready\n")) {
		close(writer.ready)
		<-writer.release
	}
	return writer.buffer.Write(data)
}

func TestDemoCancellationBeforeHistoryStarts(t *testing.T) {
	scenario, err := newDemoScenario(config.DefaultValues())
	if err != nil {
		t.Fatal(err)
	}
	writer := &readyBlockingWriter{ready: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, writer, scenario) }()
	<-writer.ready
	cancel()
	close(writer.release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("runDemo=%v", err)
	}
	if writer.buffer.Len() > 16*1024 {
		t.Fatalf("output bytes=%d", writer.buffer.Len())
	}
}

type failingWriter struct{ failure error }

func (writer failingWriter) Write([]byte) (int, error) { return 0, writer.failure }

func TestDemoWriterFailureCancelsAndJoins(t *testing.T) {
	scenario, err := newDemoScenario(config.DefaultValues())
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic writer failure")
	if err := runDemo(context.Background(), failingWriter{failure}, scenario); !errors.Is(err, failure) {
		t.Fatalf("runDemo=%v", err)
	}
}

var _ io.Writer = (*readyBlockingWriter)(nil)
