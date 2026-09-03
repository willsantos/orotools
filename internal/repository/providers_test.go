package repository_test

import (
	"context"
	"io"
	"oroborus.dev/orotools/internal/repository"
	"oroborus.dev/orotools/internal/repository/providers"
	"testing"
)

type fake struct{ calls []string }

func (f *fake) LookPath(n string) (string, error) { return n, nil }
func (f *fake) Run(_ context.Context, n string, args []string, _ string, _, _ io.Writer) error {
	f.calls = append(f.calls, n+" "+join(args))
	return nil
}
func join(xs []string) string {
	out := ""
	for _, x := range xs {
		if out != "" {
			out += " "
		}
		out += x
	}
	return out
}

func TestProvidersBuildCommands(t *testing.T) {
	for _, tc := range []struct{ name, command, expected string }{
		{"local", "git", "git init"}, {"github", "gh", "gh repo create"}, {"azure-devops", "az", "az repos create"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{}
			p, err := providers.Get(tc.name, f)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Create(context.Background(), repository.Project{Name: "demo"}, repository.Options{Base: "."}); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range f.calls {
				if len(c) >= len(tc.expected) && c[:len(tc.expected)] == tc.expected {
					found = true
				}
			}
			if !found {
				t.Errorf("calls=%v", f.calls)
			}
		})
	}
}
