package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootVersion(t *testing.T) {
	root := newRoot("v0.1.0")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "oro v0.1.0") {
		t.Errorf("version output = %q, want substring %q", got, "oro v0.1.0")
	}
}

func TestRootNoArgsShowsShort(t *testing.T) {
	root := newRoot("dev")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Orotools") {
		t.Errorf("help output = %q, want substring %q", got, "Orotools")
	}
}
