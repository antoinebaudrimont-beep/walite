package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

func TestLinkedSessionSkipsPairingWindow(t *testing.T) {
	screen := newStartupObservedScreen()
	launches := 0
	runs := 0
	err := runWithPairingWindow(context.Background(), screen, pairingStartupDependencies{
		sessionLinked: func(context.Context) (bool, error) { return true, nil },
		launchPairing: func(context.Context) error {
			launches++
			return nil
		},
		runLinked: func(context.Context, tcell.Screen) error {
			runs++
			return nil
		},
	})
	if err != nil || launches != 0 || runs != 1 {
		t.Fatalf("err=%v launches=%d runs=%d", err, launches, runs)
	}
}

func TestUnlinkedSessionLaunchesOnceThenReloadsLinkedSession(t *testing.T) {
	screen := newStartupObservedScreen()
	linkedResults := []bool{false, true}
	probes := 0
	launches := 0
	runs := 0
	sequence := make([]string, 0, 7)
	err := runWithPairingWindow(context.Background(), screen, pairingStartupDependencies{
		sessionLinked: func(context.Context) (bool, error) {
			sequence = append(sequence, "probe-start")
			result := linkedResults[probes]
			probes++
			sequence = append(sequence, "probe-closed")
			return result, nil
		},
		launchPairing: func(context.Context) error {
			if screen.initialized {
				t.Fatal("normal terminal initialized while pairing helper owned setup")
			}
			sequence = append(sequence, "helper-start", "helper-closed")
			launches++
			return nil
		},
		runLinked: func(context.Context, tcell.Screen) error {
			sequence = append(sequence, "normal-start")
			runs++
			if err := screen.Init(); err != nil {
				t.Fatal(err)
			}
			screen.Fini()
			return nil
		},
	})
	wantSequence := []string{
		"probe-start", "probe-closed",
		"helper-start", "helper-closed",
		"probe-start", "probe-closed",
		"normal-start",
	}
	if err != nil || probes != 2 || launches != 1 || runs != 1 || !reflect.DeepEqual(sequence, wantSequence) ||
		screen.initCount.Load() != 1 || screen.finiCount.Load() != 1 {
		t.Fatalf("err=%v probes=%d launches=%d runs=%d sequence=%v init=%d fini=%d",
			err, probes, launches, runs, sequence, screen.initCount.Load(), screen.finiCount.Load())
	}
}

func TestPairingWindowFailureAndUnlinkedResultPreventNormalStartup(t *testing.T) {
	t.Run("launcher failure", func(t *testing.T) {
		failure := errors.New("synthetic launcher failure")
		runs := 0
		err := runWithPairingWindow(context.Background(), newStartupObservedScreen(), pairingStartupDependencies{
			sessionLinked: func(context.Context) (bool, error) { return false, nil },
			launchPairing: func(context.Context) error { return failure },
			runLinked: func(context.Context, tcell.Screen) error {
				runs++
				return nil
			},
		})
		var pairingErr *pairingWindowError
		if !errors.As(err, &pairingErr) || !errors.Is(err, failure) || runs != 0 || pairingErr.UserMessage() != pairingFallbackMessage {
			t.Fatalf("err=%v runs=%d", err, runs)
		}
	})

	t.Run("helper exits without linked session", func(t *testing.T) {
		probes := 0
		runs := 0
		err := runWithPairingWindow(context.Background(), newStartupObservedScreen(), pairingStartupDependencies{
			sessionLinked: func(context.Context) (bool, error) {
				probes++
				return false, nil
			},
			launchPairing: func(context.Context) error { return nil },
			runLinked: func(context.Context, tcell.Screen) error {
				runs++
				return nil
			},
		})
		if !errors.Is(err, errPairingSessionUnlinked) || probes != 2 || runs != 0 {
			t.Fatalf("err=%v probes=%d runs=%d", err, probes, runs)
		}
	})
}

