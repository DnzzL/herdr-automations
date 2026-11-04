package host

import (
	"errors"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
)

func agentWorkWith(ops ops, a config.Automation) agentWork {
	return agentWork{ops: ops, knobs: fast(), a: a}
}

// ── starting the agent ──────────────────────────────────────────────

// A previous run nobody has read keeps its workspace, and with it the
// automation's agent name: the next run must still start (#13).
func TestStartTakesAnotherNameWhileAnUnreadRunHoldsIt(t *testing.T) {
	var names []string
	ops := &fakeOps{agentStart: func(name, _, _ string, _ []string) error {
		names = append(names, name)
		if name == "hourly-check" {
			return apiErr("agent start", herdr.CodeNameTaken)
		}
		return nil
	}}
	a := config.Automation{Name: "hourly-check", Workspace: config.WorkspaceWorktree}
	if err := agentWorkWith(ops, a).start(Session{PaneID: "w2:p1"}); err != nil {
		t.Fatalf("want the run to start under another name, got %v", err)
	}
	if len(names) != 2 || names[1] == names[0] {
		t.Fatalf("names tried %q", names)
	}
}

func TestStartRetriesUntilThePaneHasAShell(t *testing.T) {
	calls := 0
	ops := &fakeOps{agentStart: func(string, string, string, []string) error {
		calls++
		if calls < 3 {
			return apiErr("agent start", herdr.CodePaneBusy)
		}
		return nil
	}}

	if err := agentWorkWith(ops, config.Automation{}).start(Session{PaneID: "p"}); err != nil {
		t.Fatalf("want the third attempt to stick, got %v", err)
	}
	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
}

func TestStartGivesUpOnAPaneThatNeverComesUp(t *testing.T) {
	ops := &fakeOps{agentStart: func(string, string, string, []string) error {
		return apiErr("agent start", herdr.CodePaneBusy)
	}}
	w := agentWorkWith(ops, config.Automation{})
	w.knobs.paneReady = 20 * time.Millisecond

	err := w.start(Session{PaneID: "p"})
	if !herdr.HasCode(err, herdr.CodePaneBusy) {
		t.Fatalf("want the busy error reported, got %v", err)
	}
	if ops.starts < 2 {
		t.Errorf("tried %d times, want more than one attempt", ops.starts)
	}
}

func TestStartDoesNotRetryARealFailure(t *testing.T) {
	want := errors.New(`agent start: unknown kind "clyde"`)
	ops := &fakeOps{agentStart: func(string, string, string, []string) error { return want }}

	err := agentWorkWith(ops, config.Automation{}).start(Session{PaneID: "p"})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want it passed through", err)
	}
	if ops.starts != 1 {
		t.Errorf("tried %d times, want 1: a bad agent kind will not fix itself", ops.starts)
	}
}

