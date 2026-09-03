// Package providers is the repository provider factory.
package providers

import (
	"fmt"
	"oroborus.dev/orotools/internal/repository"
	"oroborus.dev/orotools/internal/repository/azuredevops"
	"oroborus.dev/orotools/internal/repository/github"
	"oroborus.dev/orotools/internal/repository/local"
)

func Get(name string, runner repository.CommandRunner) (repository.Provider, error) {
	switch name {
	case "local":
		return local.Provider{Runner: runner}, nil
	case "github":
		return github.Provider{Runner: runner}, nil
	case "azure-devops":
		return azuredevops.Provider{Runner: runner}, nil
	default:
		return nil, fmt.Errorf("unknown repository provider %q", name)
	}
}
