//go:build !windows

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// launchd on macOS, systemd on Linux; the Windows Run value is in
// service_windows.go.

func install(s Spec) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		path := plistPath()
		if err := writeFile(path, plist(s)); err != nil {
			return "", err
		}
		_ = launchctl("bootout", domain()+"/"+Label) // a previous install, if any
		return path, launchctl("bootstrap", domain(), path)
	case "linux":
		path := unitPath()
		if err := writeFile(path, unit(s)); err != nil {
			return "", err
		}
		if err := systemctl("daemon-reload"); err != nil {
			return "", err
		}
		return path, systemctl("enable", "--now", "herdr-automations.service")
	}
	return "", fmt.Errorf("no service support on %s; run `herdr-automations daemon` yourself", runtime.GOOS)
}

func uninstall() error {
	switch runtime.GOOS {
	case "darwin":
		_ = launchctl("bootout", domain()+"/"+Label)
		return removeIfThere(plistPath())
	case "linux":
		_ = systemctl("disable", "--now", "herdr-automations.service")
		if err := removeIfThere(unitPath()); err != nil {
			return err
		}
		return systemctl("daemon-reload")
	}
	return fmt.Errorf("no service support on %s", runtime.GOOS)
}

func installed() bool {
	path := unitPath()
	if runtime.GOOS == "darwin" {
		path = plistPath()
	}
	_, err := os.Stat(path)
	return err == nil
}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

func unitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", "herdr-automations.service")
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func removeIfThere(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func launchctl(args ...string) error {
	if out, err := exec.Command("launchctl", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

func systemctl(args ...string) error {
	all := append([]string{"--user"}, args...)
	if out, err := exec.Command("systemctl", all...).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}
