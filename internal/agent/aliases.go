package agent

// InstallerAlias translates a canonical agent ID to an installer-specific alias.
func InstallerAlias(canonicalAgent, installer string) string {
	if aliases, ok := defaultAliases[canonicalAgent]; ok {
		if alias, ok := aliases[installer]; ok {
			return alias
		}
	}
	return canonicalAgent
}

var defaultAliases = map[string]map[string]string{
	"opencode":    {"tech-leads-club": "opencode"},
	"claude-code": {"tech-leads-club": "claude-code"},
	"cursor":      {"tech-leads-club": "cursor"},
	"codex":       {"tech-leads-club": "codex"},
	"copilot":     {"tech-leads-club": "copilot"},
}
