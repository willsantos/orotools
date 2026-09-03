package devmgr

import (
	"reflect"
	"testing"
)

func intp(v int) *int { return &v }

func TestBuildStartCommand(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		port     *int
		enforce  bool
		wantArgs []string
		wantEnv  []string
	}{
		{
			name:    "vite with port",
			cmd:     "vite dev",
			port:    intp(5174),
			enforce: true,
			wantArgs: []string{"vite", "dev", "--host", "127.0.0.1", "--port", "5174"},
			wantEnv:  []string{"PORT=5174", "HOST=127.0.0.1", "NODE_ENV=development"},
		},
		{
			name:    "wrangler",
			cmd:     "wrangler dev",
			port:    intp(8787),
			enforce: true,
			wantArgs: []string{"wrangler", "dev", "--port", "8787", "--ip", "127.0.0.1"},
			wantEnv:  []string{"PORT=8787", "HOST=127.0.0.1", "NODE_ENV=development"},
		},
		{
			name:    "next",
			cmd:     "next dev",
			port:    intp(3001),
			enforce: true,
			wantArgs: []string{"next", "dev", "--port", "3001", "--hostname", "127.0.0.1"},
			wantEnv:  []string{"PORT=3001", "HOST=127.0.0.1", "NODE_ENV=development"},
		},
		{
			name:    "npm-style wrapper",
			cmd:     "pnpm dev",
			port:    intp(3001),
			enforce: true,
			wantArgs: []string{"pnpm", "dev", "--", "--port", "3001"},
			wantEnv:  []string{"PORT=3001", "HOST=127.0.0.1", "NODE_ENV=development"},
		},
		{
			name:    "npm run wrapper",
			cmd:     "npm run dev",
			port:    intp(3001),
			enforce: true,
			wantArgs: []string{"npm", "run", "dev", "--", "--port", "3001"},
			wantEnv:  []string{"PORT=3001", "HOST=127.0.0.1", "NODE_ENV=development"},
		},
		{
			name:     "direct executable gets env only",
			cmd:      "bin/dev",
			port:     intp(9000),
			enforce:  true,
			wantArgs: []string{"bin/dev"},
			wantEnv:  []string{"PORT=9000", "HOST=127.0.0.1", "NODE_ENV=development"},
		},
		{
			name: "null port worker",
			cmd:  "npm run dev",
			port: nil,
			wantArgs: []string{"npm", "run", "dev"},
			wantEnv:  nil,
		},
		{
			name:    "quoted arg",
			cmd:     "pnpm dev --host '0.0.0.0'",
			port:    intp(3002),
			enforce: true,
			wantArgs: []string{"pnpm", "dev", "--host", "0.0.0.0", "--", "--port", "3002"},
			wantEnv:  []string{"PORT=3002", "HOST=127.0.0.1", "NODE_ENV=development"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := BuildStartCommand(tt.cmd, tt.port, tt.enforce)
			if !reflect.DeepEqual(sc.Args, tt.wantArgs) {
				t.Errorf("Args = %v, want %v", sc.Args, tt.wantArgs)
			}
			if !reflect.DeepEqual(sc.Env, tt.wantEnv) {
				t.Errorf("Env = %v, want %v", sc.Env, tt.wantEnv)
			}
		})
	}
}