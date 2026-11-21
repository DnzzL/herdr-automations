package daemon

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/runner"
)

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
	// Refused for the right reason, and pointed at the daemon that holds it.
	want := fmt.Sprintf("REFUSED: another daemon is already running (pid %d)", os.Getpid())
	if !strings.Contains(out, want) {
		t.Errorf("second daemon was not refused by the first one's lock; two daemons would double-fire every automation (want %q, helper said: %s)", want, out)
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

// A daemon starting just as the running one shuts down can open the pidfile
// before release and lock it after. If release had removed the file, that
// daemon would hold a lock on a file nobody else can find, and the next one to
// start would create a fresh file, lock that too, and run alongside it.
func TestReleaseKeepsTheFileSoOnlyOneDaemonLocksIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)

	release, err := acquireLock()
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	next, err := os.OpenFile(filepath.Join(dir, "daemon.pid"), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open pidfile: %v", err)
	}
	defer next.Close()

	release()
	if err := lockFile(next, true); err != nil {
		t.Fatalf("lock the released pidfile: %v", err)
	}

	out, err := runLockHelper(dir)
	if err != nil {
		t.Fatalf("run helper: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "REFUSED: another daemon is already running") {
		t.Errorf("a third daemon took a lock while another daemon held one; two would double-fire every automation (helper said: %s)", out)
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
		fmt.Println("REFUSED:", err)
		return
	}
	defer release()
	fmt.Println("ACQUIRED")
	if os.Getenv("HERDR_LOCK_HOLD") == "1" {
		// Hold until killed: the parent checks what the OS does with a lock
		// whose holder never released it.
		time.Sleep(time.Hour)
	}
}

// The panel's banner reads Running() from another process while the scheduler
// holds the lock, and a scheduler that dies without releasing must not strand
// the next one. Windows locks are mandatory, so this also proves the PID stays
// readable while the file is locked.
func TestLockFollowsTheHolderProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)

	holder := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
	holder.Env = append(os.Environ(), "HERDR_LOCK_HELPER=1", "HERDR_LOCK_HOLD=1", "HERDR_PLUGIN_STATE_DIR="+dir)
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	defer func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.Contains(line, "ACQUIRED") {
		t.Fatalf("holder did not take the lock: %q %v", line, err)
	}

	if pid, ok := Running(); !ok || pid != holder.Process.Pid {
		t.Errorf("Running() = %d %v while pid %d holds the lock; the panel would show the wrong banner", pid, ok, holder.Process.Pid)
	}
	if second, err := acquireLock(); !errors.Is(err, ErrAlreadyRunning) {
		if err == nil {
			second()
		}
		t.Fatalf("second acquire err = %v, want ErrAlreadyRunning", err)
	}

	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()

	if _, ok := Running(); ok {
		t.Error("Running() still true after the holder died")
	}
	release, err := acquireLock()
	if err != nil {
		t.Fatalf("lock stranded by a dead holder: %v", err)
	}
	release()
}

// A re-exec that fails leaves the old daemon running, so it must still hold
// the lock; otherwise the next startup hook would launch a second scheduler.
func TestFailedReExecKeepsTheLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)

	release, err := acquireLock()
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	defer release()

	defer func(orig func(string, []string, []string) error) { execve = orig }(execve)
	execve = func(string, []string, []string) error { return syscall.ENOEXEC }
	restart(runner.New(nil), "", "binary changed")

	out, err := runLockHelper(dir)
	if err != nil {
		t.Fatalf("run helper: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "REFUSED: another daemon is already running") {
		t.Errorf("a failed re-exec dropped the lock; a second daemon could start beside the old one (helper said: %s)", out)
	}
}

// `herdr plugin install` moves the old checkout away and deletes it, so the
// running image's own path stops existing; the new build lands at the path the
// daemon started from, and that is the one to watch and re-exec.
func TestReinstallReExecsTheBinaryAtTheStartupPath(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "checkout", "bin", "herdr-automations")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := binaryStamp(exe)

	if err := os.Rename(filepath.Join(root, "checkout"), filepath.Join(root, "previous-checkout")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("new build, bigger"), 0o755); err != nil {
		t.Fatal(err)
	}

	after := binaryStamp(exe)
	if after == "" || after == before {
		t.Fatalf("reinstall went unnoticed: stamp %q -> %q", before, after)
	}

	defer func(orig func(string, []string, []string) error) { execve = orig }(execve)
	var execd string
	execve = func(path string, _ []string, _ []string) error { execd = path; return nil }
	restart(runner.New(nil), exe, "binary changed")
	if execd != exe {
		t.Errorf("re-exec targeted %q, want the startup path %q", execd, exe)
	}
}

func TestASecondDaemonIsToldItIsNotNeeded(t *testing.T) {
	// Every session's startup hook starts a daemon and all but one lose. That
	// is the design, so the losers must not read as failures in plugin log.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	release, err := acquireLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := acquireLock(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
}

// The panel's "Scheduler stopped" banner reads this.
func TestRunningFollowsTheLock(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	if _, ok := Running(); ok {
		t.Fatal("running with no daemon")
	}
	release, err := acquireLock()
	if err != nil {
		t.Fatal(err)
	}
	if pid, ok := Running(); !ok || pid != os.Getpid() {
		t.Fatalf("got %d %v while holding the lock", pid, ok)
	}
	release()
	if _, ok := Running(); ok {
		t.Fatal("still running after release")
	}
}
