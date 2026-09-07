// Package devmgr implements the `oro dev` subcommand: a Go replacement for the
// Clientes Dev Manager bash script, managing start/stop/logs of client
// projects driven by projects.config.json (zero migration).
package devmgr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Project mirrors one entry of the `projects` object in projects.config.json.
// Group is optional (dev-project-groups FR-1); it must stay last so the
// emitted JSON appends the field after description, keeping legacy configs
// byte-identical on round-trip (omitempty + field order, FR-2).
type Project struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	Cwd            string `json:"cwd"`
	PackageManager string `json:"package_manager"`
	DevCommand     string `json:"dev_command"`
	Port           *int   `json:"port"`
	Description    string `json:"description"`
	Group          string `json:"group,omitempty"`
}

// WorkDir returns the directory the dev process runs in: Cwd, falling back
// to Path when Cwd is empty — the process must spawn in the same place
// regardless of where oro was invoked from (dev-pid-tracking FR-2).
func (p *Project) WorkDir() string {
	if p.Cwd != "" {
		return p.Cwd
	}
	return p.Path
}

// Settings mirrors the `settings` object in projects.config.json.
type Settings struct {
	LogDir            string `json:"log_dir"`
	PidDir            string `json:"pid_dir"`
	EnforceUniquePorts bool  `json:"enforce_unique_ports"`
}

// Config is the parsed project config. The order of project keys is preserved
// (insertion order) so saves produce a byte-identical file to the Node-written
// config, honoring FR-2's "diff limpo" requirement.
type Config struct {
	projects map[string]*Project
	order    []string
	Settings Settings

	// pid/log dirs anchored by Load to the config's directory (FR-1). Empty
	// when the Config was hand-built — the accessors then fall back to the
	// raw Settings values.
	pidDirResolved string
	logDirResolved string
}

// PidDir returns the pid directory: anchored to the config's directory when
// the config was loaded from disk, Settings.PidDir verbatim otherwise.
func (c *Config) PidDir() string {
	if c.pidDirResolved != "" {
		return c.pidDirResolved
	}
	return c.Settings.PidDir
}

// LogDir is PidDir's counterpart for the log directory.
func (c *Config) LogDir() string {
	if c.logDirResolved != "" {
		return c.logDirResolved
	}
	return c.Settings.LogDir
}

// resolveDirs anchors pid_dir/log_dir independently of the invocation CWD
// (FR-1), each field on its own: empty → default subdirectory of the config
// dir; relative → joined with the config dir; absolute → unchanged. Settings
// keeps the raw values so Save/Marshal never persist the resolved paths.
func (c *Config) resolveDirs(base string) {
	c.pidDirResolved = anchorDir(c.Settings.PidDir, base, ".dev-pids")
	c.logDirResolved = anchorDir(c.Settings.LogDir, base, ".dev-logs")
}

func anchorDir(dir, base, def string) string {
	if dir == "" {
		return filepath.Join(base, def)
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(base, dir)
}

var (
	ErrConfigNotFound = errors.New("devmgr: config file not found")
	ErrProjectExists  = errors.New("devmgr: project already exists")
)

// Projects returns a fresh map snapshot (no aliasing) of all projects.
func (c *Config) Projects() map[string]*Project {
	out := make(map[string]*Project, len(c.projects))
	for k, v := range c.projects {
		cp := *v
		out[k] = &cp
	}
	return out
}

// Lookup returns the project for key.
func (c *Config) Lookup(key string) (*Project, bool) {
	p, ok := c.projects[key]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// Keys returns project keys in insertion order.
func (c *Config) Keys() []string {
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// Add inserts or replaces a project, preserving existing insertion position.
func (c *Config) Add(key string, p *Project) {
	if _, ok := c.projects[key]; ok {
		c.projects[key] = p
		return
	}
	if c.projects == nil {
		c.projects = map[string]*Project{}
	}
	c.projects[key] = p
	c.order = append(c.order, key)
}

// Delete removes a project.
func (c *Config) Delete(key string) {
	if _, ok := c.projects[key]; !ok {
		return
	}
	delete(c.projects, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// Len returns the number of projects.
func (c *Config) Count() int {
	if c.projects == nil {
		return 0
	}
	return len(c.projects)
}

// ResolveConfigPath returns the config path honoring --config override,
// falling back to $CLIENTES_HOME/projects.config.json (FR-3). The result is
// always absolute so the anchor used for relative pid/log dirs does not
// depend on the invocation CWD (FR-1).
func ResolveConfigPath(clientsHome, override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if clientsHome == "" {
		return "", errors.New("devmgr: CLIENTES_HOME not set and no --config given")
	}
	return filepath.Abs(filepath.Join(clientsHome, "projects.config.json"))
}

// Load reads and parses the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if err == os.ErrNotExist {
			return nil, fmt.Errorf("%w: %s", ErrConfigNotFound, path)
		}
		return nil, err
	}
	c := &Config{}
	if err := c.parse(data); err != nil {
		return nil, fmt.Errorf("devmgr: parse %s: %w", path, err)
	}
	c.resolveDirs(filepath.Dir(path))
	return c, nil
}

// Save writes the config with 2-space indentation, byte-identical to the
// Node/JSON.stringify format plus trailing newline (FR-2).
func (c *Config) Save(path string) error {
	data, err := c.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Marshal serializes the config preserving insertion order of projects.
func (c *Config) Marshal() ([]byte, error) {
	var sb strings.Builder
	sb.WriteString(`{"projects":{`)
	first := true
	for _, k := range c.order {
		p, ok := c.projects[k]
		if !ok || p == nil {
			continue
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshal project %q: %w", k, err)
		}
		if !first {
			sb.WriteString(",")
		}
		first = false
		sb.Write(kb)
		sb.WriteString(":")
		sb.Write(vb)
	}
	sb.WriteString(`},"settings":`)
	sb2, err := json.Marshal(c.Settings)
	if err != nil {
		return nil, err
	}
	sb.Write(sb2)
	sb.WriteString("}")

	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(sb.String()), "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func (c *Config) parse(data []byte) error {
	// Stream-parse the top-level object by hand to preserve project order.
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("expected top-level object")
	}
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return errors.New("expected string key")
		}
		switch key {
		case "projects":
			if err := c.parseProjects(dec); err != nil {
				return err
			}
		case "settings":
			if err := dec.Decode(&c.Settings); err != nil {
				return err
			}
		default:
			var skip json.RawMessage
			dec.Decode(&skip)
		}
	}
	if c.projects == nil {
		c.projects = map[string]*Project{}
	}
	return nil
}

func (c *Config) parseProjects(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("expected projects object")
	}
	c.projects = map[string]*Project{}
	c.order = nil
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return errors.New("expected project key")
		}
		var p Project
		if err := dec.Decode(&p); err != nil {
			return err
		}
		c.projects[key] = &p
		c.order = append(c.order, key)
	}
	_, err = dec.Token() // closing '}'
	return err
}