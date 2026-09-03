package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestStatusNonTTYDegradesToASCII(t *testing.T) {
	var buf bytes.Buffer
	s := NewStatus(&buf)
	if s.tty {
		t.Fatalf("buffer should not be detected as TTY")
	}
	s.Title("Orotools")
	s.Success("Environment")
	s.Error("pnpm")
	s.Warn("failed")
	s.Info("note")

	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("non-TTY output must not contain ANSI escapes, got: %q", out)
	}
	for _, want := range []string{"Orotools", "+ Environment", "x pnpm", "! failed", "- note"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %q", want, out)
		}
	}
}

func TestStatusNonTTYDefaultMarkers(t *testing.T) {
	var buf bytes.Buffer
	s := NewStatus(&buf)
	if s.markerGood != '+' || s.markerBad != 'x' || s.markerWarn != '!' || s.markerInfo != '-' {
		t.Fatalf("unexpected non-TTY markers: %#q", []rune{s.markerGood, s.markerBad, s.markerWarn, s.markerInfo})
	}
}
