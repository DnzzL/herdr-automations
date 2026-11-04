//go:build unix

package hostpath

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeHerdr writes an executable that answers `plugin config-dir` with answer.
func fakeHerdr(t *testing.T, dir, answer string) string {
	t.Helper()
	path := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\necho " + answer + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBinPrefersWhatHerdrAdvertises(t *testing.T) {
	fake := fakeHerdr(t, t.TempDir(), "/x")
	t.Setenv("HERDR_BIN_PATH", fake)

	if got := Bin(); got != fake {
		t.Fatalf("Bin() = %q, want %q", got, fake)
	}
}

func TestConfigDirAsksTheCLI(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HERDR_BIN_PATH", fakeHerdr(t, t.TempDir(), "/asked/herdr"))

	if got := ConfigDir(); got != "/asked/herdr" {
		t.Fatalf("ConfigDir() = %q, want the CLI's answer", got)
	}
}

func TestConfigDirStillAsksPathWhenTheAdvertisedBinaryIsGone(t *testing.T) {
	// The bug this package exists for. config.herdrBin trusted a stale
	// HERDR_BIN_PATH, so `plugin config-dir` failed to exec and the directory
	// fell all the way through to the hardcoded layout guess — without ever
	// trying the herdr that is right there on PATH. A wrong config directory
	// reads as an empty automations.yaml: every automation silently gone.
	onPath := t.TempDir()
	fakeHerdr(t, onPath, "/found/on/path")
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "uninstalled", "herdr"))

	if got := ConfigDir(); got != "/found/on/path" {
		t.Fatalf("ConfigDir() = %q, want the herdr on PATH to be asked", got)
	}
}

// What `herdr plugin list --plugin <id> --json` answers, trimmed. The login
// service compares the binary it runs with the one installed now.
func TestInstalledBinaryReadsThePluginRoot(t *testing.T) {
	raw := []byte(`{"id":"cli:plugin","result":{"plugins":[
		{"plugin_id":"someone.else","plugin_root":"/x"},
		{"plugin_id":"dnzzl.automations","plugin_root":"/p/77472-0/checkout","version":"0.11.2"}]}}`)
	if got, want := installedBinary(raw), "/p/77472-0/checkout/bin/herdr-automations"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, bad := range []string{``, `{}`, `not json`, `{"result":{"plugins":[{"plugin_id":"someone.else","plugin_root":"/x"}]}}`} {
		if got := installedBinary([]byte(bad)); got != "" {
			t.Errorf("%q: got %q, want nothing", bad, got)
		}
	}
}
