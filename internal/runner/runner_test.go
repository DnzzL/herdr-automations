package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/host"
	"github.com/DnzzL/herdr-automations/internal/notify"
)

// fakeSink is notify's test seam (internal/notify/notify_test.go), reused here
// to assert the runner drives it rather than filtering statuses itself.
type fakeSink struct {
	titles []string
}

func (f *fakeSink) NotificationShow(title, body string, sound herdr.Sound) error {
	f.titles = append(f.titles, title)
	return nil
}

// fakeHost stands in for the machine. A nil field means the step works.
type fakeHost struct {
	provision func(config.Automation) (host.Session, error)
	do        func(host.Session, config.Automation, time.Duration) error

	mu       sync.Mutex
	timeouts []time.Duration
}

func (f *fakeHost) Provision(a config.Automation) (host.Session, error) {
	if f.provision == nil {
		return host.Session{WorkspaceID: "w1", PaneID: "w1:p1"}, nil
	}
	return f.provision(a)
}

func (f *fakeHost) Do(s host.Session, a config.Automation, timeout time.Duration) error {
	f.mu.Lock()
	f.timeouts = append(f.timeouts, timeout)
	f.mu.Unlock()
	if f.do == nil {
		return nil
	}
	return f.do(s, a, timeout)
}

// runs is the history as the board and `history` would read it back.
func runs(t *testing.T) []history.Record {
	t.Helper()
	out, err := history.Runs("", 0)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// statuses is every status written for an automation, oldest first. It reads
// the log raw: history.Runs collapses each run to its latest state, which is
// what the board wants and the opposite of what this asserts.
func statuses(t *testing.T, name string) []history.Status {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(config.StateDir(), "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []history.Status
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r history.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unreadable history line %q: %v", line, err)
		}
		if r.Automation == name {
			out = append(out, r.Status)
		}
	}
	return out
}

func automation() config.Automation {
	return config.Automation{
		Name: "triage", Cron: "@daily", Repo: "/repo", Agent: "claude",
		Workspace: config.WorkspaceWorktree, Prompt: "go", TimeoutMinutes: 45,
	}
}

func TestRunRecordsTheWholeTrailOfASuccessfulRun(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	h := &fakeHost{}

	if err := New(h).Run(automation(), "cron"); err != nil {
		t.Fatal(err)
	}

	// One run ID, collapsing to done, with the workspace it happened in
	// attached — that last part is what the board's `enter` jumps to.
	got := runs(t)
	if len(got) != 1 {
		t.Fatalf("want a single run, got %d: %+v", len(got), got)
	}
	if got[0].Status != history.StatusDone {
		t.Errorf("status = %q, want done", got[0].Status)
	}
	if got[0].WorkspaceID != "w1" || got[0].PaneID != "w1:p1" {
		t.Errorf("run recorded without a workspace to jump to: %+v", got[0])
	}
	if got[0].Trigger != "cron" {
		t.Errorf("trigger = %q", got[0].Trigger)
	}
	if len(h.timeouts) != 1 || h.timeouts[0] != 45*time.Minute {
		t.Errorf("timeouts = %v, want the automation's 45m", h.timeouts)
	}
}

func TestRunPassesThroughScheduledAndRunning(t *testing.T) {
	// The board reads the latest state per run, so a run that is still working
	// has to have said so. Losing the running record makes an in-flight run
	// look like it never started.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())

	if err := New(&fakeHost{}).Run(automation(), "manual"); err != nil {
		t.Fatal(err)
	}

	want := []history.Status{history.StatusScheduled, history.StatusRunning, history.StatusDone}
	got := statuses(t, "triage")
	if len(got) != len(want) {
		t.Fatalf("trail = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trail = %v, want %v", got, want)
		}
	}
}

func TestRunRecordsAFailureToProvision(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	h := &fakeHost{provision: func(config.Automation) (host.Session, error) {
		return host.Session{}, errors.New("worktree create: fatal: not a git repository")
	}}

	err := New(h).Run(automation(), "cron")
	if err == nil {
		t.Fatal("want the provisioning error returned")
	}
	last := runs(t)[0]
	if last.Status != history.StatusFailed {
		t.Errorf("status = %q, want failed", last.Status)
	}
	if last.Error == "" {
		t.Error("a failed run has to say why")
	}
}

func TestRunRecordsAClosedWorkspaceAsCancelledNotFailed(t *testing.T) {
	// Closing a run's workspace is how you call one off. Nothing broke,
	// somebody decided — and the board dims it rather than colouring it red.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	h := &fakeHost{do: func(host.Session, config.Automation, time.Duration) error {
		return fmt.Errorf("waiting for the agent: %w", host.ErrCancelled)
	}}

	if err := New(h).Run(automation(), "cron"); err == nil {
		t.Fatal("want the cancellation returned")
	}
	if got := runs(t)[0].Status; got != history.StatusCancelled {
		t.Errorf("status = %q, want cancelled", got)
	}
}

