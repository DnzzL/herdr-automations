package host

import (
	"os/exec"
	"time"

	"github.com/DnzzL/herdr-automations/internal/herdr"
)

// ops is this package's internal seam: the individual Herdr calls, one method
// each. It is unexported on purpose — callers of Host must not have to know
// that starting an agent can answer "the pane is not a shell yet", only that
// the work either happened or didn't. The tests script it.
type ops interface {
	WorktreeCreate(repo, branch, label string) (workspaceID, paneID string, err error)
	WorkspaceCreate(cwd, label string) (workspaceID, paneID string, err error)
	TabCreate(workspaceID, cwd, label string) (paneID string, err error)

	AgentStart(name, kind, paneID string, extraArgs []string) error
	AgentSubmit(target, text string) error
	AgentSubmitPending(paneID string) error
	AgentStatus(target string) (string, error)
	AgentWait(target string, timeout time.Duration) error

	PaneRun(paneID string, command ...string) error
	PaneRead(paneID string, lines int) (string, error)

	// In returns the same calls aimed at a named Herdr session.
	In(session string) ops
	// CurrentSession names the session these calls land in; empty if unknown.
	CurrentSession() string

	// HasCode reports whether err is a Herdr API error with the given code.
	// It travels with the ops so a fake can answer for its own errors.
	HasCode(err error, code string) bool
	// LookPath reports whether a binary the delegation needs is installed.
	LookPath(file string) error
}

// herdrOps is the production ops. The Herdr calls come from the embedded
// client; the two that are not Herdr's business are here.
type herdrOps struct {
	herdr.Client
}

func (o herdrOps) In(session string) ops {
	return herdrOps{herdr.Client{Session: session}}
}

func (herdrOps) HasCode(err error, code string) bool { return herdr.HasCode(err, code) }

func (herdrOps) LookPath(file string) error {
	_, err := exec.LookPath(file)
	return err
}
