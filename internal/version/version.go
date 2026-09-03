package version

import "fmt"

// Format returns the canonical version string reported by the CLI.
// Format("dev") -> "oro dev".
func Format(v string) string {
	return fmt.Sprintf("oro %s", v)
}