func TestStartForwardsModelAndMCPConfigAheadOfAgentArgs(t *testing.T) {
	var got []string
	ops := &fakeOps{agentStart: func(_, _, _ string, args []string) error {
		got = args
		return nil
	}}
	a := config.Automation{
		Name: "n", Agent: "claude", Model: "sonnet",
		MCPConfig: "/mcp.json", AgentArgs: []string{"--verbose"},
	}

	if err := agentWorkWith(ops, a).start(Session{PaneID: "p"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"--mcp-config", "/mcp.json", "--model", "sonnet", "--verbose"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

// ── getting the prompt in front of it ───────────────────────────────

func TestSubmitIsDoneWhenHerdrConfirmsTheAgentReacted(t *testing.T) {
	ops := &fakeOps{}
	if err := agentWorkWith(ops, config.Automation{Prompt: "go"}).submit(Session{PaneID: "p"}); err != nil {
		t.Fatal(err)
	}
	if ops.pending != 0 {
		t.Errorf("pressed Enter %d times on a prompt that already landed", ops.pending)
	}
}

func TestSubmitTreatsAStallAsInconclusiveAndChecksTheStatus(t *testing.T) {
	// The failure this exists for: herdr calls a prompt stalled after five
	// seconds, but an agent still loading its MCP servers takes longer than
	// that to react. The text did land, so nudging further would double-type it.
	ops := &fakeOps{
		agentSubmit: func(string, string) error { return apiErr("agent prompt", herdr.CodeStalled) },
		agentStatus: func(string) (string, error) { return "working", nil },
	}

	if err := agentWorkWith(ops, config.Automation{Prompt: "go"}).submit(Session{PaneID: "p"}); err != nil {
		t.Fatalf("a stall with the agent working is not a failure: %v", err)
	}
	if ops.submits != 1 || ops.pending != 0 {
		t.Errorf("submits=%d pending=%d, want the prompt left alone", ops.submits, ops.pending)
	}
}

func TestSubmitPressesEnterForAnAgentSittingOnAFullComposer(t *testing.T) {
	ops := &fakeOps{
		agentSubmit: func(string, string) error { return apiErr("agent prompt", herdr.CodeStalled) },
	}
	// Idle until somebody presses Enter on the composer, then working.
	ops.agentStatus = func(string) (string, error) {
		if ops.pending > 0 {
			return "working", nil
		}
		return "idle", nil
	}

	if err := agentWorkWith(ops, config.Automation{Prompt: "go"}).submit(Session{PaneID: "p"}); err != nil {
		t.Fatalf("got %v, want the Enter press to have worked", err)
	}
	if ops.pending != 1 {
		t.Errorf("pressed Enter %d times, want 1", ops.pending)
	}
	if ops.submits != 1 {
		t.Errorf("retyped the prompt %d times, want 0", ops.submits-1)
	}
}

func TestSubmitRetypesThePromptWhenEnterChangedNothing(t *testing.T) {
	typed, checked := 0, 0
	ops := &fakeOps{}
	ops.agentSubmit = func(string, string) error {
		typed++
		return apiErr("agent prompt", herdr.CodeStalled)
	}
	ops.agentStatus = func(string) (string, error) {
		checked++
		// Idle through the first two windows; working once it was retyped.
		if typed >= 2 {
			return "working", nil
		}
		return "idle", nil
	}

	if err := agentWorkWith(ops, config.Automation{Prompt: "go"}).submit(Session{PaneID: "p"}); err != nil {
		t.Fatalf("got %v, want the retype to have worked", err)
	}
	if typed != 2 {
		t.Errorf("submitted %d times, want 2: once, then once more after Enter", typed)
	}
	if ops.pending != 1 {
		t.Errorf("pressed Enter %d times, want 1", ops.pending)
	}
	if checked == 0 {
		t.Error("never checked the status")
	}
}

func TestSubmitGivesUpOnAnAgentThatNeverStartsWorking(t *testing.T) {
	ops := &fakeOps{
		agentSubmit: func(string, string) error { return apiErr("agent prompt", herdr.CodeStalled) },
		agentStatus: func(string) (string, error) { return "idle", nil },
	}

	err := agentWorkWith(ops, config.Automation{Prompt: "go"}).submit(Session{PaneID: "p"})
	if err == nil {
		t.Fatal("want an error: nobody is going to read this prompt")
	}
	if ops.submits != 2 || ops.pending != 1 {
		t.Errorf("submits=%d pending=%d, want both nudges tried", ops.submits, ops.pending)
	}
}

func TestSubmitPassesThroughAnErrorThatIsNotAStall(t *testing.T) {
	ops := &fakeOps{
		agentSubmit: func(string, string) error { return apiErr("agent prompt", herdr.CodeAgentGone) },
	}

	err := agentWorkWith(ops, config.Automation{Prompt: "go"}).submit(Session{PaneID: "p"})
	if !herdr.HasCode(err, herdr.CodeAgentGone) {
		t.Fatalf("got %v, want the gone-agent error kept", err)
	}
	if ops.pending != 0 {
		t.Error("a vanished agent is not something Enter fixes")
	}
}

// ── waiting for it to finish ────────────────────────────────────────

func TestAwaitReturnsWhenTheAgentSettles(t *testing.T) {
	calls := 0
	ops := &fakeOps{agentWait: func(string, time.Duration) error {
		calls++
		if calls < 3 {
			return apiErr("agent wait", "timeout")
		}
		return nil
	}}

	if err := agentWorkWith(ops, config.Automation{}).await(Session{PaneID: "p"}, time.Hour); err != nil {
		t.Fatalf("got %v, want the settled agent reported as success", err)
	}
}

func TestAwaitGivesUpAsSoonAsTheWorkspaceIsClosed(t *testing.T) {
	// The failure this exists for: a run cancelled seconds in used to hold its
	// in-flight slot for the whole timeout. One slice is all it should cost now.
	ops := &fakeOps{
		agentWait:   func(string, time.Duration) error { return apiErr("agent wait", herdr.CodeAgentGone) },
		agentStatus: func(string) (string, error) { return "", apiErr("agent get", herdr.CodeWorkspaceGone) },
	}

	err := agentWorkWith(ops, config.Automation{}).await(Session{PaneID: "p"}, time.Hour)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("got %v, want ErrCancelled", err)
	}
	if ops.waits != 1 {
		t.Errorf("waited %d times, want 1: it should not wait out the timeout", ops.waits)
	}
}

func TestAwaitCallsAnAgentThatDiedInALiveWorkspaceAFailure(t *testing.T) {
	ops := &fakeOps{
		agentWait:   func(string, time.Duration) error { return apiErr("agent wait", herdr.CodeAgentGone) },
		agentStatus: func(string) (string, error) { return "idle", nil },
	}

	err := agentWorkWith(ops, config.Automation{}).await(Session{PaneID: "p"}, time.Hour)
	if errors.Is(err, ErrCancelled) {
		t.Fatalf("got ErrCancelled, want a real failure: the workspace answered, so the agent crashed")
	}
	if err == nil {
		t.Fatal("want an error for a dead agent")
	}
}

func TestAwaitCallsARepeatedAgentGoneAFailureToo(t *testing.T) {
	// AgentStatus is itself agent-scoped: the most likely answer for a dead
	// agent in a live workspace is another agent_not_running, not a status
	// string. That must still land as a crash, not fall through to cancelled.
	ops := &fakeOps{
		agentWait:   func(string, time.Duration) error { return apiErr("agent wait", herdr.CodeAgentGone) },
		agentStatus: func(string) (string, error) { return "", apiErr("agent get", herdr.CodeAgentGone) },
	}

	err := agentWorkWith(ops, config.Automation{}).await(Session{PaneID: "p"}, time.Hour)
	if errors.Is(err, ErrCancelled) {
		t.Fatalf("got ErrCancelled, want a real failure: agent_not_running twice still means the agent crashed")
	}
	if err == nil {
		t.Fatal("want an error for a dead agent")
	}
}

func TestAwaitStillCallsAClosedWorkspaceACancellation(t *testing.T) {
	ops := &fakeOps{
		agentWait:   func(string, time.Duration) error { return apiErr("agent wait", herdr.CodeAgentGone) },
		agentStatus: func(string) (string, error) { return "", apiErr("agent get", herdr.CodeWorkspaceGone) },
	}

	err := agentWorkWith(ops, config.Automation{}).await(Session{PaneID: "p"}, time.Hour)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("got %v, want ErrCancelled: this is the closed-workspace case, unchanged", err)
	}
}

func TestAwaitTreatsAnUnreadableStatusAsACancellation(t *testing.T) {
	ops := &fakeOps{
		agentWait:   func(string, time.Duration) error { return apiErr("agent wait", herdr.CodeAgentGone) },
		agentStatus: func(string) (string, error) { return "", apiErr("agent get", "some_other_error") },
	}

	err := agentWorkWith(ops, config.Automation{}).await(Session{PaneID: "p"}, time.Hour)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("got %v, want ErrCancelled: a doubt should not be turned into a failure", err)
	}
}

