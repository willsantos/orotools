package recipe

// This file contains the canonical Go types for Recipe schema v1.
// Field semantics are defined in the spec technique (sections 9, 10, 13, 15-22, 32-42, 67).
// Resolution/evaluation of variables, conditions and requirements is handled by
// downstream features; this package only parses and structurally validates.

// Recipe is the root of recipe schema v1.
type Recipe struct {
	Version            int                          `yaml:"version"`
	Name               string                       `yaml:"name"`
	Description        string                       `yaml:"description"`
	Requirements       []Requirement                `yaml:"requirements"`
	Variables          map[string]Variable          `yaml:"variables"`
	Steps              []Step                       `yaml:"steps"`
	Skills             Skills                       `yaml:"skills"`
	Agents             Agents                       `yaml:"agents"`
	ExternalInstallers map[string]ExternalInstaller `yaml:"external_installers"`
}

// Variable is a recipe-level input declared for interactive/non-interactive resolution.
type Variable struct {
	Type    string   `yaml:"type"`    // string | boolean | select
	Prompt  string   `yaml:"prompt"`
	Default any      `yaml:"default"` // string|bool; resolved by recipe-variables
	Options []string `yaml:"options"` // populated for select
}

// Requirement is an external tool dependency declared by a recipe.
type Requirement struct {
	Command string     `yaml:"command"`
	Version string     `yaml:"version"` // e.g. ">=10"; empty means presence check only
	When    *Condition `yaml:"when"`
}

// Step models one primitive operation. Step uses a single struct with a Type
// discriminator plus all possible body fields (spec section 15-22). Required
// fields per type are enforced by Validate.
type Step struct {
	ID     string     `yaml:"id"`
	Type   string     `yaml:"type"` // mkdir|copy|template|exec|message
	Stage  string     `yaml:"stage"`
	When   *Condition `yaml:"when"`
	SkipIf *Condition `yaml:"skip_if"`

	// mkdir
	Path string `yaml:"path"`

	// copy / template
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
	Mode        string `yaml:"mode"` // template only: create|overwrite (default create)

	// exec
	Command          string   `yaml:"command"`
	Args             []string `yaml:"args"`
	WorkingDirectory string   `yaml:"working_directory"`

	// message
	Text string `yaml:"text"`
}

// Condition models a structured when/skip_if clause. Recursive via All/Any.
// PathExists is used by skip_if (spec section 22); evaluation lives in recipe-conditions.
type Condition struct {
	Variable   string      `yaml:"variable"`
	Equals     *any        `yaml:"equals"`
	NotEquals  *any        `yaml:"not_equals"`
	All        []Condition `yaml:"all"`
	Any        []Condition `yaml:"any"`
	PathExists string      `yaml:"path_exists"`
}

// Skills bundles bundled and external skill references.
type Skills struct {
	Bundled  []string        `yaml:"bundled"`
	External []ExternalSkill `yaml:"external"`
}

// ExternalSkill references skills provided by an external installer.
type ExternalSkill struct {
	Installer string   `yaml:"installer"`
	Skills    []string `yaml:"skills"`
}

// Agents bundles bundled and external agent references.
type Agents struct {
	Bundled  []string        `yaml:"bundled"`
	External []ExternalAgent `yaml:"external"`
}

// ExternalAgent references agents provided by an external installer.
type ExternalAgent struct {
	Installer string   `yaml:"installer"`
	Agents    []string `yaml:"agents"`
}

// ExternalInstaller declares a third-party installer runtime and its operations.
type ExternalInstaller struct {
	Runtime string                   `yaml:"runtime"` // npx|command|... (extensible)
	Package ExternalInstallerPackage `yaml:"package"`
	Install ExternalInstallerOp      `yaml:"install"`
	Update  ExternalInstallerOp      `yaml:"update"`
	Check   ExternalInstallerOp      `yaml:"check"`
	Network bool                     `yaml:"network"`
}

// ExternalInstallerPackage identifies the runtime package of an installer.
type ExternalInstallerPackage struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// ExternalInstallerOp models one operation (install/update/check) of an installer.
type ExternalInstallerOp struct {
	Args        []string `yaml:"args"`
	Interactive bool     `yaml:"interactive"`
}
