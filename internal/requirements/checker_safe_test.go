package requirements

import "testing"

func TestRealRunnerOutputAllowlist(t *testing.T) {
	bad := []string{"node;rm", "git|cat", "/bin/sh", "foo bar", "$(x)", "evil"}
	for _, name := range bad {
		if _, err := (realRunner{}).Output(name, nil); err == nil {
			t.Errorf("Output(%q) = nil error, want rejection", name)
		}
	}
}
