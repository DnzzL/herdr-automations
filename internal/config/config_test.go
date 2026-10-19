package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadCarriesTheLineEachEntryStartsOn(t *testing.T) {
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
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Automations[0].Line; got != 2 {
		t.Errorf("first starts on line %d, want 2", got)
	}
	if got := cfg.Automations[1].Line; got != 6 {
		t.Errorf("second starts on line %d, want 6", got)
	}
}

func TestLoadKeepsTheGoodEntriesWhenOneIsBad(t *testing.T) {
	// The failure this exists for: one error for the whole file meant a typo in
	// the seventh automation stopped the other six, silently.
	withConfig(t, `automations:
  - {name: fine, cron: "@daily", repo: /x, prompt: p}
  - {name: broken, cron: "0 99 * * *", repo: /x, prompt: p}
  - {name: also-fine, cron: "@hourly", repo: /x, prompt: p}
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("a single bad entry must not fail the load: %v", err)
	}
	if len(cfg.Automations) != 2 {
		t.Fatalf("loaded %d entries, want the two good ones", len(cfg.Automations))
	}
	if len(cfg.Invalid) != 1 {
		t.Fatalf("diagnostics = %+v, want one", cfg.Invalid)
	}
	d := cfg.Invalid[0]
	if d.Name != "broken" {
		t.Errorf("diagnostic names %q", d.Name)
	}
	if d.Line != 3 {
		t.Errorf("diagnostic points at line %d, want 3", d.Line)
	}
	if got := d.String(); !strings.Contains(got, "automations.yaml:3") {
		t.Errorf("String() = %q, want a file:line an editor can open", got)
	}
}

func TestLoadStillFailsOnAFileItCannotParse(t *testing.T) {
	// Nothing to run and nothing to point at: that is a real error.
	withConfig(t, "automations: [oh: dear\n  ]]] not yaml")
	if _, err := Load(); err == nil {
		t.Fatal("want an error for a file that is not YAML")
	}
}

func TestLoadDiagnosesAnEntryWhoseTypesAreWrong(t *testing.T) {
	withConfig(t, `automations:
  - {name: fine, cron: "@daily", repo: /x, prompt: p}
  - {name: odd, cron: "@daily", repo: /x, prompt: p, timeout_minutes: soon}
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("a malformed entry must not fail the load: %v", err)
	}
	if len(cfg.Automations) != 1 || len(cfg.Invalid) != 1 {
		t.Fatalf("loaded %d, invalid %d", len(cfg.Automations), len(cfg.Invalid))
	}
	// Even undecodable, the name is worth recovering: it is how the board and
	// `run` refer to the entry.
	if cfg.Invalid[0].Name != "odd" {
		t.Errorf("diagnostic names %q, want odd", cfg.Invalid[0].Name)
	}
}

func TestDiagnosticAndDeclaresSeeTheEntriesThatFailed(t *testing.T) {
	withConfig(t, `automations:
  - {name: broken, cron: "nope", repo: /x, prompt: p}
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Find("broken") != nil {
		t.Error("a broken entry must not be findable as runnable")
	}
	if cfg.Diagnostic("broken") == nil {
		t.Error(`Diagnostic("broken") = nil: "no automation named broken" is the wrong thing to say`)
	}
	if !cfg.Declares("broken") {
		t.Error("Declares() must see it, or the wizard writes a second one")
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

func TestModelFlagKindsAreKindsHerdrKnows(t *testing.T) {
	// `herdr agent start --kind` as of Herdr 0.8.2 — a hardcoded snapshot, not a
	// live check, so this only ever catches a typo in modelFlagKinds itself.
	herdrKnows := map[string]bool{
		"pi": true, "claude": true, "codex": true, "gemini": true, "cursor": true,
		"devin": true, "agy": true, "cline": true, "omp": true, "mastracode": true,
		"opencode": true, "copilot": true, "kimi": true, "kiro": true, "droid": true,
		"amp": true, "grok": true, "hermes": true, "kilo": true, "qodercli": true,
		"qwen": true, "maki": true,
	}
	for kind := range modelFlagKinds {
		if !herdrKnows[kind] {
			t.Errorf("modelFlagKinds has %q, which herdr agent start --kind does not accept", kind)
		}
	}
}

func TestLoadDiagnosesBadEntries(t *testing.T) {
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
  - {name: a, cron: "@daily", repo: /x, prompt: p, agent: devin, model: opus}`,
		"no name": `
automations:
  - {cron: "@daily", repo: /x, prompt: p}`,
		"no repo": `
automations:
  - {name: a, cron: "@daily", prompt: p}`,
	}
	for label, yaml := range cases {
		t.Run(label, func(t *testing.T) {
			withConfig(t, yaml)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("want a diagnostic, not a failed load: %v", err)
			}
			if len(cfg.Invalid) != 1 {
				t.Fatalf("diagnostics = %+v, want one for %s", cfg.Invalid, label)
			}
			// A duplicate keeps the first entry; everything else keeps none.
			want := 0
			if label == "duplicate names" {
				want = 1
			}
			if len(cfg.Automations) != want {
				t.Fatalf("%s: loaded %d entries, want %d", label, len(cfg.Automations), want)
			}
		})
	}
}

