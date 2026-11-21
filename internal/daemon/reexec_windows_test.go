package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/runner"
)

// Windows has no exec: a scheduler whose binary changed hands over to a copy
// of itself. The copy must end up holding the lock and the old one must be
// gone, or the update never takes effect, or two schedulers double-fire.
func TestReExecHandsTheLockToTheCopy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	t.Cleanup(func() { stopHolder(t) })

	old := exec.Command(os.Args[0], "-test.run=^TestReExecHelperProcess$")
	old.Env = append(os.Environ(), "HERDR_REEXEC_HELPER=1",
		"HERDR_PLUGIN_STATE_DIR="+dir, "HERDR_PLUGIN_CONFIG_DIR="+t.TempDir())
	// Returns once the old scheduler exits: the copy keeps none of its pipes.
	out, err := old.CombinedOutput()
	if err != nil {
		t.Fatalf("old scheduler: %v (%s)", err, out)
	}

	// The copy waits for the lock as a standby scheduler does: up to one tick.
	deadline := time.Now().Add(tickInterval + 15*time.Second)
	for {
		pid, ok := Running()
		if ok && pid != 0 && pid != old.Process.Pid {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no copy took the lock (running %v, pid %d; old was %d; old said %s)", ok, pid, old.Process.Pid, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestReExecHelperProcess is both halves of the hand-over, not a test of its
// own. Run plainly, it is the old scheduler: it takes the lock and re-executes.
// Run with --standby, it is the copy, and does what the copy really does.
func TestReExecHelperProcess(t *testing.T) {
	if os.Getenv("HERDR_REEXEC_HELPER") != "1" {
		t.Skip("helper process for the re-exec test")
	}
	if slices.Contains(os.Args, "--standby") {
		// A failed test may never find this copy to kill it: it ends itself.
		time.AfterFunc(2*time.Minute, func() { os.Exit(0) })
		_ = Run(true)
		return
	}
	if _, err := acquireLock(); err != nil {
		fmt.Println("REFUSED:", err)
		os.Exit(1)
	}
	// "--" keeps the --standby the re-exec adds away from the test flags.
	os.Args = []string{os.Args[0], "-test.run=^TestReExecHelperProcess$", "--"}
	restart(runner.New(nil), os.Args[0], "binary changed")
	fmt.Println("re-exec returned; the old scheduler is still running")
	os.Exit(1)
}

// stopHolder kills whatever scheduler holds the test's lock, so a copy never
// outlives the test.
func stopHolder(t *testing.T) {
	pid, ok := Running()
	if !ok || pid == 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
		_, _ = p.Wait()
	}
	if _, ok := Running(); ok {
		t.Errorf("scheduler pid %d still holds the lock after the test", pid)
	}
}
