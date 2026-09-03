// Package providers is the pipeline provider factory.
package providers

import (
	"fmt"

	"oroborus.dev/orotools/internal/pipeline"
	"oroborus.dev/orotools/internal/pipeline/azurepipelines"
	"oroborus.dev/orotools/internal/pipeline/githubactions"
	"oroborus.dev/orotools/internal/pipeline/none"
)

func Get(name string) (pipeline.Provider, error) {
	switch name {
	case "none", "":
		return none.Provider{}, nil
	case "github-actions":
		return githubactions.Provider{}, nil
	case "azure-pipelines":
		return azurepipelines.Provider{}, nil
	default:
		return nil, fmt.Errorf("unknown pipeline provider %q", name)
	}
}
