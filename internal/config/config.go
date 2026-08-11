// Package config loads and validates automations.yaml, the single source of
// truth for the plugin. Everything else (wizard, pane, daemon) reads or
// rewrites this file.
package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// CronParser accepts the standard 5-field crontab syntax plus descriptors
// like @daily. Shared by the daemon and the wizard preview.
var CronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type Workspace string

const (
	WorkspaceWorktree Workspace = "worktree" // fresh git worktree per run
	WorkspaceRoot     Workspace = "root"     // workspace on the repo root
)

// Automation is one scheduled entry: a prompt (or delegated workflow) fired
// on a cron schedule against an agent in a provisioned workspace.
type Automation struct {
	Name string `yaml:"name"`
	Cron string `yaml:"cron"`
	// Repo the automation operates on (absolute path, ~ expanded).
	Repo string `yaml:"repo"`
	// Workspace provisioning mode; defaults to worktree.
	Workspace Workspace `yaml:"workspace,omitempty"`
	// Agent kind as understood by `herdr agent start --kind`; defaults to claude.
	Agent string `yaml:"agent,omitempty"`
	// Prompt submitted to the agent. Mutually exclusive with Workflow.
	Prompt string `yaml:"prompt,omitempty"`
	// Workflow delegates execution to herdr-workflows: `hwf run <name>`.
	Workflow string `yaml:"workflow,omitempty"`
	// MCPConfig is a path to an MCP servers JSON passed to the agent
	// (claude: --mcp-config). Sugar over AgentArgs.
	MCPConfig string `yaml:"mcp_config,omitempty"`
	// AgentArgs are extra args appended to the agent command verbatim.
	AgentArgs []string `yaml:"agent_args,omitempty"`
	// TimeoutMinutes bounds the agent prompt --wait; defaults to 60.
	TimeoutMinutes int `yaml:"timeout_minutes,omitempty"`
	// CatchUpMinutes is how late a missed occurrence may still run — the
	// laptop-was-asleep case. 0 uses the default (120); -1 never catches up.
	CatchUpMinutes int `yaml:"catch_up_minutes,omitempty"`
	// Disabled keeps the entry in the file but out of the scheduler.
	Disabled bool `yaml:"disabled,omitempty"`
}

type Config struct {
	Automations []Automation `yaml:"automations"`
}

func (a *Automation) applyDefaults() {
	if a.Workspace == "" {
		a.Workspace = WorkspaceWorktree
	}
	if a.Agent == "" {
		a.Agent = "claude"
	}
	if a.TimeoutMinutes <= 0 {
		a.TimeoutMinutes = 60
	}
	if a.CatchUpMinutes == 0 {
		a.CatchUpMinutes = 120
	}
}

// CatchUp is how late this automation may still start; negative means never.
func (a Automation) CatchUp() time.Duration {
	if a.CatchUpMinutes < 0 {
		return 0
	}
	return time.Duration(a.CatchUpMinutes) * time.Minute
}

func (a *Automation) validate() error {
	if a.Name == "" {
		return fmt.Errorf("automation without a name")
	}
	if a.Cron == "" {
		return fmt.Errorf("%s: missing cron", a.Name)
	}
	if _, err := CronParser.Parse(a.Cron); err != nil {
		return fmt.Errorf("%s: invalid cron %q: %w", a.Name, a.Cron, err)
	}
	if a.Repo == "" {
		return fmt.Errorf("%s: missing repo", a.Name)
	}
	if (a.Prompt == "") == (a.Workflow == "") {
		return fmt.Errorf("%s: exactly one of prompt or workflow is required", a.Name)
	}
	if a.Workspace != WorkspaceWorktree && a.Workspace != WorkspaceRoot {
		return fmt.Errorf("%s: workspace must be worktree or root, got %q", a.Name, a.Workspace)
	}
	return nil
}

// PluginID must match the id in herdr-plugin.toml: it locates the same
// directories Herdr hands the daemon when the CLI is run from a plain shell.
const PluginID = "dnzzl.automations"

// Dir resolves the config directory. Under Herdr the env var is authoritative;
// from a plain shell we ask the herdr CLI, so `herdr-automations list` in a
// terminal always sees what the daemon sees.
func Dir() string {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	if out, err := exec.Command(herdrBin(), "plugin", "config-dir", PluginID).Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return d
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr", "plugins", "config", PluginID)
}

// StateDir resolves where run history lives. Herdr exposes no state-dir
// command, so outside a plugin context we mirror its layout.
func StateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "herdr", "plugins", PluginID)
}

func herdrBin() string {
	if b := os.Getenv("HERDR_BIN_PATH"); b != "" {
		return b
	}
	return "herdr"
}

func Path() string { return filepath.Join(Dir(), "automations.yaml") }

// Load reads, defaults and validates the config. A missing file is an empty
// config, not an error: the daemon idles until the first automation exists.
func Load() (*Config, error) {
	raw, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(), err)
	}
	seen := map[string]bool{}
	for i := range cfg.Automations {
		a := &cfg.Automations[i]
		a.applyDefaults()
		a.Repo = expandHome(a.Repo)
		a.MCPConfig = expandHome(a.MCPConfig)
		if err := a.validate(); err != nil {
			return nil, err
		}
		if seen[a.Name] {
			return nil, fmt.Errorf("duplicate automation name %q", a.Name)
		}
		seen[a.Name] = true
	}
	return &cfg, nil
}

// Save writes the config back, creating the directory on first use.
func Save(cfg *Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), out, 0o644)
}

// LineOf returns the 1-based line where an automation is declared, so an
// editor can open the file right at it. 0 means "not found — open the top".
func LineOf(name string) int {
	raw, err := os.ReadFile(Path())
	if err != nil {
		return 0
	}
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "name:") {
			continue
		}
		// Matches both "- name: x" and a "name: x" line under a "-" bullet.
		if _, value, found := strings.Cut(trimmed, "name:"); found {
			if strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`)) == name {
				return i + 1
			}
		}
	}
	return 0
}

// Find returns the automation named name, or nil.
func (c *Config) Find(name string) *Automation {
	for i := range c.Automations {
		if c.Automations[i].Name == name {
			return &c.Automations[i]
		}
	}
	return nil
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
