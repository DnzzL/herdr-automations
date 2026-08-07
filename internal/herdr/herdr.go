// Package herdr is a thin client over the herdr CLI (which itself fronts the
// socket API). Herdr injects HERDR_BIN_PATH for plugins; outside a plugin
// context we fall back to `herdr` on PATH.
package herdr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func bin() string {
	if b := os.Getenv("HERDR_BIN_PATH"); b != "" {
		return b
	}
	return "herdr"
}

// run executes a herdr subcommand and decodes the socket-API JSON envelope
// ({"id": ..., "result": {...}}) into out when out is non-nil.
func run(out any, args ...string) error {
	cmd := exec.Command(bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("herdr %v: %w: %s", args, err, stderr.String())
	}
	if out == nil {
		return nil
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return fmt.Errorf("herdr %v: unexpected output %q: %w", args, stdout.String(), err)
	}
	raw := envelope.Result
	if raw == nil {
		raw = stdout.Bytes() // some commands print the result object bare
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("herdr %v: decode result: %w", args, err)
	}
	return nil
}

type Pane struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent"`
	AgentStatus string `json:"agent_status"`
	CWD         string `json:"cwd"`
}

// WorktreeCreate provisions a fresh git worktree workspace off repo and
// returns its workspace ID.
func WorktreeCreate(repo, branch, label string) (string, error) {
	var res struct {
		WorkspaceID string `json:"workspace_id"`
	}
	err := run(&res, "worktree", "create",
		"--cwd", repo, "--branch", branch, "--label", label, "--no-focus")
	if err != nil {
		return "", err
	}
	if res.WorkspaceID == "" {
		return "", fmt.Errorf("worktree create returned no workspace_id")
	}
	return res.WorkspaceID, nil
}

// WorkspaceCreate opens a workspace directly on a directory (root mode).
func WorkspaceCreate(cwd, label string) (string, error) {
	var res struct {
		WorkspaceID string `json:"workspace_id"`
	}
	err := run(&res, "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		return "", err
	}
	if res.WorkspaceID == "" {
		return "", fmt.Errorf("workspace create returned no workspace_id")
	}
	return res.WorkspaceID, nil
}

// FirstPane polls for the workspace's initial pane; workspace creation is
// asynchronous enough that the pane can lag by a beat.
func FirstPane(workspaceID string) (string, error) {
	deadline := time.Now().Add(15 * time.Second)
	for {
		var res struct {
			Panes []Pane `json:"panes"`
		}
		if err := run(&res, "pane", "list"); err != nil {
			return "", err
		}
		for _, p := range res.Panes {
			if p.WorkspaceID == workspaceID {
				return p.PaneID, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no pane appeared in workspace %s", workspaceID)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// AgentStart launches an interactive agent in a pane sitting at a shell
// prompt. extraArgs are forwarded to the agent executable (e.g. --mcp-config).
func AgentStart(name, kind, paneID string, extraArgs []string) error {
	args := []string{"agent", "start", name, "--kind", kind, "--pane", paneID}
	if len(extraArgs) > 0 {
		args = append(args, "--")
		args = append(args, extraArgs...)
	}
	return run(nil, args...)
}

// AgentPrompt submits a prompt and waits for the agent to settle (idle, done
// or blocked), bounded by timeout.
func AgentPrompt(target, text string, timeout time.Duration) error {
	return run(nil, "agent", "prompt", target, text,
		"--wait", "--timeout", fmt.Sprintf("%d", timeout.Milliseconds()))
}

// PaneRun executes a shell command in a pane (used to delegate to hwf).
func PaneRun(paneID string, command ...string) error {
	return run(nil, append([]string{"pane", "run", paneID}, command...)...)
}
