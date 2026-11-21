//go:build unix

package daemon

import "syscall"

// execve is syscall.Exec, swappable so a test can make the re-exec fail.
var execve = syscall.Exec
