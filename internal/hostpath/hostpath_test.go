package hostpath

import (
	"os"
	"path/filepath"
	"runtime"
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
	onPlatform(t, "linux") // Windows has no exec bit to miss

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

// onPlatform makes the lookups behave as they do on goos, so the Windows rules
// are proven on every CI runner.
func onPlatform(t *testing.T, platform string) {
	t.Helper()
	was := goos
	goos = platform
	t.Cleanup(func() { goos = was })
}

// bareHost empties everything HCBin looks at: no HC_BIN, nothing on PATH, a
// fresh home and app-data folder. It returns the home.
func bareHost(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HC_BIN", "")
	return home
}

// writeExe writes an executable-looking file at path, creating its folder.
func writeExe(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// launchd hands a service PATH=/usr/bin:/bin. The app installs hc in
// ~/.local/bin, so a bare "hc" would not resolve from the daemon.
func TestHCBinFindsTheAppInstallUnderABarePath(t *testing.T) {
	home := bareHost(t)
	want := writeExe(t, filepath.Join(home, ".local", "bin", "hc"))

	if got := HCBin(); got != want {
		t.Fatalf("HCBin() = %q, want %q", got, want)
	}
}

// Go reports no exec bits on Windows, so an HC_BIN pointing at a real file
// was rejected and the lookup fell through to a bare "hc" that is not there.
func TestOnWindowsHCBinTakesAnExistingFile(t *testing.T) {
	onPlatform(t, "windows")
	bareHost(t)
	want := filepath.Join(t.TempDir(), "hc.exe")
	if err := os.WriteFile(want, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_BIN", want)

	if got := HCBin(); got != want {
		t.Fatalf("HCBin() = %q, want %q", got, want)
	}
}

// The app's installer puts herdr-core on PATH and links hc to it only when no
// hc exists, so herdr-core may be the only name there is.
func TestOnWindowsHCBinFindsHerdrCoreOnPath(t *testing.T) {
	onPlatform(t, "windows")
	bareHost(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	name := "herdr-core"
	if runtime.GOOS == "windows" {
		name += ".exe" // LookPath goes by extension there
	}
	want := writeExe(t, filepath.Join(dir, name))

	if got := HCBin(); got != want {
		t.Fatalf("HCBin() = %q, want %q", got, want)
	}
}

// The login entry may start the scheduler before the installer's folder is on
// its PATH; the folder itself is known.
func TestOnWindowsHCBinFindsHerdrCoreWhereTheAppInstallsIt(t *testing.T) {
	onPlatform(t, "windows")
	bareHost(t)
	want := writeExe(t, filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "herdr-core", "herdr-core.exe"))

	if got := HCBin(); got != want {
		t.Fatalf("HCBin() = %q, want %q", got, want)
	}
}
