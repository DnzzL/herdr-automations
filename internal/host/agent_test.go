package host

import (
	"errors"
	"strings"
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
	if err := agentWorkWith(ops, a).start(Session{PaneID: "w2:p1"}, ""); err != nil {
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

	if err := agentWorkWith(ops, config.Automation{}).start(Session{PaneID: "p"}, ""); err != nil {
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

	err := w.start(Session{PaneID: "p"}, "")
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

	err := agentWorkWith(ops, config.Automation{}).start(Session{PaneID: "p"}, "")
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want it passed through", err)
	}
	if ops.starts != 1 {
		t.Errorf("tried %d times, want 1: a bad agent kind will not fix itself", ops.starts)
	}
}

func TestStartForwardsModelAndMCPConfigAheadOfAgentArgs(t *testing.T) {
	for _, tt := range []struct {
		agent string
		want  []string
	}{
		{"claude", []string{"--mcp-config", "/mcp config.json", "--model", "sonnet", "--verbose"}},
		{"copilot", []string{"--additional-mcp-config", "@/mcp config.json", "--model", "sonnet", "--verbose"}},
	} {
		t.Run(tt.agent, func(t *testing.T) {
			var got []string
			ops := &fakeOps{agentStart: func(_, _, _ string, args []string) error {
				got = args
				return nil
			}}
			a := config.Automation{
				Name: "n", Agent: tt.agent, Model: "sonnet",
				MCPConfig: "/mcp config.json", AgentArgs: []string{"--verbose"},
			}

			if err := agentWorkWith(ops, a).start(Session{PaneID: "p"}, a.Model); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("args = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("args = %v, want %v", got, tt.want)
				}
			}
		})
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

// ── model fallback ─────────────────────────────────────────────────

// A model that is gone or out of quota fails the start; the run tries the
// next fallback_models entry in the same workspace, with only --model moved.
func TestFallbackRetriesStartWithTheNextModel(t *testing.T) {
	var models []string
	ops := &fakeOps{
		agentStart: func(_, _, _ string, args []string) error {
			for i, a := range args {
				if a == "--model" && i+1 < len(args) {
					models = append(models, args[i+1])
				}
			}
			if len(models) == 1 {
				return errors.New("model not found")
			}
			return nil
		},
		agentSubmit: func(string, string) error { return nil },
		agentWait:   func(string, time.Duration) error { return nil },
	}
	a := config.Automation{
		Name: "nightly", Agent: "claude", Model: "opus",
		FallbackModels: []string{"sonnet", "haiku"},
	}
	if err := agentWorkWith(ops, a).do(Session{PaneID: "p"}, time.Minute); err != nil {
		t.Fatalf("want the fallback to carry the run, got %v", err)
	}
	if len(models) != 2 || models[0] != "opus" || models[1] != "sonnet" {
		t.Fatalf("models tried %q, want [opus sonnet]", models)
	}
}

// A prompt that fails is final: the agent is up and holds the pane, and a
// prompt herdr calls stalled may still run. No second agent is started.
func TestNoFallbackWhenThePromptFails(t *testing.T) {
	starts := 0
	ops := &fakeOps{
		agentStart: func(_, _, _ string, _ []string) error { starts++; return nil },
		agentSubmit: func(string, string) error {
			return errors.New("provider returned 429: quota exhausted")
		},
	}
	a := config.Automation{
		Name: "nightly", Agent: "pi", Model: "a/a",
		FallbackModels: []string{"b/b"},
	}
	err := agentWorkWith(ops, a).do(Session{PaneID: "p"}, time.Minute)
	if err == nil || strings.Contains(err.Error(), "models tried") {
		t.Fatalf("got %v, want the single prompt failure", err)
	}
	if starts != 1 {
		t.Errorf("started %d agents, want 1: a failed prompt is not retried", starts)
	}
}

// Every model refused: the error names each one and why, and still unwraps to
// the first model's refusal.
func TestFallbackExhaustedListsEveryAttempt(t *testing.T) {
	first := errors.New("unknown model opus")
	ops := &fakeOps{agentStart: func(_, _, _ string, args []string) error {
		if args[1] == "opus" {
			return first
		}
		return errors.New("unknown model " + args[1])
	}}
	a := config.Automation{
		Name: "nightly", Agent: "claude", Model: "opus",
		FallbackModels: []string{"sonnet", "haiku"},
	}
	err := agentWorkWith(ops, a).do(Session{PaneID: "p"}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "3 models tried") {
		t.Fatalf("got %v, want all three reported", err)
	}
	for _, want := range []string{`"opus": start claude agent: unknown model opus`, `"sonnet"`, `"haiku": start claude agent: unknown model haiku`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if !errors.Is(err, first) {
		t.Errorf("want the first refusal reachable through errors.Is")
	}
}

// A pane that never became a shell will not take the next model either: the
// run reports the busy pane instead of waiting it out once per model.
func TestNoFallbackOnABusyPane(t *testing.T) {
	ops := &fakeOps{agentStart: func(string, string, string, []string) error {
		return apiErr("agent start", herdr.CodePaneBusy)
	}}
	a := config.Automation{
		Name: "nightly", Agent: "claude", Model: "opus",
		FallbackModels: []string{"sonnet"},
	}
	w := agentWorkWith(ops, a)
	w.knobs.paneReady = 20 * time.Millisecond
	err := w.do(Session{PaneID: "p"}, time.Minute)
	if !herdr.HasCode(err, herdr.CodePaneBusy) || strings.Contains(err.Error(), "models tried") {
		t.Fatalf("got %v, want the busy pane reported once", err)
	}
}

// Once the agent has worked, a failure is not retried: the run may have side
// effects a second attempt would repeat.
func TestNoFallbackAfterTheAgentWorked(t *testing.T) {
	starts := 0
	ops := &fakeOps{
		agentStart:  func(_, _, _ string, _ []string) error { starts++; return nil },
		agentSubmit: func(string, string) error { return nil },
		agentWait:   func(string, time.Duration) error { return errors.New("the agent exited before finishing") },
	}
	a := config.Automation{
		Name: "nightly", Agent: "claude", Model: "opus",
		FallbackModels: []string{"sonnet"},
	}
	// await keeps polling until the deadline, so give it a short one.
	err := agentWorkWith(ops, a).do(Session{PaneID: "p"}, 20*time.Millisecond)
	if err == nil || strings.Contains(err.Error(), "models tried") {
		t.Fatalf("got %v, want the single await failure, no fallback", err)
	}
	if starts != 1 {
		t.Errorf("started %d agents, want 1: a late failure is not retried", starts)
	}
}

// An empty primary model means "the agent's own default": the first attempt
// carries no --model flag, the fallback does.
func TestFallbackAfterAnEmptyPrimaryModel(t *testing.T) {
	var withModelFlag []bool
	ops := &fakeOps{
		agentStart: func(_, _, _ string, args []string) error {
			has := false
			for _, a := range args {
				if a == "--model" {
					has = true
				}
			}
			withModelFlag = append(withModelFlag, has)
			if len(withModelFlag) == 1 {
				return errors.New("default model unavailable")
			}
			return nil
		},
		agentSubmit: func(string, string) error { return nil },
		agentWait:   func(string, time.Duration) error { return nil },
	}
	a := config.Automation{Name: "nightly", Agent: "claude", FallbackModels: []string{"sonnet"}}
	if err := agentWorkWith(ops, a).do(Session{PaneID: "p"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(withModelFlag) != 2 || withModelFlag[0] || !withModelFlag[1] {
		t.Fatalf("--model present %v, want [false true]", withModelFlag)
	}
}

// No fallback configured: the error passes through unchanged, with no
// "all models failed" wrapper.
func TestSingleModelFailureIsNotRewrapped(t *testing.T) {
	ops := &fakeOps{agentStart: func(_, _, _ string, _ []string) error {
		return errors.New("boom")
	}}
	a := config.Automation{Name: "nightly", Agent: "claude", Model: "opus"}
	err := agentWorkWith(ops, a).do(Session{PaneID: "p"}, time.Minute)
	if err == nil || strings.Contains(err.Error(), "models tried") {
		t.Fatalf("got %v, want the plain start error", err)
	}
}
