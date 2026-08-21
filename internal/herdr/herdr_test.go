package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewAPIErrorPrefersHerdrsOwnCode(t *testing.T) {
	body := []byte(`{"error":{"code":"agent_pane_busy","message":"pane w1T:p1 is not an available shell"},"id":"cli:agent:start"}`)
	err := newAPIError([]string{"agent", "start", "triage"}, body, "", errors.New("exit status 1"))

	if !HasCode(err, CodePaneBusy) {
		t.Fatalf("code not recovered from %v", err)
	}
	want := "agent start: agent_pane_busy: pane w1T:p1 is not an available shell"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

func TestNewAPIErrorFallsBackToTheExecError(t *testing.T) {
	// The failure that made a run undiagnosable: herdr exits non-zero and
	// prints nothing, so the only thing left to report is why the process died.
	err := newAPIError([]string{"worktree", "create"}, nil, "", errors.New("exit status 1"))

	want := "worktree create: exit status 1"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestNewAPIErrorKeepsStderrOverTheExecError(t *testing.T) {
	// "exit status 1" says less than whatever herdr wrote, so it loses.
	err := newAPIError([]string{"worktree", "create"}, nil,
		"  fatal: not a git repository\n", errors.New("exit status 128"))

	want := "worktree create: fatal: not a git repository"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestNewAPIErrorSurvivesANilExecError(t *testing.T) {
	if err := newAPIError([]string{"pane", "read"}, nil, "", nil); err == nil {
		t.Fatal("want an error even with nothing to say")
	}
}

func TestBinPrefersWhatHerdrAdvertises(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", fake)

	if got := bin(); got != fake {
		t.Fatalf("bin() = %q, want %q", got, fake)
	}
}

func TestBinIgnoresAPathThatNoLongerExists(t *testing.T) {
	// A herdr installed by nix, then replaced by one from homebrew: the running
	// process keeps handing out a path whose file is gone. Falling back to PATH
	// is the only thing the plugin can do about it.
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "uninstalled", "herdr"))

	if got := bin(); got != "herdr" {
		t.Fatalf("bin() = %q, want the PATH fallback", got)
	}
}

func TestBinIgnoresAPathThatIsNotExecutable(t *testing.T) {
	notExec := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(notExec, []byte("not a binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", notExec)

	if got := bin(); got != "herdr" {
		t.Fatalf("bin() = %q, want the PATH fallback", got)
	}
}

func TestBinIgnoresADirectory(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", t.TempDir())

	if got := bin(); got != "herdr" {
		t.Fatalf("bin() = %q, want the PATH fallback", got)
	}
}

func TestBinFallsBackWhenNothingIsAdvertised(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "")

	if got := bin(); got != "herdr" {
		t.Fatalf("bin() = %q, want %q", got, "herdr")
	}
}
