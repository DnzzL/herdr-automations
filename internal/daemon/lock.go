package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/DnzzL/herdr-automations/internal/config"
)

// acquireLock keeps a single scheduler alive per machine. Two daemons would
// fire every automation twice, and a stale one is easy to end up with: the
// startup hook runs again on every Herdr server restart.
func acquireLock() (release func(), err error) {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(config.StateDir(), "daemon.pid")

	if raw, err := os.ReadFile(path); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && alive(pid) && pid != os.Getpid() {
			return nil, fmt.Errorf("another daemon is already running (pid %d)", pid)
		}
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, err
	}
	return func() { os.Remove(path) }, nil
}

func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
