package hostpath

import (
	"os"
	"path/filepath"
	"testing"
)

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
