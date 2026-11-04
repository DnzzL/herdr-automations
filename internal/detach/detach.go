package detach

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Start starts exe detached and returns at once, its output appended to log,
// whose folder it creates: with no terminal and no service manager, nothing
// else keeps it. A nil env inherits this process's.
func Start(log, exe string, args, env []string) error {
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logf, logf
	setDetached(cmd)
	return cmd.Start()
}
