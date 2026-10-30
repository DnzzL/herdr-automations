package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/history"
)

func setup(t *testing.T, yaml string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "automations.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// What the panel draws its list from: every entry, the broken ones too, with
// its next run and its last.
func TestListGivesThePanelEverythingItShows(t *testing.T) {
	setup(t, `backend: hc
paused: true
automations:
  - name: triage
    cron: "0 9 * * 1-5"
    repo: /x
    model: sonnet
    prompt: p
    disabled: true
  - name: broken
    cron: "0 99 * * *"
    repo: /x
    prompt: p
`)
	if err := history.Append(history.Record{RunID: "r1", Automation: "triage", Status: history.StatusDone, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	l, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if l.Backend != "hc" || !l.Paused || len(l.Automations) != 1 || len(l.Invalid) != 1 {
		t.Fatalf("got %+v", l)
	}
	a := l.Automations[0]
	if a.Name != "triage" || !a.Disabled || a.Model != "sonnet" || a.Next.IsZero() || a.Next.Hour() != 9 {
		t.Fatalf("automation %+v", a)
	}
	if a.Last == nil || a.Last.RunID != "r1" || a.Last.Status != "done" {
		t.Fatalf("last %+v", a.Last)
	}
	if l.Invalid[0].Name != "broken" || l.Invalid[0].Line == 0 {
		t.Fatalf("invalid %+v", l.Invalid)
	}
}

func TestHistoryCarriesDurationAndSeen(t *testing.T) {
	setup(t, "automations: []\n")
	base := time.Now().Add(-time.Hour)
	for _, r := range []history.Record{
		{RunID: "r1", Automation: "triage", Status: history.StatusRunning, At: base, PaneID: "t7"},
		{RunID: "r1", Automation: "triage", Status: history.StatusDone, At: base.Add(4 * time.Minute), PaneID: "t7"},
	} {
		if err := history.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := history.MarkSeen("r1"); err != nil {
		t.Fatal(err)
	}
	runs, err := History("", 10)
	if err != nil {
		t.Fatal(err)
	}
	r := runs[0]
	if r.Terminal != "t7" || !r.Seen || r.Seconds != 240 {
		t.Fatalf("got %+v", r)
	}
}
