package host

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/hc"
)

// fakeHC scripts herdr-core and git: each Wait pops the next answer.
type fakeHC struct {
	cwd      string
	labels   map[string]string
	argv     []string
	worktree [3]string // repo, branch, path
	waits    []hc.Wait
	term     hc.Terminal
	runErr   error
	delay    time.Duration // per Wait
}

func (f *fakeHC) Run(cwd string, labels map[string]string, argv []string) (string, error) {
	f.cwd, f.labels, f.argv = cwd, labels, argv
	return "t7", f.runErr
}

func (f *fakeHC) Wait(string, time.Duration) (hc.Wait, error) {
	time.Sleep(f.delay)
	if len(f.waits) == 0 {
		return hc.Wait{Reason: "timeout"}, nil
	}
	w := f.waits[0]
	f.waits = f.waits[1:]
	return w, nil
}

func (f *fakeHC) Get(string) (hc.Terminal, error) { return f.term, nil }

func (f *fakeHC) MainCheckout(string) (string, error) { return "/src/myapp", nil }

func (f *fakeHC) WorktreeAdd(repo, branch, path string) error {
	f.worktree = [3]string{repo, branch, path}
	return nil
}

func newHCTest(f *fakeHC) *hcHost {
	return &hcHost{ops: f, home: "/home/me", knobs: knobs{
		paneReady: 30 * time.Millisecond, waitSlice: time.Millisecond, statusPoll: time.Millisecond,
	}}
}

func hcAutomation() config.Automation {
	return config.Automation{
		Name: "Issue triage", Repo: "/src/myapp/sub", Workspace: config.WorkspaceWorktree,
		Agent: "claude", Model: "sonnet", Prompt: "Triage the issues",
	}
}

// A run made the way the app's own "New worktree…" makes one, so the app
// nests it under its repo: same root, same folder name for the branch, and a
// space id the app has never seen.
func TestHCProvisionMakesAWorktreeTheAppRecognises(t *testing.T) {
	f := &fakeHC{}
	s, err := newHCTest(f).Provision(hcAutomation())
	if err != nil {
		t.Fatal(err)
	}
	repo, branch, path := f.worktree[0], f.worktree[1], f.worktree[2]
	if repo != "/src/myapp/sub" || !strings.HasPrefix(branch, "auto/issue-triage-") {
		t.Fatalf("worktree add %q %q", repo, branch)
	}
	if want := filepath.Join("/home/me/.herdr/worktrees/myapp", strings.ReplaceAll(branch, "/", "-")); path != want {
		t.Fatalf("path %q, want %q", path, want)
	}
	if f.cwd != path {
		t.Fatalf("agent started in %q, not the worktree", f.cwd)
	}
	space, tab := f.labels["herdr.space"], f.labels["herdr.tab"]
	if !strings.HasPrefix(space, "sp") || len(space) != 34 || !strings.HasPrefix(tab, "tb") || len(tab) != 34 {
		t.Fatalf("ids not in the app's shape: space %q tab %q", space, tab)
	}
	if f.labels["automation"] != "Issue triage" {
		t.Fatalf("labels %v", f.labels)
	}
	if got := strings.Join(f.argv, " "); got != "claude --model sonnet Triage the issues" {
		t.Fatalf("argv %q", got)
	}
	if s.WorkspaceID != space || s.PaneID != "t7" {
		t.Fatalf("session %+v", s)
	}
}

// ADR 0009: the space is the user's, named in the file, never guessed.
func TestHCProvisionExistingOpensATabInTheNamedSpace(t *testing.T) {
	f := &fakeHC{}
	a := hcAutomation()
	a.Workspace, a.WorkspaceID = config.WorkspaceExisting, "sp123"
	s, err := newHCTest(f).Provision(a)
	if err != nil {
		t.Fatal(err)
	}
	if f.worktree[0] != "" || f.cwd != a.Repo || f.labels["herdr.space"] != "sp123" || s.WorkspaceID != "sp123" {
		t.Fatalf("worktree %v cwd %q labels %v", f.worktree, f.cwd, f.labels)
	}
}

func TestHCProvisionHandsATemplateItsTask(t *testing.T) {
	f := &fakeHC{}
	a := hcAutomation()
	a.Agent, a.Model, a.Template = "", "", "reviewer"
	if _, err := newHCTest(f).Provision(a); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.argv, "|"); got != "reviewer|Triage the issues" {
		t.Fatalf("argv %q", got)
	}
}

func TestHCDo(t *testing.T) {
	code := 2
	cases := []struct {
		name    string
		waits   []hc.Wait
		term    hc.Terminal
		want    string // "" = success, "cancelled" = ErrCancelled, else a substring
		timeout time.Duration
		slow    bool // each wait takes longer than paneReady
	}{
		{name: "done", waits: []hc.Wait{{Ended: true, Reason: "status", Status: "done", Turn: 1}}},
		{name: "idle after its turn", waits: []hc.Wait{{Ended: true, Reason: "status", Status: "idle", Turn: 1}}},
		{name: "blocked, then done", waits: []hc.Wait{
			{Ended: true, Reason: "status", Status: "blocked", Turn: 1},
			{Ended: true, Reason: "status", Status: "done", Turn: 1},
		}},
		{name: "idle before the prompt landed is not done", waits: []hc.Wait{
			{Ended: true, Reason: "status", Status: "idle", Turn: 0},
			{Ended: true, Reason: "status", Status: "done", Turn: 1},
		}},
		{name: "space closed in the app", waits: []hc.Wait{{Ended: true, Reason: "exit"}},
			term: hc.Terminal{State: "finished", Reason: "killed"}, want: "cancelled"},
		{name: "agent exited", waits: []hc.Wait{{Ended: true, Reason: "exit", ExitCode: &code}},
			term: hc.Terminal{State: "finished", Reason: "exited", ExitCode: &code}, want: "exited with code 2"},
		{name: "agent error", waits: []hc.Wait{{Ended: true, Reason: "status", Status: "error", Turn: 1}}, want: "error"},
		// pi counts its turn when the turn ends, Claude when it starts: working
		// at turn 0 for longer than paneReady is pi on a long task, not a dialog.
		{name: "pi working at turn 0, then done", waits: []hc.Wait{
			{Ended: false, Reason: "timeout", Status: "working", Turn: 0},
			{Ended: false, Reason: "timeout", Status: "working", Turn: 0},
			{Ended: true, Reason: "status", Status: "done", Turn: 1},
		}, slow: true},
		// A trust dialog: no status at all, ever. Without a bound the run would
		// hold its slot for the whole timeout.
		{name: "never started", want: "never started working", timeout: time.Second},
		{name: "still working at the deadline", waits: []hc.Wait{
			{Ended: true, Reason: "status", Status: "working", Turn: 1},
		}, want: "still working", timeout: 5 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeHC{waits: c.waits, term: c.term, delay: map[bool]time.Duration{true: 40 * time.Millisecond}[c.slow]}
			h := newHCTest(f)
			if c.name == "still working at the deadline" {
				h.knobs.paneReady = time.Hour
			}
			timeout := c.timeout
			if timeout == 0 {
				timeout = time.Second
			}
			err := h.Do(Session{PaneID: "t7"}, hcAutomation(), timeout)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("want success, got %v", err)
			case c.want == "cancelled" && !errors.Is(err, ErrCancelled):
				t.Fatalf("want cancelled, got %v", err)
			case c.want != "" && c.want != "cancelled" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}
