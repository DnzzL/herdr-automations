package shim

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHost puts a herdr on PATH that reports root as the plugin root (empty:
// not installed), and a plugin binary under root that echoes its arguments.
func fakeHost(t *testing.T, root string) {
	t.Helper()
	bin := t.TempDir()
	list := `{"id":"cli:plugin","result":{"plugins":[],"type":"plugin_list"}}`
	if root != "" {
		list = `{"id":"cli:plugin","result":{"plugins":[{"plugin_id":"dnzzl.automations","plugin_root":"` + root + `","version":"0.9.0"}],"type":"plugin_list"}}`
		plugin := "#!/bin/sh\nfor a in \"$@\"; do echo \"[$a]\"; done\n"
		if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "bin", "herdr-automations"), []byte(plugin), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	herdr := "#!/bin/sh\necho '" + list + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(herdr), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HERDR_BIN_PATH", "")
}

func TestShimRunsTheInstalledPluginBinary(t *testing.T) {
	fakeHost(t, t.TempDir())
	dir := t.TempDir()

	if err := Install(dir); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(dir, "herdr-automations"), "run", "two words").CombinedOutput()
	if err != nil {
		t.Fatalf("shim: %v\n%s", err, out)
	}
	if got, want := string(out), "[run]\n[two words]\n"; got != want {
		t.Fatalf("shim output = %q, want %q", got, want)
	}
}

func TestShimSaysSoWhenThePluginIsNotInstalled(t *testing.T) {
	// After `herdr plugin uninstall` the old checkout is still on disk; the
	// shim must not go looking for it.
	fakeHost(t, "")
	dir := t.TempDir()

	if err := Install(dir); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(dir, "herdr-automations")).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 127 {
		t.Fatalf("shim err = %v, want exit 127", err)
	}
	if !strings.Contains(string(out), "not installed") {
		t.Fatalf("shim output = %q, want it to say the plugin is not installed", out)
	}
}

func TestShimSaysSoWhenHerdrIsNotOnPath(t *testing.T) {
	// "plugin not installed" would send the user reinstalling a plugin that is
	// fine.
	dir := t.TempDir()
	if err := Install(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HERDR_BIN_PATH", "")

	out, err := exec.Command(filepath.Join(dir, "herdr-automations")).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 127 {
		t.Fatalf("shim err = %v, want exit 127", err)
	}
	if !strings.Contains(string(out), "herdr not found") {
		t.Fatalf("shim output = %q, want it to say herdr is missing", out)
	}
}

func TestInstallTwiceIsFine(t *testing.T) {
	fakeHost(t, t.TempDir())
	dir := t.TempDir()

	if err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir); err != nil {
		t.Fatalf("second Install: %v", err)
	}
}

func TestInstallRefusesToOverwriteAFileItDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	theirs := filepath.Join(dir, "herdr-automations")
	if err := os.WriteFile(theirs, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Install(dir); !errors.Is(err, ErrNotOurs) {
		t.Fatalf("Install err = %v, want ErrNotOurs", err)
	}
	if got, _ := os.ReadFile(theirs); string(got) != "#!/bin/sh\necho mine\n" {
		t.Fatalf("file was changed to %q", got)
	}
}

// The login service runs whatever Find returns, so that a plugin update, which
// moves the checkout, does not leave launchd pointing at a deleted binary.
func TestFindReturnsOurShimOnPathAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if _, ok := Find(); ok {
		t.Fatal("found a shim on an empty PATH")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho someone else\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := Find(); ok {
		t.Fatal("took someone else's herdr-automations for our shim")
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if got, ok := Find(); !ok || got != filepath.Join(dir, name) {
		t.Fatalf("Find() = %q, %v", got, ok)
	}
}
