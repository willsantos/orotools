package executor

import (
	"io"
	"os"
	"os/exec"
)

// realExecRunner is the default ExecRunner backed by os/exec.
type realExecRunner struct{}

func (realExecRunner) Run(dir, name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
