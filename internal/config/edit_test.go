package config

import (
	"os"
	"strings"
	"testing"
)

func fileText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

const commented = `# my automations
backend: hc
automations:
  # mornings
  - name: triage
    cron: "0 9 * * 1-5"   # weekdays
    repo: /x
    prompt: p
  - name: broken
    cron: "0 99 * * *"
    repo: /x
    prompt: p
`

// The panel edits one entry; the rest of the file is the user's, comments and
// broken siblings included. Save rewrites everything and refuses while one
// entry is broken, which would lock the panel out of the whole file.
func TestPutReplacesOneEntryAndKeepsTheRest(t *testing.T) {
	withConfig(t, commented)
	a := Automation{Name: "triage", Cron: "0 8 * * 1-5", Repo: "/x", Prompt: "new prompt"}
	if res, err := Put("triage", a, false); err != nil || res.Problem != "" {
		t.Fatalf("put: %v %q", err, res.Problem)
	}
	text := fileText(t)
	for _, want := range []string{"# my automations", "# mornings", "new prompt", "0 8 * * 1-5", "name: broken"} {
		if !strings.Contains(text, want) {
			t.Errorf("file lost %q:\n%s", want, text)
		}
	}
}

func TestPutAppendsANewEntry(t *testing.T) {
	withConfig(t, commented)
	a := Automation{Name: "digest", Cron: "0 17 * * 5", Repo: "/x", Prompt: "sum up"}
	if res, err := Put("", a, false); err != nil || res.Problem != "" {
		t.Fatalf("put: %v %q", err, res.Problem)
	}
	cfg, _ := Load()
	if cfg.Find("digest") == nil || cfg.Find("triage") == nil {
		t.Fatalf("got %+v", cfg.Automations)
	}
}

func TestPutRefusesWhatWouldNotLoadAndWritesNothing(t *testing.T) {
	withConfig(t, commented)
	before := fileText(t)
	a := Automation{Name: "triage", Cron: "nope", Repo: "/x", Prompt: "p"}
	res, err := Put("triage", a, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Problem, "invalid cron") {
		t.Fatalf("problem %q", res.Problem)
	}
	if fileText(t) != before {
		t.Fatal("a refused put changed the file")
	}
}

func TestPutRefusesANameAlreadyTaken(t *testing.T) {
	withConfig(t, commented)
	a := Automation{Name: "broken", Cron: "@daily", Repo: "/x", Prompt: "p"}
	res, err := Put("", a, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Problem, "already") {
		t.Fatalf("problem %q", res.Problem)
	}
}

func TestPutDryRunPreviewsAndWritesNothing(t *testing.T) {
	withConfig(t, commented)
	before := fileText(t)
	a := Automation{Name: "triage", Cron: "0 8 * * 1-5", Repo: "/x", Prompt: "p"}
	res, err := Put("triage", a, true)
	if err != nil || res.Problem != "" {
		t.Fatalf("put: %v %q", err, res.Problem)
	}
	if len(res.Next) != 3 || res.Next[0].Hour() != 8 {
		t.Fatalf("next %v", res.Next)
	}
	if fileText(t) != before {
		t.Fatal("dry run changed the file")
	}
}

func TestRemoveAndPauseKeepComments(t *testing.T) {
	withConfig(t, commented)
	if err := SetPaused("", true); err != nil {
		t.Fatal(err)
	}
	if err := SetPaused("triage", true); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load()
	if !cfg.Paused || !cfg.Find("triage").Disabled {
		t.Fatalf("paused %v, triage %+v", cfg.Paused, cfg.Find("triage"))
	}
	if err := Remove("broken"); err != nil {
		t.Fatal(err)
	}
	text := fileText(t)
	if strings.Contains(text, "broken") || !strings.Contains(text, "# weekdays") {
		t.Fatalf("got:\n%s", text)
	}
	if err := Remove("ghost"); err == nil {
		t.Fatal("removing a name that is not there must say so")
	}
}

// An edit must not reformat the entries it did not touch. yaml.v3 does
// normalise the gap before an inline comment to one space; that is all.
func TestEditsKeepTheFilesIndentation(t *testing.T) {
	for _, file := range []string{commented, strings.ReplaceAll(commented, "\n  ", "\n    ")} {
		withConfig(t, file)
		if err := SetPaused("", true); err != nil {
			t.Fatal(err)
		}
		if err := SetPaused("", false); err != nil {
			t.Fatal(err)
		}
		if got, want := fileText(t), strings.ReplaceAll(file, `"   # weekdays`, `" # weekdays`); got != want {
			t.Fatalf("round trip changed the file:\n--- before\n%s--- after\n%s", file, got)
		}
	}
}
