// Package herdr is a thin client over the herdr CLI (which itself fronts the
// socket API). Herdr injects HERDR_BIN_PATH for plugins; outside a plugin
// context, or when that path no longer resolves, we fall back to `herdr` on
// PATH.
package herdr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// bin locates the herdr CLI. HERDR_BIN_PATH is herdr telling a plugin exactly
// which binary to call back into, so it wins — but only if it still exists. A
// long-running herdr whose binary was moved or uninstalled under it (a package
// manager switch, say) keeps advertising the old path, and taking it on faith
// made every call fail with a fork/exec error the user could do nothing about.
func bin() string {
	if b := os.Getenv("HERDR_BIN_PATH"); b != "" && usable(b) {
		return b
	}
	return "herdr"
}

// usable reports whether path is something we could actually execute.
func usable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// run executes a herdr subcommand and decodes the socket-API JSON envelope
// ({"id": ..., "result": {...}}) into out when out is non-nil.
func run(out any, args ...string) error {
	cmd := exec.Command(bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return newAPIError(args, stdout.Bytes(), stderr.String(), err)
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

// APIError carries herdr's error code so callers can recover from the ones
// that are recoverable, and so logs get one readable line instead of a JSON
// payload.
type APIError struct {
	Command string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" && e.Message != "" {
		return e.Command + ": " + e.Code + ": " + e.Message
	}
	if e.Code != "" {
		return e.Command + ": " + e.Code
	}
	return e.Command + ": " + e.Message
}

// HasCode reports whether err is a herdr API error with the given code.
func HasCode(err error, code string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

// newAPIError turns a failed herdr invocation into one readable line. runErr
// is the error from cmd.Run and is the last resort: a herdr that exits non-zero
// while printing nothing at all used to produce a bare "worktree create: ",
// which says only that the run failed — not that it exited 1, was killed, or
// was never on PATH.
func newAPIError(args []string, stdout []byte, stderr string, runErr error) error {
	cmd := strings.Join(args[:min(2, len(args))], " ")
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(stdout, &envelope) == nil && envelope.Error.Code != "" {
		return &APIError{Command: cmd, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(string(stdout))
	}
	if msg == "" && runErr != nil {
		msg = runErr.Error()
	}
	return &APIError{Command: cmd, Message: msg}
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

// Worktree is one checkout backing a workspace. OpenWorkspaceID is empty once
// the workspace has been closed, which is the only durable signal Herdr keeps
// about whether anyone came back to look at a run.
type Worktree struct {
	Branch           string `json:"branch"`
	Path             string `json:"path"`
	OpenWorkspaceID  string `json:"open_workspace_id"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

// WorktreeList returns every worktree Herdr knows about for repo, including
// the source checkout itself.
func WorktreeList(repo string) ([]Worktree, error) {
	var res struct {
		Worktrees []Worktree `json:"worktrees"`
	}
	if err := run(&res, "worktree", "list", "--cwd", repo); err != nil {
		return nil, err
	}
	return res.Worktrees, nil
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

// CodePaneBusy is herdr's refusal to start an agent in a pane that is not
// sitting at a shell prompt. It means the workspace exists but its shell has
// not spawned yet, so it is a wait-and-retry rather than a dead run: a
// sleep-delayed worktree create can return minutes before the pane is usable.
const CodePaneBusy = "agent_pane_busy"

// CodeAgentGone means there is no agent in the target pane any more — the
// workspace was closed, or the agent exited on its own. herdr only reports it
// when the call it was given returns, so a wait handed the run's whole timeout
// sits on a dead pane for that long before saying so.
const CodeAgentGone = "agent_not_running"

// CodeStalled is herdr's verdict when a submitted prompt produces no visible
// state change within 5 seconds. It does not mean the prompt was lost — an
// agent still loading its MCP servers takes longer than that to react.
const CodeStalled = "agent_prompt_stalled"

// AgentSubmit types a prompt into the agent and asks herdr to confirm the
// agent reacted. A CodeStalled error is inconclusive; callers should check the
// status before giving up.
func AgentSubmit(target, text string) error {
	return run(nil, "agent", "prompt", target, text, "--wait", "--until", "working",
		"--timeout", "30000")
}

// AgentStatus reports the agent's current state: idle, working, blocked…
func AgentStatus(target string) (string, error) {
	var res struct {
		Agent struct {
			AgentStatus string `json:"agent_status"`
		} `json:"agent"`
	}
	if err := run(&res, "agent", "get", target); err != nil {
		return "", err
	}
	return res.Agent.AgentStatus, nil
}

// AgentSubmitPending presses Enter on the pane, submitting anything already
// sitting in the composer. Harmless when the composer is empty.
func AgentSubmitPending(paneID string) error {
	return run(nil, "pane", "send-keys", paneID, "enter")
}

// AgentWait blocks until the agent settles (idle, done or blocked).
func AgentWait(target string, timeout time.Duration) error {
	return run(nil, "agent", "wait", target,
		"--timeout", fmt.Sprintf("%d", timeout.Milliseconds()))
}

// ErrGone means the run's workspace no longer exists — the expected outcome
// once you've reviewed and closed it, not a failure worth a stack trace.
var ErrGone = errors.New("workspace already closed")

// Focus brings a run's workspace to the front, then its agent pane when one
// is known — the "jump to what this automation did" move.
func Focus(workspaceID, paneID string) error {
	if workspaceID != "" {
		if err := run(nil, "workspace", "focus", workspaceID); err != nil {
			if strings.Contains(err.Error(), "workspace_not_found") {
				return ErrGone
			}
			return err
		}
	}
	if paneID != "" {
		// Best-effort: the pane may be gone while the workspace lives on.
		_ = run(nil, "agent", "focus", paneID)
	}
	return nil
}

// PaneRun executes a shell command in a pane (used to delegate to hwf).
func PaneRun(paneID string, command ...string) error {
	return run(nil, append([]string{"pane", "run", paneID}, command...)...)
}

// PaneRead returns the pane's recent terminal output. A pane is the only
// channel a delegated command has, so this is how its result gets read back.
// Unlike the rest of the API this one prints the screen, not a JSON envelope.
func PaneRead(paneID string, lines int) (string, error) {
	cmd := exec.Command(bin(), "pane", "read", paneID,
		"--source", "recent", "--lines", fmt.Sprintf("%d", lines), "--format", "text")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", newAPIError([]string{"pane", "read"}, stdout.Bytes(), stderr.String(), err)
	}
	return stdout.String(), nil
}
