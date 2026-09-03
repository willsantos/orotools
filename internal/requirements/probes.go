package requirements

import (
	"fmt"
	"regexp"
	"strings"
)

// Probe describes how to obtain and parse the version of an external command.
type Probe struct {
	Args  []string
	Parse func([]byte) (string, error)
}

// probes holds version probes for the commands covered by the official recipes
// (dotnet, node, pnpm, ruby) plus common companions. Commands not present here
// cannot have their version verified; the checker reports "not supported".
var probes = map[string]Probe{
	"node":   {Args: []string{"--version"}, Parse: parseLeadingV},
	"dotnet": {Args: []string{"--version"}, Parse: parsePlain},
	"git":    {Args: []string{"--version"}, Parse: parseGitPrefix},
	"go":     {Args: []string{"version"}, Parse: parseGoPrefix},
	"pnpm":   {Args: []string{"--version"}, Parse: parsePlain},
	"npm":    {Args: []string{"--version"}, Parse: parsePlain},
	"yarn":   {Args: []string{"--version"}, Parse: parsePlain},
	"ruby":   {Args: []string{"--version"}, Parse: parseRubyPrefix},
}

var (
	gitPrefixRe   = regexp.MustCompile(`git version (\d+(?:\.\d+)*)`)
	goPrefixRe    = regexp.MustCompile(`go version go(\d+(?:\.\d+)*)`)
	rubyPrefixRe  = regexp.MustCompile(`ruby (\d+(?:\.\d+)*)`)
)

// parsePlain trims whitespace. Used by tools that print just the version
// (pnpm/npm/yarn/dotnet): "9.0.0\n" -> "9.0.0".
func parsePlain(out []byte) (string, error) {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", fmt.Errorf("empty version output")
	}
	return s, nil
}

// parseLeadingV strips a single leading "v" then trims. Used by node:
// "v24.0.0\n" -> "24.0.0".
func parseLeadingV(out []byte) (string, error) {
	s := strings.TrimSpace(string(out))
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return "", fmt.Errorf("empty version output")
	}
	return s, nil
}

func parseGitPrefix(out []byte) (string, error) {
	return parseFirstGroup(gitPrefixRe, out)
}

func parseGoPrefix(out []byte) (string, error) {
	return parseFirstGroup(goPrefixRe, out)
}

func parseRubyPrefix(out []byte) (string, error) {
	return parseFirstGroup(rubyPrefixRe, out)
}

func parseFirstGroup(re *regexp.Regexp, out []byte) (string, error) {
	m := re.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("could not parse version from %q", strings.TrimSpace(string(out)))
	}
	return string(m[1]), nil
}