func TestSaveRefusesToDropTheEntriesThatDidNotLoad(t *testing.T) {
	// The regression this exists for: Load returns only the valid entries, so
	// marshalling a Config straight back deletes every broken one. The wizard
	// does exactly that — read, append, save — which would make
	// `herdr-automations add` silently eat an automation with a typo in it.
	withConfig(t, `automations:
  - {name: fine, cron: "@daily", repo: /x, prompt: p}
  - {name: broken, cron: "nope", repo: /x, prompt: p}
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if err := Save(cfg); err == nil {
		t.Fatal("want Save refused: it cannot write what it was never given")
	}
	// And the file is untouched.
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "broken") {
		t.Fatal("the broken entry was deleted from the file")
	}
}

func TestExistingWorkspaceValidation(t *testing.T) {
	a := Automation{Name: "inbox", Cron: "@hourly", Repo: "/repo", Prompt: "check", Workspace: WorkspaceExisting}
	if err := a.validate(); err == nil {
		t.Fatal("missing workspace_id accepted")
	}
	a.WorkspaceID = "w7"
	if err := a.validate(); err != nil {
		t.Fatal(err)
	}
	a.Workspace = WorkspaceRoot
	if err := a.validate(); err == nil {
		t.Fatal("ignored workspace_id accepted")
	}
}

func TestExistingWorkspaceSurvivesConfigRoundTrip(t *testing.T) {
	withConfig(t, `automations:
  - name: daily-summary
    cron: "@daily"
    repo: /repo
    workspace: existing
    workspace_id: w7
    prompt: Summarize recent changes.
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Invalid) != 0 || len(cfg.Automations) != 1 {
		t.Fatalf("config=%+v", cfg)
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Find("daily-summary")
	if a == nil || a.Workspace != WorkspaceExisting || a.WorkspaceID != "w7" {
		t.Fatalf("existing workspace was lost on save: %+v", a)
	}
}

func TestSessionNamesWhereTheRunHappens(t *testing.T) {
	withConfig(t, `
automations:
  - {name: triage, cron: "@daily", repo: /x, prompt: p, session: automations}
  - {name: here, cron: "@daily", repo: /x, prompt: p}
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Find("triage").Session; got != "automations" {
		t.Errorf("session = %q, want automations", got)
	}
	if got := cfg.Find("here").Session; got != "" {
		t.Errorf("session = %q, want empty: no key keeps today's behaviour", got)
	}
}

func TestASessionNameHerdrCouldNotResolveIsRefusedAtLoad(t *testing.T) {
	for _, bad := range []string{`"my session"`, `"../default"`, `" automations"`} {
		withConfig(t, "automations:\n  - {name: t, cron: \"@daily\", repo: /x, prompt: p, session: "+bad+"}\n")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Invalid) != 1 || !strings.Contains(cfg.Invalid[0].Message, "session") {
			t.Errorf("session %s: invalid = %+v, want one diagnostic about the session", bad, cfg.Invalid)
		}
	}
}
