package daemon

import (
	"os"
	"testing"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/notify"
	"github.com/DnzzL/herdr-automations/internal/runner"
)

type fakeSink struct {
	toasts int
}

func (f *fakeSink) NotificationShow(title, body string, sound herdr.Sound) error {
	f.toasts++
	return nil
}

func invalidRecords(t *testing.T) int {
	t.Helper()
	runs, err := history.Runs("", 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range runs {
		if r.Status == history.StatusInvalid {
			n++
		}
	}
	return n
}

func TestReportInvalidComplainsOncePerBrokenEntry(t *testing.T) {
	// evaluate runs every 30 seconds. Re-recording an unfixed typo would bury
	// the log and the board under the same line all day.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	cfg := &config.Config{Invalid: []config.Diagnostic{
		{Name: "broken", Line: 3, Message: "broken: invalid cron"},
	}}
	state := &scheduleState{Invalid: map[string]bool{}}

	if changed, _ := reportInvalid(cfg, state); !changed {
		t.Error("the first sighting is a change worth persisting")
	}
	if changed, _ := reportInvalid(cfg, state); changed {
		t.Error("the second tick has nothing new to say")
	}
	if got := invalidRecords(t); got != 1 {
		t.Fatalf("recorded %d invalid entries, want 1", got)
	}
}

func TestReportInvalidSpeaksUpAgainAfterAFixAndABreak(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	broken := &config.Config{Invalid: []config.Diagnostic{
		{Name: "broken", Line: 3, Message: "broken: invalid cron"},
	}}
	fixed := &config.Config{}
	state := &scheduleState{Invalid: map[string]bool{}}

	reportInvalid(broken, state)
	if changed, _ := reportInvalid(fixed, state); !changed {
		t.Error("forgetting a fixed entry is a change worth persisting")
	}
	if len(state.Invalid) != 0 {
		t.Fatalf("state still remembers %v", state.Invalid)
	}
	reportInvalid(broken, state)
	if got := invalidRecords(t); got != 2 {
		t.Fatalf("recorded %d invalid entries, want 2: breaking it again is news", got)
	}
}

func TestReportInvalidNamesAnEntryTooBrokenToHaveAName(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	cfg := &config.Config{Invalid: []config.Diagnostic{{Line: 7, Message: "cannot decode"}}}

	reportInvalid(cfg, &scheduleState{Invalid: map[string]bool{}})

	runs, err := history.Runs("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Automation != "(unnamed)" {
		t.Fatalf("records = %+v, want one attributed to (unnamed)", runs)
	}
}

func TestATickWithThreeBrokenEntriesToastsOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)

	yaml := `automations:
  - name: a
    cron: "not a cron"
    repo: /repo
    prompt: go
  - name: b
    cron: "not a cron either"
    repo: /repo
    prompt: go
  - name: c
    cron: "still not a cron"
    repo: /repo
    prompt: go
`
	if err := os.WriteFile(dir+"/automations.yaml", []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &fakeSink{}
	state := &scheduleState{Invalid: map[string]bool{}}
	evaluate(state, runner.New(nil), notify.With(sink))

	if sink.toasts != 1 {
		t.Fatalf("toasts = %d, want exactly one for the whole tick, regardless of file size", sink.toasts)
	}
}

func TestReportInvalidToastsOncePerBrokenEntryAcrossTicks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)

	yaml := `automations:
  - name: a
    cron: "not a cron"
    repo: /repo
    prompt: go
`
	if err := os.WriteFile(dir+"/automations.yaml", []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &fakeSink{}
	state := &scheduleState{Invalid: map[string]bool{}}
	evaluate(state, runner.New(nil), notify.With(sink))
	evaluate(state, runner.New(nil), notify.With(sink))

	if sink.toasts != 1 {
		t.Fatalf("toasts = %d, want the second tick to stay quiet about the same unfixed entry", sink.toasts)
	}
}

func TestTheDaemonStopsOnlyOnceNoSessionIsLeft(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sessions []string
		err      error
		want     bool
	}{
		{"its own session stopped, another runs", []string{"alt"}, nil, false},
		{"nothing runs", nil, nil, true},
		// Herdr unreachable is not Herdr gone: an orphan beats a machine
		// without a scheduler.
		{"cannot tell", nil, os.ErrNotExist, false},
	} {
		got := noSessionLeft(func() ([]string, error) { return tc.sessions, tc.err })
		if got != tc.want {
			t.Errorf("%s: noSessionLeft = %v, want %v", tc.name, got, tc.want)
		}
	}
}
