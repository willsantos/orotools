// Package safeexec guards the boundaries where OroTools spawns external
// processes. Commands are always built with a separated program name and args
// (never a shell string); ValidateName rejects names that could smuggle path
// separators, whitespace or shell metacharacters into the program position.
package safeexec

import (
	"fmt"
	"strings"
)

// banned collects characters that never occur in a legitimate program name
// but do occur in path separators, whitespace and shell syntax.
const banned = "\x00/\\\t\r\n `;&|<>$'\"(){}[]*?~!"

// ValidateName reports whether name is safe to pass to exec as the program.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("command name is empty")
	}
	if i := strings.IndexAny(name, banned); i >= 0 {
		return fmt.Errorf("command name %q contains forbidden character %q", name, name[i:i+1])
	}
	return nil
}