func TestPairingWindowCancellationIsPropagated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := runWithPairingWindow(ctx, newStartupObservedScreen(), pairingStartupDependencies{
		sessionLinked: func(context.Context) (bool, error) { return false, nil },
		launchPairing: func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		},
		runLinked: func(context.Context, tcell.Screen) error {
			t.Fatal("normal startup reached after cancellation")
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestXFCEPairingLauncherArgumentsAreDeterministic(t *testing.T) {
	const executable = "/opt/walite test/walite"
	var gotName string
	var gotArguments []string
	err := launchXFCEPairingWindow(context.Background(), executable, func(_ context.Context, name string, arguments ...string) error {
		gotName = name
		gotArguments = append([]string(nil), arguments...)
		return nil
	})
	wantArguments := []string{
		"--disable-server",
		"--geometry=80x46",
		"--font=Monospace 8",
		"--title=walite — WhatsApp setup",
		"--hide-menubar",
		"--hide-toolbar",
		"--hide-scrollbar",
		"--execute",
		executable,
		"--pair",
	}
	if err != nil || gotName != "xfce4-terminal" || !reflect.DeepEqual(gotArguments, wantArguments) {
		t.Fatalf("err=%v name=%q arguments=%q", err, gotName, gotArguments)
	}
}

func TestDarwinPairingUsesDedicatedTerminalAppWindowWithoutITerm(t *testing.T) {
	const executable = "/Applications/Walite Test/walite"
	var gotName string
	var gotArguments []string
	err := launchDarwinPairingWindow(context.Background(), "/usr/bin/osascript", executable,
		func(_ context.Context, name string, arguments ...string) error {
			gotName = name
			gotArguments = append([]string(nil), arguments...)
			return nil
		})
	if err != nil || gotName != "/usr/bin/osascript" || len(gotArguments) != 4 ||
		gotArguments[0] != "-e" || gotArguments[1] != darwinPairingScript || gotArguments[2] != "--" || gotArguments[3] != executable {
		t.Fatalf("err=%v name=%q arguments=%q", err, gotName, gotArguments)
	}
	for _, required := range []string{`application "Terminal"`, "quoted form of walitePath", "number of columns of setupWindow to 80", "number of rows of setupWindow to 46", "busy of setupTab", "close setupWindow"} {
		if !strings.Contains(darwinPairingScript, required) {
			t.Fatalf("pairing script missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(darwinPairingScript), "iterm") {
		t.Fatal("darwin pairing requires iTerm")
	}
}

func TestPairingHelperOwnsOnlyUnlinkedConnection(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		connection := newFakeApplicationConnection(false)
		viewCalled := 0
		err := runPairingHelper(context.Background(), tcell.NewSimulationScreen("UTF-8"), connection,
			func(_ context.Context, _ tcell.Screen, input tui.ConnectionInput) error {
				<-connection.started
				if input.Linked {
					t.Fatal("pairing helper marked unlinked session linked")
				}
				viewCalled++
				return nil
			})
		if err != nil || viewCalled != 1 || connection.disconnectCount.Load() != 1 || connection.closeCount.Load() != 1 {
			t.Fatalf("err=%v view=%d disconnect=%d close=%d", err, viewCalled, connection.disconnectCount.Load(), connection.closeCount.Load())
		}
	})

	t.Run("already linked", func(t *testing.T) {
		connection := newFakeApplicationConnection(true)
		viewCalled := false
		err := runPairingHelper(context.Background(), tcell.NewSimulationScreen("UTF-8"), connection,
			func(context.Context, tcell.Screen, tui.ConnectionInput) error {
				viewCalled = true
				return nil
			})
		if !errors.Is(err, errPairingNotRequired) || viewCalled || connection.closeCount.Load() != 1 {
			t.Fatalf("err=%v view=%t close=%d", err, viewCalled, connection.closeCount.Load())
		}
	})
}
