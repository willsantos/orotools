// Package github implements the GitHub repository provider through gh.
package github

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
	if err := repository.RequireCommand(r, "gh"); err != nil {
		return err
	}
	visibility := o.Visibility
	if visibility != "public" && visibility != "private" {
		visibility = "private"
	}
	args := []string{"repo", "create", project.Name, "--" + visibility, "--source", o.Base, "--remote", "origin", "--push"}
	if err := r.Run(ctx, "gh", args, o.Base, o.Stdout, o.Stderr); err != nil {
		return fmt.Errorf("gh repo create: %w", err)
	}
	return nil
}

func (p Provider) ConfigureRemote(context.Context, repository.Project, repository.Options) error {
	return nil
}