func TestRunSkipsAnAutomationThatIsStillWorking(t *testing.T) {
	// The 9:00 run is still going at 10:00: the occurrence is dropped, not
	// queued, and the drop is recorded so it isn't a silent absence.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	release := make(chan struct{})
	entered := make(chan struct{})
	h := &fakeHost{do: func(host.Session, config.Automation, time.Duration) error {
		close(entered)
		<-release
		return nil
	}}
	r := New(h)

	// Waited on before returning: a run still recording after t.Setenv is
	// undone writes into the real history.
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(automation(), "cron") }()
	<-entered

	if !r.Busy() {
		t.Error("Busy() = false with a run in flight")
	}
	err := r.Run(automation(), "cron")
	if err == nil {
		t.Fatal("want the second run refused")
	}
	close(release)
	<-done

	var skipped bool
	for _, rec := range runs(t) {
		if rec.Status == history.StatusSkipped {
			skipped = true
		}
	}
	if !skipped {
		t.Error("a skipped occurrence has to leave a record")
	}
}

func TestRunLetsADifferentAutomationThrough(t *testing.T) {
	// The in-flight set is per automation: a nightly job holding a slot must
	// not block the morning triage.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	release := make(chan struct{})
	entered := make(chan struct{})
	h := &fakeHost{do: func(_ host.Session, a config.Automation, _ time.Duration) error {
		if a.Name == "triage" {
			close(entered)
			<-release
		}
		return nil
	}}
	r := New(h)

	// Waited on before returning: a run still recording after t.Setenv is
	// undone writes into the real history.
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(automation(), "cron") }()
	<-entered

	other := automation()
	other.Name = "nightly"
	if err := r.Run(other, "cron"); err != nil {
		t.Fatalf("a different automation was blocked: %v", err)
	}
	close(release)
	<-done
}

func TestBusyIsFalseOnceARunFinishes(t *testing.T) {
	// The daemon re-execs itself on a plugin upgrade only when nothing is in
	// flight. A slot that leaks means it never upgrades.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	r := New(&fakeHost{do: func(host.Session, config.Automation, time.Duration) error {
		return errors.New("boom")
	}})

	_ = r.Run(automation(), "cron")
	if r.Busy() {
		t.Error("Busy() = true after the run failed: the slot leaked")
	}
}

func TestRunToastsAFailureAndNothingBefore(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	h := &fakeHost{do: func(host.Session, config.Automation, time.Duration) error {
		return errors.New("boom")
	}}
	sink := &fakeSink{}
	r := NewWith(h, notify.With(sink))

	_ = r.Run(automation(), "cron")

	if len(sink.titles) != 1 {
		t.Fatalf("toasts = %v, want exactly one despite scheduled/running also being recorded", sink.titles)
	}
	if sink.titles[0] != "triage failed" {
		t.Errorf("title = %q", sink.titles[0])
	}
}

func TestRunNotifiesTheNotifierOfEveryStatusItRecords(t *testing.T) {
	// Deciding what deserves a toast is notify's job; a runner that pre-filters
	// puts the policy in two places.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	sink := &fakeSink{}
	r := NewWith(&fakeHost{}, notify.With(sink))

	if err := r.Run(automation(), "cron"); err != nil {
		t.Fatal(err)
	}
	if len(sink.titles) != 0 {
		t.Fatalf("toasts = %v, want none for a run that succeeded", sink.titles)
	}
}

func TestRunWithoutANotifierStillRuns(t *testing.T) {
	// The board and the CLI build a Runner with New/Default, which carries no
	// notifier — a nil one must not panic.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	if err := New(&fakeHost{}).Run(automation(), "cron"); err != nil {
		t.Fatal(err)
	}
}

func TestStatusForSeparatesACancellationFromAFailure(t *testing.T) {
	cancelled := fmt.Errorf("waiting for the agent: %w", host.ErrCancelled)
	if got := statusFor(cancelled); got != history.StatusCancelled {
		t.Errorf("a closed workspace recorded as %q, want cancelled", got)
	}
	if got := statusFor(errors.New("start claude agent: boom")); got != history.StatusFailed {
		t.Errorf("a real failure recorded as %q, want failed", got)
	}
}

func TestHistoryRemembersWhichSessionARunLivesIn(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	h := &fakeHost{provision: func(config.Automation) (host.Session, error) {
		return host.Session{WorkspaceID: "w1", PaneID: "w1:p1", HerdrSession: "automations"}, nil
	}}
	if err := New(h).Run(config.Automation{Name: "triage"}, history.TriggerManual); err != nil {
		t.Fatal(err)
	}
	if got := runs(t)[0].Session; got != "automations" {
		t.Fatalf("session = %q, want automations: the board cannot find w1 without it", got)
	}
}
