package runner

import "testing"

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
