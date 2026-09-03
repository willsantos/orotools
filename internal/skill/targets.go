package skill

import "fmt"

// SkillsDir returns the project-relative skills directory for a canonical agent ID.
func SkillsDir(agent string) (string, error) {
	switch agent {
	case "opencode":
		return ".opencode/skills", nil
	case "cursor":
		return ".cursor/skills", nil
	case "claude-code":
		return ".claude/skills", nil
	case "codex":
		return ".codex/skills", nil
	case "copilot":
		return ".github/skills", nil
	default:
		return "", fmt.Errorf("unknown agent %q", agent)
	}
}
