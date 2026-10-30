package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestNewAPIErrorReadsTheCodeFromStderr(t *testing.T) {
	// herdr 0.8.0 writes the error envelope to stderr, not stdout.
	stderr := `{"error":{"code":"agent_pane_busy","message":"agent target pane w3V:p1 is not an available shell"},"id":"cli:agent:start"}` + "\n"
	err := newAPIError([]string{"agent", "start", "triage"}, nil, stderr, errors.New("exit status 1"))

	if !HasCode(err, CodePaneBusy) {
		t.Fatalf("code not recovered from %v", err)
	}
	want := "agent start: agent_pane_busy: agent target pane w3V:p1 is not an available shell"
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

func TestNotificationArgsOmitsAnEmptyBody(t *testing.T) {
	got := notificationArgs("triage failed", "", SoundRequest)
	for _, a := range got {
		if a == "--body" {
			t.Fatalf("args = %v, want no --body flag for an empty body", got)
		}
	}
}

func TestNotificationArgsPassesTheTitleAsAPositionalArgument(t *testing.T) {
	got := notificationArgs("triage failed", "boom", SoundRequest)
	want := []string{"notification", "show", "triage failed",
		"--body", "boom", "--position", notificationPosition, "--sound", "request"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestTabCreate(t *testing.T) {
	for _, tc := range []struct {
		name, response, pane string
		wantError            bool
	}{
		{"created", `{"result":{"type":"tab_created","root_pane":{"pane_id":"w7:p2"}}}`, "w7:p2", false},
		{"missing pane", `{"result":{"type":"tab_created"}}`, "", true},
		{"invalid response", `not json`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "herdr")
			argsFile := filepath.Join(dir, "args")
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HERDR_TEST_ARGS\"\nprintf '%s\\n' \"$HERDR_TEST_RESPONSE\"\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_BIN_PATH", bin)
			t.Setenv("HERDR_TEST_ARGS", argsFile)
			t.Setenv("HERDR_TEST_RESPONSE", tc.response)
			pane, err := (Client{}).TabCreate("w7", "/a repo", "auto: daily summary")
			if (err != nil) != tc.wantError || pane != tc.pane {
				t.Fatalf("pane=%q err=%v", pane, err)
			}
			raw, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			want := []string{"tab", "create", "--workspace", "w7", "--cwd", "/a repo", "--label", "auto: daily summary", "--no-focus"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("args=%q, want %q", got, want)
			}
		})
	}
}

// fakeHerdr installs a herdr that records its argv and prints response.
func fakeHerdr(t *testing.T, response string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HERDR_TEST_ARGS\"\nprintf '%s\\n' \"$HERDR_TEST_RESPONSE\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	t.Setenv("HERDR_TEST_ARGS", argsFile)
	t.Setenv("HERDR_TEST_RESPONSE", response)
	return argsFile
}

func readArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func TestAClientBoundToASessionTargetsItOnEveryCall(t *testing.T) {
	args := fakeHerdr(t, `{"result":{"root_pane":{"pane_id":"w7:p2"}}}`)
	if _, err := (Client{Session: "automations"}).TabCreate("w7", "/r", "l"); err != nil {
		t.Fatal(err)
	}
	got := readArgs(t, args)
	if len(got) < 3 || got[0] != "--session" || got[1] != "automations" || got[2] != "tab" {
		t.Fatalf("args = %q, want --session automations ahead of the subcommand", got)
	}
}

const twoSessions = `{"sessions":[
 {"default":true,"name":"default","running":true,"socket_path":"/h/.config/herdr/herdr.sock"},
 {"default":false,"name":"automations","running":true,"socket_path":"/h/.config/herdr/sessions/automations/herdr.sock"},
 {"default":false,"name":"old","running":false,"socket_path":"/h/.config/herdr/sessions/old/herdr.sock"}]}`

func TestRunningSessionsLeavesOutTheStoppedOnes(t *testing.T) {
	args := fakeHerdr(t, twoSessions)
	got, err := (Client{Session: "ignored"}).RunningSessions()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"default", "automations"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions = %q, want %q", got, want)
	}
	// Listing sessions is about the machine, not about one of them.
	if a := readArgs(t, args); a[0] != "session" {
		t.Fatalf("args = %q, want no --session on session list", a)
	}
}

func TestCurrentSessionIsTheOneWhoseSocketThisProcessUses(t *testing.T) {
	fakeHerdr(t, twoSessions)
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_SOCKET_PATH", "/h/.config/herdr/sessions/automations/herdr.sock")
	if got := (Client{}).CurrentSession(); got != "automations" {
		t.Fatalf("current = %q, want automations", got)
	}
}

func TestCurrentSessionOutsideHerdrIsTheDefaultOne(t *testing.T) {
	// herdr's CLI talks to the default session when nothing says otherwise.
	fakeHerdr(t, twoSessions)
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_SOCKET_PATH", "")
	if got := (Client{}).CurrentSession(); got != "default" {
		t.Fatalf("current = %q, want default", got)
	}
}

func TestCurrentSessionOfABoundClientIsItsOwn(t *testing.T) {
	if got := (Client{Session: "automations"}).CurrentSession(); got != "automations" {
		t.Fatalf("current = %q, want automations", got)
	}
}

func TestAnErrorFromABoundClientIsLabelledByTheCommandNotTheSession(t *testing.T) {
	err := newAPIError([]string{"--session", "b", "agent", "start", "x"}, nil,
		`{"error":{"code":"agent_not_ready","message":"m"}}`, errors.New("exit status 1"))
	if want := "agent start: agent_not_ready: m"; err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}
