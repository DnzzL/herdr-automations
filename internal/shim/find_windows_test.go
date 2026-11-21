//go:build windows

package shim

import (
	"os"
	"path/filepath"
	"testing"
)

// LookPath resolves herdr-automations through PATHEXT, so the exe that the
// installer drops on PATH, marker included, is what it finds. That is not our shim: the service
// must register the exe you ran, not point at it by PATH.
func TestFindIgnoresAnExeOnPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name+".exe"), append([]byte("MZ\x90\x00"), marker...), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got, ok := Find(); ok {
		t.Fatalf("Find = %q, want none", got)
	}
}
