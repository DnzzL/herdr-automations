package daemon

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes a LockFileEx lock without waiting. Windows locks are
// mandatory: locking the PID itself would make it unreadable to Running and
// to the error naming the holder, so the lock covers one byte far past the end
// of the file, where nothing is ever read or written. Like flock, the OS drops
// it when the holder exits, however it exits.
func lockFile(f *os.File, exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, lockedByte())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}

func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockedByte())
}

// lockedByte is the offset every process locks; a pidfile never grows near it.
func lockedByte() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: 0x7fffffff}
}
