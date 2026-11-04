package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The test binary doubles as the CLI: run with HERDR_AUTOMATIONS_TEST_CLI set,
// it is herdr-automations, so the tests drive the same entry point as the panel.
func TestMain(m *testing.M) {
	if os.Getenv("HERDR_AUTOMATIONS_TEST_CLI") != "" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type cliResult struct {
	stdout, stderr string
	code           int
	file           string // automations.yaml after the call
}

// cli runs herdr-automations with args over a fresh config holding yaml.
func cli(t *testing.T, yaml, stdin string, args ...string) cliResult {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "automations.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "HERDR_AUTOMATIONS_TEST_CLI=1",
		"HERDR_PLUGIN_CONFIG_DIR="+dir, "HERDR_PLUGIN_STATE_DIR="+t.TempDir())
	cmd.Stdin = bytes.NewBufferString(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	res := cliResult{}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		res.code = exit.ExitCode()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res.stdout, res.stderr, res.file = out.String(), errOut.String(), string(raw)
	return res
}

const putConfig = `# mine
automations:
  - name: triage
    cron: "0 9 * * 1-5"
    repo: /x
    prompt: p
`

// The panel has no stdin to give exec, so it passes the entry with --entry.
// Whatever the entry, the answer must be the one stdin gives.
func TestPutEntryAnswersLikeStdin(t *testing.T) {
	cases := []struct {
		name, entry string
		flags       []string
	}{
		{"new entry", `{"name":"report","cron":"0 8 * * 1","repo":"/x","prompt":"weekly"}`, nil},
		{"edit", `{"name":"triage","cron":"0 8 * * 1-5","prompt":"new"}`, []string{"--original", "triage"}},
		{"dry run", `{"name":"triage","cron":"0 8 * * 1-5","prompt":"new"}`, []string{"--original", "triage", "--dry-run"}},
		{"refused", `{"name":"bad","cron":"0 99 * * *","repo":"/x","prompt":"p"}`, nil},
		{"not JSON", `{"name":`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			viaStdin := cli(t, putConfig, c.entry, append([]string{"put"}, c.flags...)...)
			viaFlag := cli(t, putConfig, "", append([]string{"put", "--entry", c.entry}, c.flags...)...)
			if viaFlag != viaStdin {
				t.Fatalf("--entry gave %+v\nstdin gave %+v", viaFlag, viaStdin)
			}
		})
	}
}
