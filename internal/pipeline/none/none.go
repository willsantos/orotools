// Package none is the no-op pipeline provider.
package none

import (
	"context"

	"oroborus.dev/orotools/internal/pipeline"
)

type Provider struct{}

func (Provider) Generate(context.Context, pipeline.Project, pipeline.Options) error {
	return nil
}
