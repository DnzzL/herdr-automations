package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// The lock must still do its job: a real second daemon is a separate process,
// so the check has to hold across processes rather than within one.
func TestAcquireLockRejectsSecondDaemonProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)

	release, err := acquireLock()
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	defer release()

	out, err := runLockHelper(dir)
	if err != nil {
		t.Fatalf("run helper: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "REFUSED") {
		t.Errorf("second daemon was not refused; two daemons would double-fire every automation (helper said: %s)", out)
	}
}

func TestAcquireLockAfterReleaseSucceeds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)

	release, err := acquireLock()
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	release()

	out, err := runLockHelper(dir)
	if err != nil {
		t.Fatalf("run helper: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "ACQUIRED") {
		t.Errorf("lock was not reusable after release (helper said: %s)", out)
	}
}

// runLockHelper re-executes this test binary as a child process that tries to
// take the lock once and reports what happened.
func runLockHelper(stateDir string) (string, error) {
	cmd := exec.Command(os.Args[0], "-test.run=TestLockHelperProcess")
	cmd.Env = append(os.Environ(), "HERDR_LOCK_HELPER=1", "HERDR_PLUGIN_STATE_DIR="+stateDir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestLockHelperProcess is the child half of runLockHelper, not a test of its
// own; it does nothing unless the parent asks for it.
func TestLockHelperProcess(t *testing.T) {
	if os.Getenv("HERDR_LOCK_HELPER") != "1" {
		t.Skip("helper process for the lock tests")
	}
	release, err := acquireLock()
	if err != nil {
		fmt.Println("REFUSED")
		return
	}
	defer release()
	fmt.Println("ACQUIRED")
}
