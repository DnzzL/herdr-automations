package host

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
)

// fakeOps scripts Herdr's answers. A nil field means "this call is fine and
// says nothing", which keeps each test to the calls it actually cares about.
type fakeOps struct {
	tabCreate       func(workspaceID, cwd, label string) (string, error)
	worktreeCreate  func(repo, branch, label string) (string, string, error)
	workspaceCreate func(cwd, label string) (string, string, error)
	agentStart      func(name, kind, paneID string, args []string) error
	agentSubmit     func(target, text string) error
	submitPending   func(paneID string) error
	agentStatus     func(target string) (string, error)
	agentWait       func(target string, d time.Duration) error
	paneRun         func(paneID string, command ...string) error
	paneRead        func(paneID string, lines int) (string, error)
	lookPath        func(file string) error

	// session is what In bound the fake to, read by a test at call time to
	// see which session a call was made in. current answers CurrentSession.
	session string
	current string

	starts  int
	submits int
	pending int
	waits   int
	reads   int
}

func (f *fakeOps) WorktreeCreate(repo, branch, label string) (string, string, error) {
	if f.worktreeCreate == nil {
		return "w1", "w1:p1", nil
	}
	return f.worktreeCreate(repo, branch, label)
}

func (f *fakeOps) WorkspaceCreate(cwd, label string) (string, string, error) {
	if f.workspaceCreate == nil {
		return "w2", "w2:p1", nil
	}
	return f.workspaceCreate(cwd, label)
}

func (f *fakeOps) TabCreate(workspaceID, cwd, label string) (string, error) {
	if f.tabCreate == nil {
		return workspaceID + ":p1", nil
	}
	return f.tabCreate(workspaceID, cwd, label)
}

func (f *fakeOps) AgentStart(name, kind, paneID string, args []string) error {
	f.starts++
	if f.agentStart == nil {
		return nil
	}
	return f.agentStart(name, kind, paneID, args)
}

func (f *fakeOps) AgentSubmit(target, text string) error {
	f.submits++
	if f.agentSubmit == nil {
		return nil
	}
	return f.agentSubmit(target, text)
}

func (f *fakeOps) AgentSubmitPending(paneID string) error {
	f.pending++
	if f.submitPending == nil {
		return nil
	}
	return f.submitPending(paneID)
}

func (f *fakeOps) AgentStatus(target string) (string, error) {
	if f.agentStatus == nil {
		return "working", nil
	}
	return f.agentStatus(target)
}

func (f *fakeOps) AgentWait(target string, d time.Duration) error {
	f.waits++
	if f.agentWait == nil {
		return nil
	}
	return f.agentWait(target, d)
}

func (f *fakeOps) PaneRun(paneID string, command ...string) error {
	if f.paneRun == nil {
		return nil
	}
	return f.paneRun(paneID, command...)
}

func (f *fakeOps) PaneRead(paneID string, lines int) (string, error) {
	f.reads++
	if f.paneRead == nil {
		return "", nil
	}
	return f.paneRead(paneID, lines)
}

func (f *fakeOps) LookPath(file string) error {
	if f.lookPath == nil {
		return nil
	}
	return f.lookPath(file)
}

func (f *fakeOps) In(session string) ops {
	f.session = session
	return f
}

func (f *fakeOps) CurrentSession() string {
	if f.session != "" {
		return f.session
	}
	return f.current
}

// HasCode is the real thing: the fakes return real *herdr.APIError values, so
// the code-recovery path under test is the one that ships.
func (f *fakeOps) HasCode(err error, code string) bool { return herdr.HasCode(err, code) }

// fast is defaultKnobs with the sleeps taken out.
func fast() knobs {
	return knobs{
		paneReady:        time.Minute,
		paneReadyPoll:    time.Millisecond,
		waitSlice:        30 * time.Second,
		statusPoll:       time.Millisecond,
		promptSettle:     5 * time.Millisecond,
		promptSettleLast: 5 * time.Millisecond,
		workflowPoll:     time.Millisecond,
	}
}

func apiErr(command, code string) error {
	return &herdr.APIError{Command: command, Code: code, Message: code}
}

