// Package hc is a thin client over herdr-core's CLI, the backend of the Herdr
// desktop app. Where that binary lives is hostpath's problem.
package hc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/hostpath"
)

// Client talks to the hc CLI. The zero value is ready.
type Client struct{}

// Wait is one `hc wait` answer, trimmed to what a run needs.
type Wait struct {
	// Ended is false when the wait only checked in (reason "timeout").
	Ended bool `json:"ended"`
	// Reason is status, exit, output, timeout or not_started.
	Reason string `json:"reason"`
	// Status is the agent's own word when Reason is status: idle, working,
	// done, blocked or error.
	Status   string `json:"status"`
	Turn     int    `json:"turn"`
	ExitCode *int   `json:"exit_code"`
}

// Terminal is one `hc get` answer, trimmed.
type Terminal struct {
	State string `json:"state"`
	// Reason is why a finished terminal finished: exited, or killed (its pane
	// was closed in the app).
	Reason   string `json:"reason"`
	ExitCode *int   `json:"exit_code"`
	// Status is what the agent last reported, nil when it never has.
	Status *struct {
		State string `json:"state"`
	} `json:"status"`
}

// ErrNotFound is hc's answer about a terminal it does not have, or no longer.
var ErrNotFound = errors.New("no such terminal")

func run(out any, args ...string) error {
	cmd := exec.Command(hostpath.HCBin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		var e struct {
			Error struct {
				Kind string `json:"kind"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(msg), &e) == nil && e.Error.Kind == "not_found" {
			return fmt.Errorf("hc %s: %s (%w)", args[0], msg, ErrNotFound)
		}
		return fmt.Errorf("hc %s: %s (%w)", args[0], msg, err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("hc %s: unexpected output %q: %w", args[0], stdout.String(), err)
	}
	return nil
}

// Run starts argv in a new kept terminal at cwd and returns its address. The
// terminal has no parent: the daemon is not one, and a run must outlive it.
func (Client) Run(cwd string, labels map[string]string, argv []string) (string, error) {
	args := []string{"run", "--bg", "--keep", "--no-parent", "--json", "--cwd", cwd}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}
	args = append(args, "--")
	args = append(args, argv...)
	var res struct {
		Address string `json:"address"`
	}
	if err := run(&res, args...); err != nil {
		return "", err
	}
	if res.Address == "" {
		return "", fmt.Errorf("hc run: no address in the answer")
	}
	return res.Address, nil
}

// Wait blocks until the terminal stops working, exits, or timeout passes.
func (Client) Wait(address string, timeout time.Duration) (Wait, error) {
	secs := max(1, int(timeout.Seconds()))
	var w Wait
	err := run(&w, "wait", address, "--json", "--timeout", strconv.Itoa(secs))
	return w, err
}

// Get reads one terminal's state.
func (Client) Get(address string) (Terminal, error) {
	var t Terminal
	err := run(&t, "get", address, "--json")
	return t, err
}

// Kill ends a terminal's program. A finished terminal is fine.
func (Client) Kill(address string) error { return run(nil, "kill", address, "--json") }

// Rm removes a finished terminal, so hc ls --all stops listing it.
func (Client) Rm(address string) error { return run(nil, "rm", address, "--json") }

// Available reports whether herdr-core answers on this machine.
func Available() bool {
	return exec.Command(hostpath.HCBin(), "server", "status").Run() == nil
}
