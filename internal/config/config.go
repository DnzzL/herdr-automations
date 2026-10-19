// Package config loads and validates automations.yaml, the single source of
// truth for the plugin. Everything else (wizard, pane, daemon) reads or
// rewrites this file.
//
// Loading is deliberately partial: an entry that does not validate becomes a
// Diagnostic and the rest of the file still runs. Returning one error for the
// whole file meant a typo in the seventh automation stopped the other six, and
// the daemon's only response was a log line nobody reads at 09:00. For a tool
// whose promise is "it ran while you slept", a silent absence is the worst
// failure mode there is.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"

	"github.com/DnzzL/herdr-automations/internal/hostpath"
)

// CronParser accepts the standard 5-field crontab syntax plus descriptors
// like @daily. Shared by the daemon and the wizard preview.
var CronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type Workspace string

const (
	WorkspaceWorktree Workspace = "worktree" // fresh git worktree per run
	WorkspaceExisting Workspace = "existing" // fresh tab in an existing workspace
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
	// WorkspaceID is required for existing mode. Each run gets a fresh tab at Repo.
	WorkspaceID string `yaml:"workspace_id,omitempty"`
	// Agent kind as understood by `herdr agent start --kind`; defaults to claude.
	Agent string `yaml:"agent,omitempty"`
	// Model handed to the agent executable as --model. Empty leaves the agent
	// on its own default, which may not be the one your quota can afford.
	// The value is passed through untouched: model names move faster than any
	// list we could keep here, so a typo surfaces when the agent refuses it.
	Model string `yaml:"model,omitempty"`
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
	// Session is the Herdr session the run happens in, whoever triggers it.
	// Empty means the session of the process running it (ADR 0010).
	Session string `yaml:"session,omitempty"`
	// Disabled keeps the entry in the file but out of the scheduler.
	Disabled bool `yaml:"disabled,omitempty"`

	// Line is where this entry starts in automations.yaml, 1-based, so an
	// editor can be opened right at it. Set by Load, never written back.
	Line int `yaml:"-"`
}

type Config struct {
	// Automations are the entries that loaded. Only these run.
	Automations []Automation `yaml:"automations"`
	// Invalid are the entries that did not, one Diagnostic each. Kept out of
	// Automations so nothing has to check before scheduling, and reported so
	// the failure is visible where you look rather than only in a log.
	Invalid []Diagnostic `yaml:"-"`
}

// Diagnostic is one entry that did not load, and where to go and fix it.
type Diagnostic struct {
	// Name of the automation, or empty when the entry is malformed enough that
	// even its name could not be read.
	Name string
	// Line the entry starts on, 1-based. 0 when it could not be located.
	Line    int
	Message string
}

// String renders a diagnostic the way an editor and a human both want it:
// file, line, then what is wrong.
func (d Diagnostic) String() string {
	where := filepath.Base(Path())
	if d.Line > 0 {
		where = fmt.Sprintf("%s:%d", where, d.Line)
	}
	return where + ": " + d.Message
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
	if a.Workspace != WorkspaceWorktree && a.Workspace != WorkspaceRoot && a.Workspace != WorkspaceExisting {
		return fmt.Errorf("%s: workspace must be worktree, root or existing, got %q", a.Name, a.Workspace)
	}
	if a.Workspace == WorkspaceExisting && strings.TrimSpace(a.WorkspaceID) == "" {
		return fmt.Errorf("%s: workspace_id is required for existing mode", a.Name)
	}
	if a.Workspace != WorkspaceExisting && a.WorkspaceID != "" {
		return fmt.Errorf("%s: workspace_id requires existing mode", a.Name)
	}
	// The name becomes a directory under Herdr's config dir; whether that
	// session is running is a 3am fact, not a load-time one.
	if a.Session != "" && (strings.ContainsAny(a.Session, "/\\ \t\n") || strings.HasPrefix(a.Session, ".")) {
		return fmt.Errorf("%s: session %q is not a Herdr session name", a.Name, a.Session)
	}
	if a.Model != "" && !KindAcceptsModel(a.Agent) {
		return fmt.Errorf("%s: agent %q takes no --model; put the flag it does take in agent_args", a.Name, a.Agent)
	}
	return nil
}

