package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

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