func TestAwaitNeverWaitsPastTheDeadline(t *testing.T) {
	// The fake sleeps the slice it is handed, as herdr does, so wall-clock time
	// is what bounds the loop.
	const timeout = 50 * time.Millisecond
	var asked []time.Duration
	ops := &fakeOps{agentWait: func(_ string, d time.Duration) error {
		asked = append(asked, d)
		time.Sleep(d)
		return apiErr("agent wait", "timeout")
	}}
	w := agentWorkWith(ops, config.Automation{})
	w.knobs.waitSlice = 10 * time.Millisecond

	started := time.Now()
	if err := w.await(Session{PaneID: "p"}, timeout); err == nil {
		t.Fatal("want the timeout reported")
	}
	for _, d := range asked {
		if d <= 0 || d > w.knobs.waitSlice {
			t.Fatalf("asked herdr to wait %s, want a slice within (0, %s]", d, w.knobs.waitSlice)
		}
	}
	// Generous: this asserts the deadline is honoured, not the scheduler's
	// precision.
	if elapsed := time.Since(started); elapsed > 4*timeout {
		t.Errorf("took %s for a %s timeout", elapsed, timeout)
	}
}

func TestAwaitReportsWhatHerdrLastSaid(t *testing.T) {
	// Better to surface herdr's own last word than a generic "still working".
	ops := &fakeOps{agentWait: func(string, time.Duration) error {
		return apiErr("agent wait", "timeout")
	}}
	w := agentWorkWith(ops, config.Automation{})
	w.knobs.waitSlice = time.Millisecond

	err := w.await(Session{PaneID: "p"}, 2*time.Millisecond)
	if !herdr.HasCode(err, "timeout") {
		t.Fatalf("got %v, want herdr's last error kept", err)
	}
}
