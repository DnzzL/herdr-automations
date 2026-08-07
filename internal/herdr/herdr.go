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

// createResult matches both worktree_created and workspace_created payloads:
// the new workspace plus its initial pane, ready for `agent start`.
type createResult struct {
	Workspace struct {
		WorkspaceID string `json:"workspace_id"`
	} `json:"workspace"`
	RootPane struct {
		PaneID string `json:"pane_id"`
	} `json:"root_pane"`
}

func (r createResult) ids(what string) (string, string, error) {
	if r.Workspace.WorkspaceID == "" || r.RootPane.PaneID == "" {
		return "", "", fmt.Errorf("%s returned no workspace/pane id", what)
	}
	return r.Workspace.WorkspaceID, r.RootPane.PaneID, nil
}

// WorktreeCreate provisions a fresh git worktree workspace off repo and
// returns its workspace and root pane IDs.
func WorktreeCreate(repo, branch, label string) (workspaceID, paneID string, err error) {
	var res createResult
	err = run(&res, "worktree", "create",
		"--cwd", repo, "--branch", branch, "--label", label, "--no-focus")
	if err != nil {
		return "", "", err
	}
	return res.ids("worktree create")
}

// WorkspaceCreate opens a workspace directly on a directory (root mode).
func WorkspaceCreate(cwd, label string) (workspaceID, paneID string, err error) {
	var res createResult
	err = run(&res, "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		return "", "", err
	}
	return res.ids("workspace create")
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
