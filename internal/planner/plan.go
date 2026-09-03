package planner

import (
	"fmt"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/installer"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/variables"
)

// Category labels the kind of operation a planned step represents.
type Category int

const (
	CatDirectory Category = iota
	CatFile
	CatCommand
	CatMessage
)

// PlannedStep is a recipe step that survived the when-filter, annotated with
// its category and (for commands) network flag.
type PlannedStep struct {
	recipe.Step
	Category Category
	Network  bool
}

// FileOp describes a copy or template file operation for rendering.
type FileOp struct {
	Kind        string // "copy" | "template"
	Source      string
	Destination string
}

// CommandOp describes an exec operation for rendering.
type CommandOp struct {
	Command          string
	Args             []string
	WorkingDirectory string
	Network          bool
}

// ExternalInstallerOp describes an external installer for dry-run rendering.
type ExternalInstallerOp struct {
	Name    string
	Label   string
	Command string
	Args    []string
}

// Plan is the resolved, filtered view of a recipe ready for execution or
// dry-run rendering.
type Plan struct {
	Project             variables.Project
	Steps               []PlannedStep
	Directories         []string
	Files               []FileOp
	Commands            []CommandOp
	Messages            []string
	BundledSkills       []string
	BundledAgents       []string
	ExternalInstallers  []ExternalInstallerOp
	LocalOps            int
	NetworkOps          int
	NetworkRequired     bool
}

// Build constructs the execution plan for r given resolved variables. Steps
// whose when evaluates false are excluded. Steps are kept in declared order.
// agentID is the canonical AI agent target used for external agent installer aliases.
func Build(r *recipe.Recipe, res *variables.Result, ctx conditions.Context, agentID string) (*Plan, error) {
	if r == nil || res == nil {
		return nil, fmt.Errorf("planner: recipe and result are required")
	}
	p := &Plan{Project: res.Project}

	for i := range r.Steps {
		s := r.Steps[i]
		if s.When != nil {
			ok, err := conditions.Evaluate(*s.When, ctx)
			if err != nil {
				return nil, fmt.Errorf("step %q when: %w", s.ID, err)
			}
			if !ok {
				continue
			}
		}
		if err := addToPlan(p, s); err != nil {
			return nil, err
		}
	}
	p.BundledSkills = append([]string(nil), r.Skills.Bundled...)
	p.BundledAgents = append([]string(nil), r.Agents.Bundled...)
	if err := appendExternalInstallers(p, r, agentID); err != nil {
		return nil, err
	}
	p.NetworkRequired = p.NetworkOps > 0
	return p, nil
}

func appendExternalInstallers(p *Plan, r *recipe.Recipe, agentID string) error {
	ops, err := installer.PlanAll(r, agentID)
	if err != nil {
		return err
	}
	for _, op := range ops {
		p.ExternalInstallers = append(p.ExternalInstallers, ExternalInstallerOp{
			Name:    op.InstallerName,
			Label:   op.Label,
			Command: op.Command,
			Args:    append([]string(nil), op.Args...),
		})
		p.NetworkOps++
	}
	return nil
}

func addToPlan(p *Plan, s recipe.Step) error {
	cat, err := categorize(s.Type)
	if err != nil {
		return fmt.Errorf("step %q: %w", s.ID, err)
	}
	ps := PlannedStep{Step: s, Category: cat}

	switch cat {
	case CatDirectory:
		p.Directories = append(p.Directories, s.Path)
		p.LocalOps++
	case CatFile:
		p.Files = append(p.Files, FileOp{Kind: s.Type, Source: s.Source, Destination: s.Destination})
		p.LocalOps++
	case CatCommand:
		net := isNetwork(s.Command)
		p.Commands = append(p.Commands, CommandOp{
			Command:          s.Command,
			Args:             append([]string(nil), s.Args...),
			WorkingDirectory: s.WorkingDirectory,
			Network:          net,
		})
		ps.Network = net
		if net {
			p.NetworkOps++
		} else {
			p.LocalOps++
		}
	case CatMessage:
		p.Messages = append(p.Messages, s.Text)
	}

	p.Steps = append(p.Steps, ps)
	return nil
}

func categorize(typ string) (Category, error) {
	switch typ {
	case "mkdir":
		return CatDirectory, nil
	case "copy", "template":
		return CatFile, nil
	case "exec":
		return CatCommand, nil
	case "message":
		return CatMessage, nil
	default:
		return 0, fmt.Errorf("unknown step type %q", typ)
	}
}
