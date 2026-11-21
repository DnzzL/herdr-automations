package daemon

import (
	"os"
	"slices"
)

// execve stands in for exec, which Windows lacks: it starts a detached copy
// with --standby and exits, which releases the lock. The copy waits for the
// lock like any standby scheduler, so the two never run side by side. A copy
// that fails to start leaves this process running, lock held. Swappable so a
// test can make the re-exec fail.
var execve = func(exe string, argv, env []string) error {
	args := slices.Clone(argv[1:])
	if !slices.Contains(args, "--standby") {
		args = append(args, "--standby")
	}
	if err := startDetached(exe, args, env); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
