// Package service installs the daemon as a user service: a launchd agent on
// macOS, a systemd user unit on Linux, a value under the user's Run registry
// key on Windows. The Herdr desktop app has no plugin
// startup hook, and a scheduler must fire with no window open (ADR 0011).
package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// Label names the service in launchd and systemd, and the Run value on Windows.
const Label = "dev.dnzzl.herdr-automations"

// FromLogin marks a scheduler the Windows login entry started. Windows opens
// a console window for it, so it starts a detached copy of itself and exits
// (see daemon.StartStandby). A flag rather than sniffing for a fresh console:
// it is explicit, and a test can drive it where no console exists. It is not
// in the usage: nobody but the login entry passes it.
const FromLogin = "--from-login"

// Spec is what the service runs, settled at install time.
type Spec struct {
	Exe  string
	Logs string
	Env  map[string]string
}

// Install writes the service running program and starts it. env is baked in:
// the shell's PATH, so the daemon finds hc, git and the agents, and the
// resolved directories, so it reads the automations.yaml the CLI reads.
func Install(program string, env map[string]string, logs string) (string, error) {
	return install(Spec{Exe: program, Logs: logs, Env: env})
}

// Uninstall stops the service and removes its file.
func Uninstall() error { return uninstall() }

// Installed reports whether the service file (on Windows, the Run value) exists.
func Installed() bool { return installed() }

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
