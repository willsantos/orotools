// Package githubactions generates GitHub Actions workflow files.
package githubactions

import (
	"context"

	"oroborus.dev/orotools/internal/pipeline"
)

const (
	templateRel = "pipelines/github-actions.yml.tmpl"
	destRel     = ".github/workflows/ci.yml"
	providerKey = "github-actions"
)

type Provider struct{}

func (Provider) Generate(_ context.Context, project pipeline.Project, o pipeline.Options) error {
	_, err := pipeline.Render(templateRel, destRel, providerKey, project, o)
	return err
}
