package agent

import "fmt"

// AgentsDir returns the project-relative agents directory for a canonical agent ID.
func AgentsDir(agent string) (string, error) {
	switch agent {
	case "opencode":
		return ".opencode/agents", nil
	case "cursor":
		return ".cursor/agents", nil
	case "claude-code":
		return ".claude/agents", nil
	case "codex":
		return ".codex/agents", nil
	case "copilot":
		return ".github/agents", nil
	default:
		return "", fmt.Errorf("unknown agent %q", agent)
	}
}
