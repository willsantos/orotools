package planner

// networkCommands lists commands whose execution typically requires network
// access (package managers, installers, fetchers). Used as a heuristic to flag
// network operations in the plan (spec section 64). git is treated as local
// here; remote clone/push are owned by the repository provider (fase 12).
var networkCommands = map[string]bool{
	"npx":  true,
	"npm":  true,
	"pnpm": true,
	"yarn": true,
	"bun":  true,
	"bunx": true,
	"pip":  true,
	"pipx": true,
	"uv":   true,
	"uvx":  true,
	"cargo": true,
	"go":   true,
}

func isNetwork(command string) bool {
	return networkCommands[command]
}
