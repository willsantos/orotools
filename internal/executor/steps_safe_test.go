package executor

import (
	"testing"

	"oroborus.dev/orotools/internal/recipe"
)

func TestExec_RejectsUnsafeCommandName(t *testing.T) {
	fake := &fakeExecRunner{}
	r := &Result{}
	ctx, _ := baseCtx(t, nil)
	execExec(r, recipe.Step{Type: "exec", Command: "foo;bar", Args: []string{"x"}}, ctx, fake)
	if r.Error == nil {
		t.Fatal("expected error for unsafe command name")
	}
	if r.Severity != SevFatal {
		t.Fatalf("severity = %v, want SevFatal", r.Severity)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("runner was called %d times, want 0", len(fake.calls))
	}
}
