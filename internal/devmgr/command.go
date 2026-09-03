package devmgr

import (
	"fmt"
	"strings"
)

// StartCommand is the resolved argv for a project's dev server plus the
// environment injected for port enforcement (FR-10).
type StartCommand struct {
	Args []string
	Env  []string
}

// BuildStartCommand ports the bash _build_start_command `case`:
//
//	*wrangler* → --port P --ip 127.0.0.1
//	*vite*     → --host 127.0.0.1 --port P
//	*next*     → --port P --hostname 127.0.0.1
//	*          → -- --port P
//
// Env PORT/HOST/NODE_ENV is always injected when the command is a known
// wrapper so next/vite respect the single source of truth. When
// enforce_unique_ports is disabled, or the port is null (workers), the raw
// command is returned unchanged (no flags, no PORT env).
func BuildStartCommand(devCommand string, port *int, enforce bool) StartCommand {
	raw := strings.TrimSpace(devCommand)
	if raw == "" {
		return StartCommand{}
	}
	if !enforce || port == nil {
		return StartCommand{Args: splitArgs(raw)}
	}

	argv := splitArgs(raw)
	var flags []string
	switch {
	case strings.Contains(raw, "wrangler") || hasCfPrefix(raw):
		flags = []string{"--port", fmt.Sprintf("%d", *port), "--ip", "127.0.0.1"}
	case strings.Contains(raw, "vite"):
		flags = []string{"--host", "127.0.0.1", "--port", fmt.Sprintf("%d", *port)}
	case strings.Contains(raw, "next") || strings.Contains(raw, "nextjs"):
		flags = []string{"--port", fmt.Sprintf("%d", *port), "--hostname", "127.0.0.1"}
	default:
		flags = []string{"--", "--port", fmt.Sprintf("%d", *port)}
	}
	argv = append(argv, flags...)

	return StartCommand{
		Args: argv,
		Env: []string{
			fmt.Sprintf("PORT=%d", *port),
			"HOST=127.0.0.1",
			"NODE_ENV=development",
		},
	}
}

// hasCfPrefix treats bare `cf dev` / `npx cf` as a wrangler-style worker.
func hasCfPrefix(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return false
	}
	return strings.HasPrefix(fields[0], "cf") || fields[0] == "wrangler"
}

// splitArgs breaks a dev_command into argv, handling simple quoted strings.
func splitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	quote := rune(0)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}