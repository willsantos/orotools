// Package azuredevops implements Azure Repos through the az CLI.
package azuredevops

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
	if err := repository.RequireCommand(r, "az"); err != nil {
		return err
	}
	args := []string{"repos", "create", "--name", project.Name}
	if o.AzureProject != "" {
		args = append(args, "--project", o.AzureProject)
	}
	if o.Organization != "" {
		args = append(args, "--organization", o.Organization)
	}
	if err := r.Run(ctx, "az", args, o.Base, o.Stdout, o.Stderr); err != nil {
		return fmt.Errorf("az repos create: %w", err)
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
