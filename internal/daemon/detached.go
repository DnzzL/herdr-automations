package daemon

import (
	"os"
	"path/filepath"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/detach"
)

// StartStandby starts `exe daemon --standby` detached and returns at once. On
// Windows the login entry goes through it, so the scheduler outlives the
// console window the login opened, and that window closes.
func StartStandby(exe string) error {
	return startDetached(exe, []string{"daemon", "--standby"}, os.Environ())
}

// startDetached starts exe detached, its output appended to daemon.log: with
// no console and no service manager, nothing else keeps the log.
func startDetached(exe string, args, env []string) error {
	return detach.Start(filepath.Join(config.StateDir(), "daemon.log"), exe, args, env)
}
