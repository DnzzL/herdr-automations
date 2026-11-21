package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DnzzL/herdr-automations/internal/config"
)

// acquireLock keeps a single scheduler alive per machine. Two daemons would
// fire every automation twice.
//
// The lock is an OS file lock on the pidfile, not the PID it contains. The
// OS drops it when the holder exits, however it exits, so a daemon
// killed with the Herdr server cannot leave a lock behind. Trusting the PID
// instead would strand the scheduler: the startup hook runs again on every
// server restart, and by then the recorded PID may belong to an unrelated
// process, which looks exactly like a running daemon. The PID is still written,
// for humans reading the file and for the error below, but never decides
// whether a daemon is live.
// ErrAlreadyRunning is the expected answer for every startup hook but the
// first: each Herdr session starts the daemon, and one per machine serves them
// all (ADR 0010).
var ErrAlreadyRunning = errors.New("another daemon is already running")

func acquireLock() (release func(), err error) {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(config.StateDir(), "daemon.pid")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f, true); err != nil {
		f.Close()
		// Only a held lock means a daemon. Reporting any other failure — a
		// filesystem without flock, say — as one would send someone hunting
		// for a process that does not exist.
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("%w (pid %s)", ErrAlreadyRunning, holderPID(path))
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	if err := f.Truncate(0); err != nil {
		releaseFile(f)
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		releaseFile(f)
		return nil, err
	}

	// Empty the file on release rather than remove it. A daemon that opened it
	// a moment earlier would otherwise lock a file that is no longer there, and
	// the next one would lock a fresh one beside it.
	return func() {
		f.Truncate(0)
		releaseFile(f)
	}, nil
}

// errLocked is what lockFile returns when another process holds the lock.
var errLocked = errors.New("locked by another process")

// releaseFile drops the lock. Closing the descriptor would be enough, but
// unlocking first keeps the intent obvious at the call sites.
func releaseFile(f *os.File) {
	unlockFile(f)
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

// Running reports whether a scheduler holds the lock, and its pid when the
// pidfile says. The lock decides, not the pid: a pid can outlive its daemon
// or be reused.
func Running() (int, bool) {
	f, err := os.Open(filepath.Join(config.StateDir(), "daemon.pid"))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if err := lockFile(f, false); err == nil {
		unlockFile(f)
		return 0, false
	}
	pid, _ := strconv.Atoi(holderPID(f.Name()))
	return pid, true
}
