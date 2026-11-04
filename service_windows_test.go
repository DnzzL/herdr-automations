package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/DnzzL/herdr-automations/internal/service"
)

// serviceCLI runs `herdr-automations service <verb>` over the given state.
func serviceCLI(t *testing.T, state, verb string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "service", verb)
	cmd.Env = append(os.Environ(), "HERDR_AUTOMATIONS_TEST_CLI=1",
		"HERDR_PLUGIN_CONFIG_DIR="+t.TempDir(), "HERDR_PLUGIN_STATE_DIR="+state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("service %s: %v (%s)", verb, err, out)
	}
	return string(out)
}

// The panel's Start button and a user at a prompt both rely on this: install
// registers the scheduler for every login and starts one now, uninstall takes
// both away, and status tells them apart. It runs in this user's own HKCU.
func TestServiceInstallStartsTheSchedulerAndUninstallStopsIt(t *testing.T) {
	run, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err == nil {
		_, _, err = run.GetStringValue(service.Label)
		run.Close()
		if err == nil {
			t.Skip("a real login entry exists; not touching it")
		}
	}
	state := t.TempDir()
	t.Cleanup(func() { serviceCLI(t, state, "uninstall") })

	serviceCLI(t, state, "install")
	var status string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if status = serviceCLI(t, state, "status"); strings.Contains(status, "scheduler running: true") {
			break
		}
	}
	if !strings.Contains(status, "service installed: true") || !strings.Contains(status, "scheduler running: true") {
		t.Fatalf("after install, status says:\n%s", status)
	}

	serviceCLI(t, state, "uninstall")
	status = serviceCLI(t, state, "status")
	if !strings.Contains(status, "service installed: false") || !strings.Contains(status, "scheduler running: false") {
		t.Fatalf("after uninstall, status says:\n%s", status)
	}
}
