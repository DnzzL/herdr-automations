//go:build unix

package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// A daemon killed with the Herdr server leaves its pidfile behind, and the
// startup hook runs again on the next restart. By then the recorded PID may
// have been reused by an unrelated process, which must not be mistaken for a
// running daemon.
func TestAcquireLockIgnoresStalePIDOwnedByAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)

	// A live process of our own that is plainly not a daemon. Signalling it
	// succeeds, so any liveness check based on the PID alone reports it as one.
	victim := exec.Command("sleep", "60")
	if err := victim.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	defer func() {
		_ = victim.Process.Kill()
		_, _ = victim.Process.Wait()
	}()

	pidfile := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(victim.Process.Pid)), 0o644); err != nil {
		t.Fatalf("seed stale pidfile: %v", err)
	}

	release, err := acquireLock()
	if err != nil {
		t.Fatalf("acquireLock refused to start over a stale pidfile: %v", err)
	}
	defer release()

	got, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatalf("read pidfile: %v", err)
	}
	if want := strconv.Itoa(os.Getpid()); string(got) != want {
		t.Errorf("pidfile = %q, want %q", got, want)
	}
}
