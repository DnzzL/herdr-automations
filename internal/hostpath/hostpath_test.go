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

func TestBinIgnoresAPathThatNoLongerExists(t *testing.T) {
	// A herdr installed by nix, then replaced by one from homebrew: the running
	// process keeps handing out a path whose file is gone. Falling back to PATH
	// is the only thing the plugin can do about it.
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "uninstalled", "herdr"))

	if got := Bin(); got != "herdr" {
		t.Fatalf("Bin() = %q, want the PATH fallback", got)
	}
}

func TestBinIgnoresAPathThatIsNotExecutable(t *testing.T) {
	notExec := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(notExec, []byte("not a binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", notExec)

	if got := Bin(); got != "herdr" {
		t.Fatalf("Bin() = %q, want the PATH fallback", got)
	}
}

func TestBinIgnoresADirectory(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", t.TempDir())

	if got := Bin(); got != "herdr" {
		t.Fatalf("Bin() = %q, want the PATH fallback", got)
	}
}

func TestBinFallsBackWhenNothingIsAdvertised(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "")

	if got := Bin(); got != "herdr" {
		t.Fatalf("Bin() = %q, want %q", got, "herdr")
	}
}

func TestConfigDirPrefersTheEnvironment(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "/from/herdr")
	if got := ConfigDir(); got != "/from/herdr" {
		t.Fatalf("ConfigDir() = %q", got)
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

func TestStateDirPrefersTheEnvironment(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "/from/herdr")
	if got := StateDir(); got != "/from/herdr" {
		t.Fatalf("StateDir() = %q", got)
	}
}

func TestRootPrefersTheEnvironment(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_ROOT", "/checkout")
	got, err := Root()
	if err != nil || got != "/checkout" {
		t.Fatalf("Root() = %q, %v", got, err)
	}
}

// launchd hands a service PATH=/usr/bin:/bin. The app installs hc in
// ~/.local/bin, so a bare "hc" would not resolve from the daemon.
func TestHCBinFindsTheAppInstallUnderABarePath(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(bin, "hc")
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HC_BIN", "")

	if got := HCBin(); got != want {
		t.Fatalf("HCBin() = %q, want %q", got, want)
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
