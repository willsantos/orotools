package cli

import (
	"fmt"
	"io"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/requirements"
	"oroborus.dev/orotools/internal/ui"
)

// runDoctor validates the requirements declared by a recipe against the host
// environment. In v0.1 the recipe must be supplied via --recipe (official
// recipe discovery is delivered in fase 16 with go:embed).
func runDoctor(out io.Writer, stack, recipePath string) error {
	if recipePath == "" && stack == "" {
		return fmt.Errorf("doctor requer --stack ou --recipe")
	}
	src, err := resolveRecipe(stack, recipePath)
	if err != nil {
		return err
	}
	r, err := loadRecipeFromSource(src, recipePath)
	if err != nil {
		return fmt.Errorf("carregar recipe: %w", err)
	}
	status := ui.NewStatus(out)
	status.Title("Orotools Doctor")
	fmt.Fprintln(out)

	results := requirements.Checker{}.Check(r.Requirements, conditions.Context{})
	if len(results) == 0 {
		status.Info("nenhum requisito declarado pela recipe")
		return nil
	}
	failed := 0
	for _, res := range results {
		switch {
		case res.Skipped:
			status.Info(fmt.Sprintf("%s (pulando: when=false)", res.Requirement.Command))
		case res.Satisfied && res.FoundVersion != "":
			status.Success(fmt.Sprintf("%s %s", res.Requirement.Command, res.FoundVersion))
		case res.Satisfied:
			status.Success(res.Requirement.Command)
		default:
			status.Error(fmt.Sprintf("%s — %s", res.Requirement.Command, res.Reason))
			failed++
		}
	}
	fmt.Fprintln(out)
	if failed > 0 {
		status.Error(fmt.Sprintf("%d requisito(s) não satisfeito(s)", failed))
		return fmt.Errorf("%d requisito(s) não satisfeito(s)", failed)
	}
	status.Success("todos os requisitos satisfeitos")
	return nil
}
