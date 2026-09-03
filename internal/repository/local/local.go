// Package local implements the local Git repository provider.
package local

import (
	"context"
	"fmt"
	"oroborus.dev/orotools/internal/repository"
)

type Provider struct{ Runner repository.CommandRunner }

func (p Provider) runner() repository.CommandRunner {
	if p.Runner != nil {
		return p.Runner
	}
	return repository.RealRunner{}
}

func (p Provider) Create(ctx context.Context, project repository.Project, o repository.Options) error {
	r := p.runner()
	for _, command := range []string{"git"} {
		if err := repository.RequireCommand(r, command); err != nil {
			return err
		}
	}
	steps := [][]string{{"init"}, {"branch", "-M", "main"}, {"add", "."}, {"commit", "-m", "chore: initial project setup"}}
	for _, args := range steps {
		if err := r.Run(ctx, "git", args, o.Base, o.Stdout, o.Stderr); err != nil {
			return fmt.Errorf("git %v: %w", args, err)
		}
	}
	return nil
}

func (p Provider) ConfigureRemote(ctx context.Context, project repository.Project, o repository.Options) error {
	if o.Remote == "" {
		return nil
	}
	if err := p.runner().Run(ctx, "git", []string{"remote", "add", "origin", o.Remote}, o.Base, o.Stdout, o.Stderr); err != nil {
		return fmt.Errorf("git remote add origin: %w", err)
	}
	return nil
}
