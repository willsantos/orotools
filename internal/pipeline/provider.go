package pipeline

import (
	"context"
	"io"
	"io/fs"

	"oroborus.dev/orotools/internal/variables"
)

type Project = variables.Project

type Options struct {
	Base, RecipeName string
	RecipeFS         fs.FS
	Vars             map[string]any
	Stdout, Stderr   io.Writer
}

type Provider interface {
	Generate(context.Context, Project, Options) error
}

// DefaultProvider derives the pipeline provider from the repository provider
// when --pipeline is not explicitly set (spec section 47).
func DefaultProvider(repository string) string {
	switch repository {
	case "github":
		return "github-actions"
	case "azure-devops":
		return "azure-pipelines"
	default:
		return "none"
	}
}