func TestProvisionOpensAWorktreeOnABranchOfItsOwn(t *testing.T) {
	var gotBranch, gotLabel string
	ops := &fakeOps{worktreeCreate: func(repo, branch, label string) (string, string, error) {
		gotBranch, gotLabel = branch, label
		return "w7", "w7:p1", nil
	}}
	h := &live{ops: ops, knobs: fast()}

	s, err := h.Provision(config.Automation{
		Name: "Weekly sprint planning", Repo: "/x", Workspace: config.WorkspaceWorktree,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.WorkspaceID != "w7" || s.PaneID != "w7:p1" {
		t.Fatalf("session = %+v", s)
	}
	// git check-ref-format rejects spaces, so the name has to be slugged before
	// it can become a branch.
	if want := "auto/weekly-sprint-planning-"; len(gotBranch) <= len(want) || gotBranch[:len(want)] != want {
		t.Errorf("branch = %q, want it to start with %q", gotBranch, want)
	}
	if gotLabel != "auto: Weekly sprint planning" {
		t.Errorf("label = %q", gotLabel)
	}
}

func TestProvisionRootModeOpensTheRepoItself(t *testing.T) {
	called := false
	ops := &fakeOps{workspaceCreate: func(cwd, label string) (string, string, error) {
		called = true
		if cwd != "/repo" {
			t.Errorf("cwd = %q", cwd)
		}
		return "w9", "w9:p1", nil
	}}
	h := &live{ops: ops, knobs: fast()}

	if _, err := h.Provision(config.Automation{
		Name: "n", Repo: "/repo", Workspace: config.WorkspaceRoot,
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("root mode must not create a worktree")
	}
}

func TestProvisionRefusesAWorkspaceModeItDoesNotKnow(t *testing.T) {
	// Load validates this, so reaching here means something bypassed it. Saying
	// so beats provisioning nothing and reporting success.
	h := &live{ops: &fakeOps{}, knobs: fast()}
	if _, err := h.Provision(config.Automation{Name: "n", Workspace: "sandbox"}); err == nil {
		t.Fatal("want an error for an unknown workspace mode")
	}
}

func TestDoPicksTheWorkflowAdapterWhenOneIsNamed(t *testing.T) {
	ops := &fakeOps{
		paneRead: func(string, int) (string, error) { return "", errors.New("no screen") },
		lookPath: func(string) error { return errors.New("not found") },
	}
	h := &live{ops: ops, knobs: fast()}

	err := h.Do(Session{PaneID: "p"}, config.Automation{Workflow: "bump"}, time.Minute)
	if err == nil || ops.starts != 0 {
		t.Fatalf("want the hwf adapter chosen, got err=%v starts=%d", err, ops.starts)
	}
}

func TestDoPicksTheAgentAdapterWhenThereIsAPrompt(t *testing.T) {
	ops := &fakeOps{}
	h := &live{ops: ops, knobs: fast()}

	if err := h.Do(Session{PaneID: "p"}, config.Automation{
		Agent: "claude", Prompt: "go", Name: "n",
	}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if ops.starts != 1 || ops.submits != 1 || ops.waits != 1 {
		t.Fatalf("starts=%d submits=%d waits=%d, want one of each", ops.starts, ops.submits, ops.waits)
	}
}

func TestSlugProducesValidBranchNames(t *testing.T) {
	cases := map[string]string{
		"Weekly sprint planning": "weekly-sprint-planning",
		"issue-triage":           "issue-triage",
		"Deps  bump!!":           "deps-bump",
		"  ~weird/name~  ":       "weird-name",
		"???":                    "automation",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgentNameFitsHerdrsLimit(t *testing.T) {
	long := "a-very-long-automation-name-that-herdr-will-not-accept"
	got := agentName(long)
	if len(got) > 32 {
		t.Fatalf("agentName(%q) = %q, %d chars", long, got, len(got))
	}
	if got[len(got)-1] == '-' {
		t.Errorf("agentName(%q) = %q, want no trailing dash", long, got)
	}
}

func TestProvisionExistingUsesFreshTabsInTheSameWorkspace(t *testing.T) {
	calls := 0
	ops := &fakeOps{
		workspaceCreate: func(string, string) (string, string, error) {
			t.Fatal("created a workspace")
			return "", "", nil
		},
		worktreeCreate: func(string, string, string) (string, string, error) {
			t.Fatal("created a worktree")
			return "", "", nil
		},
		tabCreate: func(workspaceID, cwd, label string) (string, error) {
			if workspaceID != "w7" || cwd != "/repo" || !strings.HasPrefix(label, "auto: inbox ") {
				t.Fatalf("unexpected tab: %q %q %q", workspaceID, cwd, label)
			}
			calls++
			return fmt.Sprintf("w7:p%d", calls), nil
		},
	}
	h := &live{ops: ops, knobs: fast()}
	a := config.Automation{Name: "inbox", Repo: "/repo", Workspace: config.WorkspaceExisting, WorkspaceID: "w7"}
	first, err := h.Provision(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.Provision(a)
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkspaceID != second.WorkspaceID || first.PaneID == second.PaneID || calls != 2 {
		t.Fatalf("runs must share a workspace with distinct panes: %+v %+v", first, second)
	}
	ops.tabCreate = func(string, string, string) (string, error) { return "", errors.New("workspace gone") }
	if _, err := h.Provision(a); err == nil {
		t.Fatal("missing workspace must fail, never create a replacement")
	}
}

func TestExistingRunsHaveDistinctLiveAgentNames(t *testing.T) {
	seen := map[string]bool{}
	ops := &fakeOps{agentStart: func(name, kind, paneID string, args []string) error {
		if seen[name] || len(name) > 32 {
			t.Fatalf("invalid or reused name %q", name)
		}
		seen[name] = true
		return nil
	}}
	w := agentWork{ops: ops, knobs: fast(), a: config.Automation{Name: strings.Repeat("long-name", 8), Workspace: config.WorkspaceExisting}}
	// Herdr's pane IDs are case-sensitive; agent names are lowercase only. w1D
	// and w1d are two different live tabs and must not fold onto one name.
	for _, pane := range []string{"w7:p1", "w7:p2", "w1D:p1", "w1d:p1", "w17:pB", "w17:pb"} {
		if err := w.start(Session{PaneID: pane}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentNameIsTheAutomationsOutsideExistingMode(t *testing.T) {
	// The pane ID only earns its place where earlier runs stay open. Elsewhere
	// the sidebar should keep reading "inbox", not "w7-p1-inbox".
	for _, mode := range []config.Workspace{config.WorkspaceWorktree, config.WorkspaceRoot} {
		a := config.Automation{Name: "inbox", Workspace: mode}
		if got := agentNameFor(a, "w7:p1"); got != "inbox" {
			t.Errorf("agentNameFor(%s) = %q, want %q", mode, got, "inbox")
		}
	}
}

func TestARunNamingASessionIsProvisionedAndWorkedThere(t *testing.T) {
	ops := &fakeOps{current: "default"}
	var provisionedIn, startedIn string
	ops.worktreeCreate = func(string, string, string) (string, string, error) {
		provisionedIn = ops.session
		return "w7", "w7:p1", nil
	}
	ops.agentStart = func(string, string, string, []string) error {
		startedIn = ops.session
		return nil
	}
	h := &live{ops: ops, knobs: fast()}
	a := config.Automation{Name: "n", Repo: "/r", Workspace: config.WorkspaceWorktree,
		Prompt: "p", Session: "automations"}

	s, err := h.Provision(a)
	if err != nil {
		t.Fatal(err)
	}
	if provisionedIn != "automations" || s.HerdrSession != "automations" {
		t.Fatalf("provisioned in %q, session = %+v; want automations", provisionedIn, s)
	}
	if err := h.Do(s, a, time.Minute); err != nil {
		t.Fatal(err)
	}
	if startedIn != "automations" {
		t.Fatalf("agent started in %q, want automations", startedIn)
	}
}

func TestARunNamingNoSessionHappensWhereItWasTriggered(t *testing.T) {
	ops := &fakeOps{current: "alt"}
	h := &live{ops: ops, knobs: fast()}
	s, err := h.Provision(config.Automation{Name: "n", Repo: "/r", Workspace: config.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	if ops.session != "" || s.HerdrSession != "alt" {
		t.Fatalf("bound to %q, session = %+v; want unbound, recorded as alt", ops.session, s)
	}
}
