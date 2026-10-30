// Package service installs the daemon as a user service: a launchd agent on
// macOS, a systemd user unit on Linux. The Herdr desktop app has no plugin
// startup hook, and a scheduler must fire with no window open (ADR 0011).
package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Label names the service in launchd and systemd.
const Label = "dev.dnzzl.herdr-automations"

// Spec is what the service runs, settled at install time.
type Spec struct {
	Exe  string
	Logs string
	Env  map[string]string
}

// Install writes the service for this binary and starts it. env is baked in:
// the shell's PATH, so the daemon finds hc, git and the agents, and the
// resolved directories, so it reads the automations.yaml the CLI reads.
func Install(env map[string]string, logs string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	s := Spec{Exe: exe, Logs: logs, Env: env}
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

// Uninstall stops the service and removes its file.
func Uninstall() error {
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

// Installed reports whether the service file exists.
func Installed() bool {
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

// plist renders the launchd agent. --standby keeps it from exiting while the
// TUI's startup hook holds the scheduler lock: KeepAlive would relaunch it
// every ten seconds otherwise.
func plist(s Spec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + esc(Label) + `</string>
  <key>ProgramArguments</key>
  <array><string>` + esc(s.Exe) + `</string><string>daemon</string><string>--standby</string></array>
  <key>EnvironmentVariables</key>
  <dict>
`)
	for _, k := range sortedKeys(s.Env) {
		b.WriteString("    <key>" + esc(k) + "</key><string>" + esc(s.Env[k]) + "</string>\n")
	}
	b.WriteString(`  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>` + esc(s.Logs) + `</string>
  <key>StandardErrorPath</key><string>` + esc(s.Logs) + `</string>
</dict>
</plist>
`)
	return b.String()
}

func unit(s Spec) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=herdr-automations scheduler\n\n[Service]\n")
	b.WriteString(fmt.Sprintf("ExecStart=%q daemon --standby\n", s.Exe))
	for _, k := range sortedKeys(s.Env) {
		b.WriteString(fmt.Sprintf("Environment=%q\n", k+"="+s.Env[k]))
	}
	b.WriteString(fmt.Sprintf("StandardOutput=append:%s\nStandardError=append:%s\n", s.Logs, s.Logs))
	b.WriteString("Restart=always\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

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
