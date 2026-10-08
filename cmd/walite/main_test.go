package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const terminationSignalHelperEnvironment = "WALITE_TERMINATION_SIGNAL_HELPER"

func TestTerminationSignalsIncludeTerminalLossAndNormalTermination(t *testing.T) {
	got := terminationSignals()
	want := []os.Signal{os.Interrupt, syscall.SIGHUP, syscall.SIGTERM}
	if len(got) != len(want) {
		t.Fatalf("signals=%v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("signals=%v want=%v", got, want)
		}
	}
}

func TestTerminationSignalsCancelSharedContextCleanly(t *testing.T) {
	for _, test := range []struct {
		name   string
		signal os.Signal
	}{
		{name: "SIGINT", signal: os.Interrupt},
		{name: "SIGTERM", signal: syscall.SIGTERM},
		{name: "SIGHUP", signal: syscall.SIGHUP},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestTerminationSignalHelperProcess$")
			command.Env = append(os.Environ(), terminationSignalHelperEnvironment+"=1")
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			ready := make(chan string, 1)
			go func() {
				line, _ := bufio.NewReader(stdout).ReadString('\n')
				ready <- line
			}()
			select {
			case line := <-ready:
				if line != "ready\n" {
					_ = command.Process.Kill()
					_ = command.Wait()
					t.Fatalf("helper readiness=%q stderr=%q", line, stderr.String())
				}
			case <-time.After(2 * time.Second):
				_ = command.Process.Kill()
				_ = command.Wait()
				t.Fatalf("helper did not become ready: %s", stderr.String())
			}
			if err := command.Process.Signal(test.signal); err != nil {
				_ = command.Process.Kill()
				_ = command.Wait()
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("helper exit=%v stderr=%q", err, stderr.String())
				}
			case <-time.After(2 * time.Second):
				_ = command.Process.Kill()
				<-done
				t.Fatal("helper did not exit after termination signal")
			}
		})
	}
}

func TestTerminationSignalHelperProcess(t *testing.T) {
	if os.Getenv(terminationSignalHelperEnvironment) != "1" {
		return
	}
	ctx, stop := terminationContext(context.Background())
	defer stop()
	_, _ = os.Stdout.WriteString("ready\n")
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error=%v", ctx.Err())
	}
}

func TestMainArgumentHelp(t *testing.T) {
	for _, argument := range []string{"--help", "-h"} {
		t.Run(argument, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			handled, code := handleMainArguments([]string{argument}, &stdout, &stderr)
			if !handled || code != 0 || stdout.String() != commandHelp || stderr.Len() != 0 {
				t.Fatalf("handled=%t code=%d stdout=%q stderr=%q", handled, code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestMainArgumentVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	handled, code := handleMainArguments([]string{"--version"}, &stdout, &stderr)
	if !handled || code != 0 || stdout.String() != "walite "+version+"\n" || stderr.Len() != 0 {
		t.Fatalf("handled=%t code=%d stdout=%q stderr=%q", handled, code, stdout.String(), stderr.String())
	}
}

func TestMainArgumentPairAndNormalStartupPassThrough(t *testing.T) {
	for _, args := range [][]string{nil, {pairingHelperFlag}} {
		var stdout, stderr bytes.Buffer
		handled, code := handleMainArguments(args, &stdout, &stderr)
		if handled || code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("args=%v handled=%t code=%d stdout=%q stderr=%q", args, handled, code, stdout.String(), stderr.String())
		}
	}
}

func TestMainRejectsUnsupportedArguments(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {pairingHelperFlag, "extra"}} {
		var stdout, stderr bytes.Buffer
		handled, code := handleMainArguments(args, &stdout, &stderr)
		if !handled || code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unsupported arguments") || !strings.Contains(stderr.String(), commandHelp) {
			t.Fatalf("args=%v handled=%t code=%d stdout=%q stderr=%q", args, handled, code, stdout.String(), stderr.String())
		}
	}
}

func TestMainReportsWrappedStartupError(t *testing.T) {
	var output bytes.Buffer
	if code := reportMainError(&output, errors.New("construct service: read receipt capability unavailable")); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if got, want := output.String(), "walite failed: construct service: read receipt capability unavailable\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
}

func TestMainTreatsIntentionalCancellationAsCleanExit(t *testing.T) {
	for _, err := range []error{context.Canceled, fmt.Errorf("run application: %w", context.Canceled), errors.Join(context.Canceled, fmt.Errorf("stop: %w", context.Canceled))} {
		var output bytes.Buffer
		if code := mainResult(&output, err); code != 0 || output.Len() != 0 {
			t.Fatalf("error=%v code=%d output=%q", err, code, output.String())
		}
	}
}

func TestMainReportsRealFailureThatAccompaniesCancellation(t *testing.T) {
	var output bytes.Buffer
	err := errors.Join(context.Canceled, errors.New("close cache: synthetic failure"))
	if code := mainResult(&output, err); code != 1 || !strings.Contains(output.String(), "close cache: synthetic failure") {
		t.Fatalf("code=%d output=%q", code, output.String())
	}
}
