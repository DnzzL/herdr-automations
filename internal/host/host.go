// Package host is the seam between a run and the machine it happens on.
//
// Two methods: provision somewhere for the work to happen, then do the work.
// Behind them sits everything that used to be the runner's problem — which
// herdr error code means "retry", which means "the agent is gone", how long a
// fresh pane gets to become a shell, how a delegated command's exit status is
// recovered from a terminal that has no exit status.
//
// The point of the narrowness is that runner.Run becomes testable. Four of the
// ten releases before this seam existed fixed sequencing bugs in that function
// and not one of the fixes could be pinned by a test, because there was no
// interface between it and exec.Command.
package host

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
)

// Session is a provisioned place for a run to happen: a workspace and the pane
// its agent will live in.
type Session struct {
	WorkspaceID string
	PaneID      string
	// HerdrSession is the Herdr session both IDs belong to. Not to be confused
	// with Session itself, which predates Herdr having sessions.
	HerdrSession string
}

// Host provisions somewhere for a run to happen, and does the work there.
type Host interface {
	Provision(a config.Automation) (Session, error)
	Do(s Session, a config.Automation, timeout time.Duration) error
}

// ErrCancelled means the run's workspace was closed while it was working.
// Closing it is the gesture for calling a run off, so the run is reported
// cancelled rather than failed.
var ErrCancelled = errors.New("the run's workspace was closed")

// New returns the Host that drives the real Herdr.
func New() Host { return &live{ops: herdrOps{}, knobs: defaultKnobs()} }

// live is the production adapter: Herdr's CLI, a real clock, real sleeps.
type live struct {
	ops   ops
	knobs knobs
}

// knobs are the timings the choreography depends on. Fields rather than
// constants so the tests can drive the same code without sleeping.
type knobs struct {
	// paneReady is how long a freshly provisioned pane gets to become a shell.
	// Creating the workspace normally hands one back in milliseconds; the wait
	// exists for the run that starts as the machine wakes, where herdr answered
	// `worktree create` a quarter of an hour after it was asked and the pane's
	// shell was still not up.
	paneReady     time.Duration
	paneReadyPoll time.Duration
	// waitSlice bounds one herdr wait call. herdr reports a vanished pane only
	// when the call it was given returns, so this is how long a cancelled run
	// keeps its slot — not something to make long.
	waitSlice  time.Duration
	statusPoll time.Duration
	// promptSettle is how long the agent is watched after each nudge before
	// the next one; promptSettleLast gives the final attempt a little longer,
	// since there is nothing after it but giving up.
	promptSettle     time.Duration
	promptSettleLast time.Duration
	workflowPoll     time.Duration
}

func defaultKnobs() knobs {
	return knobs{
		paneReady:        2 * time.Minute,
		paneReadyPoll:    5 * time.Second,
		waitSlice:        30 * time.Second,
		statusPoll:       2 * time.Second,
		promptSettle:     20 * time.Second,
		promptSettleLast: 30 * time.Second,
		workflowPoll:     5 * time.Second,
	}
}

// in aims the host at the automation's Herdr session, if it names one.
func (h *live) in(a config.Automation) *live {
	if a.Session == "" {
		return h
	}
	return &live{ops: h.ops.In(a.Session), knobs: h.knobs}
}

// Provision opens the workspace the automation asked for.
func (h *live) Provision(a config.Automation) (Session, error) {
	h = h.in(a)
	label := "auto: " + a.Name
	var workspaceID, paneID string
	var err error
	switch a.Workspace {
	case config.WorkspaceWorktree:
		branch := fmt.Sprintf("auto/%s-%s", slug(a.Name), time.Now().Format("20060102-1504"))
		workspaceID, paneID, err = h.ops.WorktreeCreate(a.Repo, branch, label)
	case config.WorkspaceExisting:
		workspaceID = a.WorkspaceID
		paneID, err = h.ops.TabCreate(workspaceID, a.Repo, label+" "+time.Now().Format("2006-01-02 15:04"))
	case config.WorkspaceRoot:
		workspaceID, paneID, err = h.ops.WorkspaceCreate(a.Repo, label)
	default:
		err = fmt.Errorf("unknown workspace mode %q", a.Workspace)
	}
	return Session{WorkspaceID: workspaceID, PaneID: paneID, HerdrSession: h.ops.CurrentSession()}, err
}

// Do runs the automation's work in the session and reports whether it worked.
func (h *live) Do(s Session, a config.Automation, timeout time.Duration) error {
	return h.in(a).workFor(a).do(s, timeout)
}

// work is one way of getting an automation's work done in a session. Two
// adapters satisfy it — an agent taking a prompt, and a herdr-workflows
// delegation — and they share nothing but the question they answer: did it
// work?
type work interface {
	do(s Session, timeout time.Duration) error
}

func (h *live) workFor(a config.Automation) work {
	if a.Workflow != "" {
		return hwfWork{ops: h.ops, knobs: h.knobs, name: a.Workflow}
	}
	return agentWork{ops: h.ops, knobs: h.knobs, a: a}
}

// slug makes a name safe for a git branch: spaces and the characters
// git check-ref-format rejects would otherwise fail worktree creation.
func slug(name string) string {
	var b strings.Builder
	lastDash := true // also trims leading dashes
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "automation"
	}
	return out
}

// agentNameFor is the name this run's agent gets. Normally the automation's,
// which is what you want to read in Herdr's sidebar: one automation, one agent
// name, and the previous run's is free because its workspace was closed.
//
// In existing mode it is not free — earlier runs stay open in their own tabs on
// purpose, so their names are still live and a repeat would be refused. The
// pane ID goes in front to tell them apart, and in front rather than behind so
// the 32-character trim can only eat the automation name.
func agentNameFor(a config.Automation, paneID string) string {
	if a.Workspace != config.WorkspaceExisting {
		return agentName(a.Name)
	}
	return agentName(caseMarked(paneID) + "-" + a.Name)
}

// caseMarked prefixes each uppercase rune with an underscore, so that the
// lowercasing in slug stays injective. Herdr's pane IDs are case-sensitive and
// mixed case in practice (w1D:p1, w17:pB) while agent names are lowercase only,
// so folding them directly would let two live tabs land on one name.
func caseMarked(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// agentName fits an automation name into Herdr's agent-name rules: lowercase,
// [a-z0-9-_], at most 32 characters.
func agentName(name string) string {
	s := slug(name)
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-")
	}
	return s
}
