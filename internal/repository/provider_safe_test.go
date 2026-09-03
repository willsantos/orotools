package repository

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRealRunnerAllowlist(t *testing.T) {
	if err := (RealRunner{}).Run(context.Background(), "git;push", nil, "", io.Discard, io.Discard); err == nil {
		t.Fatal("expected rejection of non-allowlisted command")
	}
	var out strings.Builder
	if err := (RealRunner{}).Run(context.Background(), "git", []string{"--version"}, t.TempDir(), &out, &out); err != nil {
		t.Fatalf("git --version failed: %v", err)
	}
	if !strings.Contains(out.String(), "git version") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}
