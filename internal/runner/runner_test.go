package runner

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
)

// paneBusy is the error herdr returns for a pane whose shell has not spawned.
func paneBusy() error {
	return &herdr.APIError{
		Command: "agent start",
		Code:    herdr.CodePaneBusy,
		Message: "agent target pane w1T:p1 is not an available shell",
	}
}

func TestAgentArgsIncludePiProviderAndModel(t *testing.T) {
	a := config.Automation{
		Agent: "pi", Provider: "xai", Model: "grok-4.6",
		AgentArgs: []string{"--thinking", "high"},
	}
	want := []string{"--provider", "xai", "--model", "grok-4.6", "--thinking", "high"}
	if got := agentArgs(a); !reflect.DeepEqual(got, want) {
		t.Fatalf("agentArgs() = %q, want %q", got, want)
	}
}

func TestStartAgentRetriesUntilThePaneHasAShell(t *testing.T) {
	paneReadyPoll = time.Millisecond
	calls := 0
	err := startAgent(func() error {
		calls++
		if calls < 3 {
			return paneBusy()
		}
		return nil
	}, time.Minute)

	if err != nil {
		t.Fatalf("want the third attempt to stick, got %v", err)
	}
	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
}

func TestStartAgentGivesUpOnAPaneThatNeverComesUp(t *testing.T) {
	paneReadyPoll = time.Millisecond
	calls := 0
	err := startAgent(func() error { calls++; return paneBusy() }, 20*time.Millisecond)

	if !herdr.HasCode(err, herdr.CodePaneBusy) {
		t.Fatalf("want the busy error reported, got %v", err)
	}
	if calls < 2 {
		t.Errorf("called %d times, want more than one attempt", calls)
	}
}

func TestStartAgentDoesNotRetryARealFailure(t *testing.T) {
	paneReadyPoll = time.Millisecond
	calls := 0
	want := errors.New("agent start: unknown kind \"clyde\"")
	err := startAgent(func() error { calls++; return want }, time.Minute)

	if !errors.Is(err, want) {
		t.Fatalf("got %v, want it passed through", err)
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1: a bad agent kind will not fix itself", calls)
	}
}

func TestStartAgentDoesNotWaitWhenThePaneIsReady(t *testing.T) {
	calls := 0
	if err := startAgent(func() error { calls++; return nil }, 0); err != nil {
		t.Fatalf("got %v", err)
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1", calls)
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

func TestExitCodeIgnoresTheEchoedCommand(t *testing.T) {
	marker := "HWF-123"
	// The shell echoes the command before running it, so the literal printf
	// format is on screen alongside the real status.
	screen := "$ sh -c 'hwf run x; printf \"\\nHWF-123:%d\\n\" $?'\n" +
		"running workflow x…\n" +
		"HWF-123:0\n"
	code, done := exitCode(screen, marker)
	if !done || code != 0 {
		t.Fatalf("got (%d, %v), want (0, true)", code, done)
	}
}

func TestExitCodeWaitsWhileNothingIsPrinted(t *testing.T) {
	if _, done := exitCode("still working…\n", "HWF-123"); done {
		t.Fatal("a workflow still running must not be read as finished")
	}
}

func TestExitCodeTakesTheLastRun(t *testing.T) {
	code, done := exitCode("HWF-9:0\nsecond attempt\nHWF-9:2\n", "HWF-9")
	if !done || code != 2 {
		t.Fatalf("got (%d, %v), want (2, true)", code, done)
	}
}

func TestShellQuoteSurvivesAnApostrophe(t *testing.T) {
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Fatalf("got %s", got)
	}
}

// agentGone is what herdr returns once there is no agent in the pane.
func agentGone() error {
	return &herdr.APIError{
		Command: "agent wait",
		Code:    herdr.CodeAgentGone,
		Message: "agent is no longer running in the target pane",
	}
}

// sliceExpired stands in for a wait that timed out with the agent still busy.
func sliceExpired() error {
	return &herdr.APIError{Command: "agent wait", Code: "timeout"}
}

func TestWaitForAgentReturnsWhenTheAgentSettles(t *testing.T) {
	calls := 0
	err := waitForAgent(func(time.Duration) error {
		calls++
		if calls < 3 {
			return sliceExpired()
		}
		return nil
	}, time.Hour)

	if err != nil {
		t.Fatalf("got %v, want the settled agent reported as success", err)
	}
}

func TestWaitForAgentGivesUpAsSoonAsTheWorkspaceIsClosed(t *testing.T) {
	// The failure this exists for: a run cancelled seconds in used to hold its
	// inFlight slot for the whole timeout. One slice is all it should cost now.
	calls := 0
	err := waitForAgent(func(time.Duration) error { calls++; return agentGone() }, time.Hour)

	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("got %v, want ErrCancelled", err)
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1: it should not wait out the timeout", calls)
	}
}

func TestWaitForAgentNeverWaitsPastTheDeadline(t *testing.T) {
	// The fake sleeps the slice it is handed, as herdr does, so wall-clock time
	// is what bounds the loop.
	agentWaitSlice = 10 * time.Millisecond
	const timeout = 50 * time.Millisecond

	var asked []time.Duration
	started := time.Now()
	err := waitForAgent(func(d time.Duration) error {
		asked = append(asked, d)
		time.Sleep(d)
		return sliceExpired()
	}, timeout)

	if err == nil {
		t.Fatal("want the timeout reported")
	}
	for _, d := range asked {
		if d <= 0 {
			t.Fatalf("asked herdr to wait %s", d)
		}
		if d > agentWaitSlice {
			t.Fatalf("asked herdr to wait %s, longer than one slice", d)
		}
	}
	// Generous: this asserts the deadline is honoured, not the scheduler's
	// precision.
	if elapsed := time.Since(started); elapsed > 4*timeout {
		t.Errorf("took %s for a %s timeout", elapsed, timeout)
	}
}

func TestWaitForAgentReportsWhatHerdrLastSaid(t *testing.T) {
	// Better to surface herdr's own last word than a generic "still working".
	agentWaitSlice = time.Millisecond
	err := waitForAgent(func(time.Duration) error { return sliceExpired() }, 2*time.Millisecond)

	if !herdr.HasCode(err, "timeout") {
		t.Fatalf("got %v, want herdr's last error kept", err)
	}
}

func TestStatusForSeparatesACancellationFromAFailure(t *testing.T) {
	cancelled := fmt.Errorf("waiting for the agent: %w", ErrCancelled)
	if got := statusFor(cancelled); got != history.StatusCancelled {
		t.Errorf("a closed workspace recorded as %q, want cancelled", got)
	}
	if got := statusFor(errors.New("start claude agent: boom")); got != history.StatusFailed {
		t.Errorf("a real failure recorded as %q, want failed", got)
	}
}
