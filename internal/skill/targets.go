package skill

import "fmt"

// AllAgents lista os agents suportados na ordem canônica: detecção de
// diretórios, seleção no wizard e limpeza de orphans percorrem esta ordem para
// manter entrada e relatórios determinísticos.
var AllAgents = []string{"opencode", "cursor", "claude-code", "codex", "copilot"}

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

// AgentFromSkillsDir faz o reverso de SkillsDir: dado o diretório de skills de
// um target do lock (path.Dir do target), devolve o id canônico do agent.
func AgentFromSkillsDir(dir string) (string, error) {
	for _, agent := range AllAgents {
		if d, _ := SkillsDir(agent); d == dir {
			return agent, nil
		}
	}
	return "", fmt.Errorf("diretório %q não corresponde a um agent suportado", dir)
}
