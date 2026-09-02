package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestMainReportsWrappedStartupError(t *testing.T) {
	var output bytes.Buffer
	if code := reportMainError(&output, errors.New("construct service: read receipt capability unavailable")); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if got, want := output.String(), "walite failed: construct service: read receipt capability unavailable\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
}
