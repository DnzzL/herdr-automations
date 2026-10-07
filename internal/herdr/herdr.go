// Package herdr is a thin client over the herdr CLI (which itself fronts the
// socket API). Where that binary lives is hostpath's problem.
//
// The calls are methods on Client rather than package functions so a caller
// that wants a narrower interface can embed it and get the whole set, instead
// of hand-forwarding a dozen one-line wrappers.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/hostpath"
)

// Client talks to the herdr CLI. It holds nothing: the zero value is ready.
type Client struct{}

// run executes a herdr subcommand and decodes the socket-API JSON envelope
// ({"id": ..., "result": {...}}) into out when out is non-nil.
func run(out any, args ...string) error {
	return runCtx(context.Background(), out, args...)
}

// runCtx is run with a context, so a caller that cannot afford to block
// forever — a toast, not a run step — can bound it.
func runCtx(ctx context.Context, out any, args ...string) error {
	cmd := exec.CommandContext(ctx, hostpath.Bin(), args...)
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
	// herdr ≥ 0.8.0 prints the error envelope on stderr; older builds used stdout.
	for _, out := range [][]byte{stdout, []byte(stderr)} {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(out, &envelope) == nil && envelope.Error.Code != "" {
			return &APIError{Command: cmd, Code: envelope.Error.Code, Message: envelope.Error.Message}
		}
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
func (Client) WorktreeCreate(repo, branch, label string) (workspaceID, paneID string, err error) {
	var res createResult
	err = run(&res, "worktree", "create",
		"--cwd", repo, "--branch", branch, "--label", label, "--no-focus")
	if err != nil {
		return "", "", err
	}
	return res.ids("worktree create")
}

// WorkspaceCreate opens a workspace directly on a directory (root mode).
func (Client) WorkspaceCreate(cwd, label string) (workspaceID, paneID string, err error) {
	var res createResult
	err = run(&res, "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		return "", "", err
	}
	return res.ids("workspace create")
}

// TabCreate gives a run its own shell without creating a workspace.
func (Client) TabCreate(workspaceID, cwd, label string) (string, error) {
	var res createResult
	if err := run(&res, "tab", "create", "--workspace", workspaceID,
		"--cwd", cwd, "--label", label, "--no-focus"); err != nil {
		return "", err
	}
	if res.RootPane.PaneID == "" {
		return "", fmt.Errorf("tab create returned no pane id")
	}
	return res.RootPane.PaneID, nil
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
func (Client) WorktreeList(repo string) ([]Worktree, error) {
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
func (Client) AgentStart(name, kind, paneID string, extraArgs []string) error {
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

// CodeWorkspaceGone is herdr's answer when the workspace ID no longer names
// anything — the expected result of asking about a run somebody has reviewed
// and closed.
const CodeWorkspaceGone = "workspace_not_found"

// CodeStalled is herdr's verdict when a submitted prompt produces no visible
// state change within 5 seconds. It does not mean the prompt was lost — an
// agent still loading its MCP servers takes longer than that to react.
const CodeStalled = "agent_prompt_stalled"

// AgentSubmit types a prompt into the agent and asks herdr to confirm the
// agent reacted. A CodeStalled error is inconclusive; callers should check the
// status before giving up.
func (Client) AgentSubmit(target, text string) error {
	return run(nil, "agent", "prompt", target, text, "--wait", "--until", "working",
		"--timeout", "30000")
}

// AgentStatus reports the agent's current state: idle, working, blocked…
func (Client) AgentStatus(target string) (string, error) {
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
func (Client) AgentSubmitPending(paneID string) error {
	return run(nil, "pane", "send-keys", paneID, "enter")
}

// AgentWait blocks until the agent settles (idle, done or blocked).
func (Client) AgentWait(target string, timeout time.Duration) error {
	return run(nil, "agent", "wait", target,
		"--timeout", fmt.Sprintf("%d", timeout.Milliseconds()))
}

// ErrGone means the run's workspace no longer exists — the expected outcome
// once you've reviewed and closed it, not a failure worth a stack trace.
var ErrGone = errors.New("workspace already closed")

// Focus brings a run's workspace to the front, then its agent pane when one
// is known — the "jump to what this automation did" move.
func (Client) Focus(workspaceID, paneID string) error {
	if workspaceID != "" {
		if err := run(nil, "workspace", "focus", workspaceID); err != nil {
			if HasCode(err, CodeWorkspaceGone) {
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

// Sound is which of herdr's notification sounds a toast plays.
type Sound string

const (
	SoundNone    Sound = "none"
	SoundDone    Sound = "done"
	SoundRequest Sound = "request"
)

// notificationTimeout bounds a toast. A local socket call needs milliseconds;
// three seconds is the point past which a wedged herdr is costing a finished
// run something.
const notificationTimeout = 3 * time.Second

// notificationPosition: the board is an overlay pane and list prints at the
// bottom, so a toast goes top-right and covers neither.
const notificationPosition = "top-right"

// notificationArgs assembles the toast's argv. Split out because the one thing
// worth pinning — an empty body producing no --body flag rather than an empty
// one — is testable with no herdr to talk to.
func notificationArgs(title, body string, sound Sound) []string {
	args := []string{"notification", "show", title}
	if body != "" {
		args = append(args, "--body", body)
	}
	args = append(args, "--position", notificationPosition, "--sound", string(sound))
	return args
}

// NotificationShow raises an in-session Herdr toast. In-session is the whole
// caveat: a panel in the Herdr window, not an OS notification.
func (Client) NotificationShow(title, body string, sound Sound) error {
	ctx, cancel := context.WithTimeout(context.Background(), notificationTimeout)
	defer cancel()
	return runCtx(ctx, nil, notificationArgs(title, body, sound)...)
}

// PaneRun executes a shell command in a pane (used to delegate to hwf).
func (Client) PaneRun(paneID string, command ...string) error {
	return run(nil, append([]string{"pane", "run", paneID}, command...)...)
}

// PaneRead returns the pane's recent terminal output. A pane is the only
// channel a delegated command has, so this is how its result gets read back.
// Unlike the rest of the API this one prints the screen, not a JSON envelope.
func (Client) PaneRead(paneID string, lines int) (string, error) {
	cmd := exec.Command(hostpath.Bin(), "pane", "read", paneID,
		"--source", "recent", "--lines", fmt.Sprintf("%d", lines), "--format", "text")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", newAPIError([]string{"pane", "read"}, stdout.Bytes(), stderr.String(), err)
	}
	return stdout.String(), nil
}
