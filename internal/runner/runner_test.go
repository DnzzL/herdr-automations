package runner

import (
	"errors"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/herdr"
)

// paneBusy is the error herdr returns for a pane whose shell has not spawned.
func paneBusy() error {
	return &herdr.APIError{
		Command: "agent start",
		Code:    herdr.CodePaneBusy,
		Message: "agent target pane w1T:p1 is not an available shell",
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
