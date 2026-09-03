package cli

import (
	"fmt"
	"io"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/ui"
)

// addOptions carries parsed args/flags for `oro add`.
type addOptions struct {
	kind         string // skill | agent | pipeline
	value        string
	manifestPath string
	out          io.Writer
}

// runAdd mutates orotools.yaml to register a skill, agent or pipeline (spec
// section 59). In v0.1 this edits the manifest lists only — actual installation
// of skills/agents and pipeline file generation arrive in fases 13-15.
func runAdd(opts addOptions) error {
	out := writerOrStdout(opts.out)
	status := ui.NewStatus(out)

	m, err := manifest.Read(opts.manifestPath)
	if err != nil {
		return fmt.Errorf("ler manifest: %w", err)
	}

	switch opts.kind {
	case "skill":
		if contains(m.Skills.Bundled, opts.value) {
			status.Info(fmt.Sprintf("skill %q já presente", opts.value))
			return nil
		}
		m.Skills.Bundled = append(m.Skills.Bundled, opts.value)
		status.Success(fmt.Sprintf("skill %q adicionada", opts.value))
	case "agent":
		if contains(m.Agents.Bundled, opts.value) {
			status.Info(fmt.Sprintf("agent %q já presente", opts.value))
			return nil
		}
		m.Agents.Bundled = append(m.Agents.Bundled, opts.value)
		status.Success(fmt.Sprintf("agent %q adicionado", opts.value))
	case "pipeline":
		if m.Pipeline.Provider == opts.value {
			status.Info(fmt.Sprintf("provider de pipeline já é %q", opts.value))
			return nil
		}
		m.Pipeline.Provider = opts.value
		status.Success(fmt.Sprintf("provider de pipeline definido: %q", opts.value))
	default:
		return fmt.Errorf("add: tipo desconhecido %q (esperado skill|agent|pipeline)", opts.kind)
	}

	if err := manifest.Write(opts.manifestPath, m); err != nil {
		return fmt.Errorf("escrever manifest: %w", err)
	}
	return nil
}
