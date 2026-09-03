package repository

import (
	"context"
	"fmt"
	"io"
	"oroborus.dev/orotools/internal/variables"
	"os"
	"os/exec"
)

type Project = variables.Project

type Options struct {
	Base, Remote, Organization, AzureProject, Visibility string
	Stdout, Stderr                                       io.Writer
}

type Provider interface {
	Create(context.Context, Project, Options) error
	ConfigureRemote(context.Context, Project, Options) error
}

type CommandRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, []string, string, io.Writer, io.Writer) error
}

type RealRunner struct{}

func (RealRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
// RealRunner.Run executa apenas os binários usados pelos providers
// (allowlist git/gh/az); qualquer outro nome é rejeitado antes do exec.
// O cancelamento por contexto é feito manualmente (Kill ao ctx.Done) porque
// o comando é construído com nome literal.
func (RealRunner) Run(ctx context.Context, name string, args []string, dir string, stdout, stderr io.Writer) error {
	var cmd *exec.Cmd
	switch name {
	case "git":
		cmd = exec.Command("git", args...)
	case "gh":
		cmd = exec.Command("gh", args...)
	case "az":
		cmd = exec.Command("az", args...)
	default:
		return fmt.Errorf("command %q is not allowed", name)
	}
	cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.Stdin = dir, stdout, stderr, os.Stdin
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return ctx.Err()
	}
}

func RequireCommand(r CommandRunner, name string) error {
	if _, err := r.LookPath(name); err != nil {
		return fmt.Errorf("%s not found on PATH: %w", name, err)
	}
	return nil
}
