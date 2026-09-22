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
// fire every automation twice.
//
// The lock is an advisory flock on the pidfile, not the PID it contains. The
// kernel drops an flock when the holder exits, however it exits, so a daemon
// killed with the Herdr server cannot leave a lock behind. Trusting the PID
// instead would strand the scheduler: the startup hook runs again on every
// server restart, and by then the recorded PID may belong to an unrelated
// process, which looks exactly like a running daemon. The PID is still written,
// for humans reading the file and for the error below, but never decides
// whether a daemon is live.
func acquireLock() (release func(), err error) {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(config.StateDir(), "daemon.pid")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another daemon is already running (pid %s)", holderPID(path))
	}

	if err := f.Truncate(0); err != nil {
		releaseFile(f)
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		releaseFile(f)
		return nil, err
	}

	return func() {
		os.Remove(path)
		releaseFile(f)
	}, nil
}

// releaseFile drops the flock. Closing the descriptor would be enough, but
// unlocking first keeps the intent obvious at the call sites.
func releaseFile(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

// holderPID reports what the pidfile claims, for the error message only. The
// file may be empty if the holder has not written its PID yet.
func holderPID(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	if pid := strings.TrimSpace(string(raw)); pid != "" {
		return pid
	}
	return "unknown"
}
