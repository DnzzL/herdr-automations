package config

import (
	"os"
	"path/filepath"
	"testing"
)

func withConfig(t *testing.T, yaml string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	if yaml != "" {
		if err := os.WriteFile(filepath.Join(dir, "automations.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	withConfig(t, "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Automations) != 0 {
		t.Fatalf("expected empty config, got %d entries", len(cfg.Automations))
	}
}

func TestLoadDefaultsAndValidation(t *testing.T) {
	withConfig(t, `
automations:
  - name: triage
    cron: "0 9 * * 1-5"
    repo: ~/Projects/foo
    prompt: "Triage the issues"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Automations[0]
	if a.Workspace != WorkspaceWorktree || a.Agent != "claude" || a.TimeoutMinutes != 60 {
		t.Fatalf("defaults not applied: %+v", a)
	}
	home, _ := os.UserHomeDir()
	if a.Repo != filepath.Join(home, "Projects/foo") {
		t.Fatalf("home not expanded: %s", a.Repo)
	}
}

func TestLineOfFindsTheEntry(t *testing.T) {
	withConfig(t, `automations:
  - name: first
    cron: "@daily"
    repo: /x
    prompt: p
  - name: second
    cron: "@daily"
    repo: /x
    prompt: p
`)
	if got := LineOf("second"); got != 6 {
		t.Fatalf("LineOf(second) = %d, want 6", got)
	}
	if got := LineOf("first"); got != 2 {
		t.Fatalf("LineOf(first) = %d, want 2", got)
	}
	if got := LineOf("missing"); got != 0 {
		t.Fatalf("LineOf(missing) = %d, want 0", got)
	}
}

func TestLoadRejectsBadEntries(t *testing.T) {
	cases := map[string]string{
		"bad cron": `
automations:
  - {name: a, cron: "not a cron", repo: /x, prompt: p}`,
		"prompt and workflow": `
automations:
  - {name: a, cron: "@daily", repo: /x, prompt: p, workflow: w}`,
		"neither prompt nor workflow": `
automations:
  - {name: a, cron: "@daily", repo: /x}`,
		"duplicate names": `
automations:
  - {name: a, cron: "@daily", repo: /x, prompt: p}
  - {name: a, cron: "@daily", repo: /x, prompt: p}`,
		"bad workspace": `
automations:
  - {name: a, cron: "@daily", repo: /x, prompt: p, workspace: sandbox}`,
		"model on a kind that takes none": `
automations:
  - {name: a, cron: "@daily", repo: /x, prompt: p, agent: droid, model: opus}`,
	}
	for label, yaml := range cases {
		t.Run(label, func(t *testing.T) {
			withConfig(t, yaml)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %s", label)
			}
		})
	}
}

func TestModelIsPassedThroughUnvalidated(t *testing.T) {
	// Model names change faster than any allowlist would survive, so anything
	// non-empty loads and the agent gets the final say.
	withConfig(t, `
automations:
  - {name: a, cron: "@daily", repo: /x, prompt: p, model: some-unreleased-model}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Automations[0].Model != "some-unreleased-model" {
		t.Fatalf("model not kept: %q", cfg.Automations[0].Model)
	}
}

func TestCollisionsReportsSharedOccurrencesOnce(t *testing.T) {
	withConfig(t, `
automations:
  - {name: early, cron: "0 6 * * *", repo: /x, prompt: p}
  - {name: also-early, cron: "0 6 * * *", repo: /x, prompt: p}
  - {name: alone, cron: "0 14 * * *", repo: /x, prompt: p}
  - {name: off, cron: "0 6 * * *", repo: /x, prompt: p, disabled: true}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	clashes := cfg.Collisions()
	// Both are daily, so they clash seven times over the horizon — but that is
	// one fact about the schedule, not seven.
	if len(clashes) != 1 {
		t.Fatalf("expected a single deduped collision, got %d: %+v", len(clashes), clashes)
	}
	if len(clashes[0].Names) != 2 {
		t.Fatalf("disabled entries must not collide: %+v", clashes[0].Names)
	}
}

func TestCollidesWithNamesTheClashingAutomations(t *testing.T) {
	withConfig(t, `
automations:
  - {name: sprint, cron: "0 9 * * 1", repo: /x, prompt: p}
  - {name: nightly, cron: "0 3 * * *", repo: /x, prompt: p}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.CollidesWith("0 9 * * 1"); len(got) != 1 || got[0] != "sprint" {
		t.Fatalf("expected sprint, got %v", got)
	}
	if got := cfg.CollidesWith("30 9 * * 1"); len(got) != 0 {
		t.Fatalf("expected no clash, got %v", got)
	}
}
