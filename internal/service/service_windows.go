package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/DnzzL/herdr-automations/internal/daemon"
)

// runKey holds what Windows starts at the user's login. It needs no admin and
// no COM, unlike Task Scheduler or a Startup-folder shortcut.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// installRunValue writes the login entry, then runs it once, which starts the
// scheduler now. No environment is baked in: Windows gives a login process the
// user's own PATH, and the default directories are home-relative. Nothing
// relaunches a scheduler that dies before the next login (no supervisor).
func installRunValue(s Spec) (string, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return "", fmt.Errorf("open HKCU\\%s: %w", runKey, err)
	}
	defer k.Close()
	if err := k.SetStringValue(Label, runCommand(s.Exe)); err != nil {
		return "", fmt.Errorf("write the login entry: %w", err)
	}
	// It returns as soon as the detached scheduler is started.
	if out, err := exec.Command(s.Exe, "daemon", "--standby", FromLogin).CombinedOutput(); err != nil {
		return "", fmt.Errorf("start the scheduler: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return `HKCU\` + runKey + `\` + Label, nil
}

// runCommand is the login entry's command line; the quotes keep a path with
// spaces whole.
func runCommand(exe string) string {
	return `"` + exe + `" daemon --standby ` + FromLogin
}

// uninstallRunValue removes the login entry and stops the scheduler, as
// launchd and systemd do on uninstall.
func uninstallRunValue() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err == nil {
		err = k.DeleteValue(Label)
		k.Close()
	}
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("remove the login entry: %w", err)
	}
	// The lock holder is the scheduler: the lock, not the pid, decides.
	pid, running := daemon.Running()
	if !running || pid == 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("stop the scheduler (pid %d): %w", pid, err)
	}
	if err := p.Kill(); err != nil {
		return fmt.Errorf("stop the scheduler (pid %d): %w", pid, err)
	}
	_, _ = p.Wait() // so status, right after, sees the lock free
	return nil
}

func runValueExists() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(Label)
	return err == nil
}
