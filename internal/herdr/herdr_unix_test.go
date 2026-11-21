//go:build unix

package herdr

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
