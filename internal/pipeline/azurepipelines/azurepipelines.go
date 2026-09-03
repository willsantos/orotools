// Package azurepipelines generates Azure Pipelines YAML files.
package azurepipelines

import (
	"context"

	"oroborus.dev/orotools/internal/pipeline"
)

const (
	templateRel = "pipelines/azure-pipelines.yml.tmpl"
	destRel     = "azure-pipelines.yml"
	providerKey = "azure-pipelines"
)

type Provider struct{}

func (Provider) Generate(_ context.Context, project pipeline.Project, o pipeline.Options) error {
	_, err := pipeline.Render(templateRel, destRel, providerKey, project, o)
	return err
}