// modelFlagKinds are the agent kinds whose executable takes `--model`. Herdr
// forwards the flag verbatim, so a model: on any other kind would make the
// agent refuse to start — better to fail here, while the file is being
// written, than at 3am inside a scheduler goroutine.
//
// This is a known-good allowlist, not a gate: `herdr agent start --help`
// accepts more kinds than this table lists (see ADR 0003), and a kind missing
// here is a false rejection, not a safety net doing its job.
var modelFlagKinds = map[string]bool{
	"claude":   true,
	"codex":    true,
	"cursor":   true,
	"gemini":   true,
	"opencode": true,
	"grok":     true,
	"qwen":     true,
	"kimi":     true,
	"amp":      true,
	"droid":    true,
	"copilot":  true,
	"hermes":   true,
	"pi":       true,
}

// KindAcceptsModel reports whether this agent kind understands `--model`.
func KindAcceptsModel(kind string) bool { return modelFlagKinds[kind] }

// Dir is where automations.yaml lives.
func Dir() string { return hostpath.ConfigDir() }

// StateDir is where run history and scheduler state live.
func StateDir() string { return hostpath.StateDir() }

func Path() string { return filepath.Join(Dir(), "automations.yaml") }

// Load reads the config. A missing file is an empty config, not an error: the
// daemon idles until the first automation exists. An unreadable or unparseable
// file is an error — there is nothing to run and nothing to point at. Anything
// short of that is a Diagnostic on Config.Invalid, and the entries that did
// load still run.
func Load() (*Config, error) {
	raw, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	// Decoding through yaml.Node rather than straight into Config is what buys
	// the line numbers: a diagnostic that cannot say where to look is only
	// marginally better than silence.
	var doc struct {
		Automations []yaml.Node `yaml:"automations"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(), err)
	}

	cfg := &Config{}
	seen := map[string]bool{}
	for _, node := range doc.Automations {
		var a Automation
		if err := node.Decode(&a); err != nil {
			cfg.Invalid = append(cfg.Invalid, Diagnostic{
				Name: nameOf(node), Line: node.Line, Message: cleanYAMLError(err),
			})
			continue
		}
		a.Line = node.Line
		a.applyDefaults()
		a.Repo = expandHome(a.Repo)
		a.MCPConfig = expandHome(a.MCPConfig)

		if err := a.validate(); err != nil {
			cfg.Invalid = append(cfg.Invalid, Diagnostic{
				Name: a.Name, Line: a.Line, Message: err.Error(),
			})
			continue
		}
		if seen[a.Name] {
			cfg.Invalid = append(cfg.Invalid, Diagnostic{
				Name: a.Name, Line: a.Line,
				Message: fmt.Sprintf("%s: an automation by that name is already declared above", a.Name),
			})
			continue
		}
		seen[a.Name] = true
		cfg.Automations = append(cfg.Automations, a)
	}
	return cfg, nil
}

// nameOf recovers just the name from an entry too malformed to decode whole,
// so the diagnostic can still say which automation it is about.
func nameOf(node yaml.Node) string {
	var partial struct {
		Name string `yaml:"name"`
	}
	if node.Decode(&partial) != nil {
		return ""
	}
	return partial.Name
}

// cleanYAMLError trims the library's line prefix: the diagnostic carries the
// entry's own line, which is the one worth opening.
func cleanYAMLError(err error) string {
	msg := err.Error()
	if _, rest, found := strings.Cut(msg, ": "); found && strings.HasPrefix(msg, "yaml: line ") {
		return rest
	}
	return strings.TrimPrefix(msg, "yaml: ")
}

// Save writes the config back, creating the directory on first use.
//
// It refuses while anything in the file failed to load. Save marshals what the
// Config holds, and a Config holds only the entries that loaded — so writing
// one back over a file with a broken entry in it deletes that entry. The wizard
// does read-append-save, which is how `add` would silently eat an automation
// with a typo in it.
func Save(cfg *Config) error {
	if len(cfg.Invalid) > 0 {
		return fmt.Errorf("%s has %d entr%s that did not load; fix %s first",
			filepath.Base(Path()), len(cfg.Invalid),
			plural(len(cfg.Invalid), "y", "ies"), cfg.Invalid[0])
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), out, 0o644)
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

// Diagnostic returns the reason an automation by that name did not load, or
// nil. "No automation named x" is the wrong thing to say about one that is
// right there in the file with a broken cron.
func (c *Config) Diagnostic(name string) *Diagnostic {
	for i := range c.Invalid {
		if c.Invalid[i].Name == name {
			return &c.Invalid[i]
		}
	}
	return nil
}

// Declares reports whether the file mentions this name at all, valid or not.
// The wizard needs it: refusing a duplicate only when the existing entry
// happens to be valid writes a second one nobody asked for.
func (c *Config) Declares(name string) bool {
	return c.Find(name) != nil || c.Diagnostic(name) != nil
}

// plural picks a suffix without making the caller write a map literal.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
