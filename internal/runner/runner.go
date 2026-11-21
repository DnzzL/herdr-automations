// Package runner executes one automation end to end and records every state
// transition in the history log. What it means to provision a workspace or get
// an agent to do something lives behind the host seam; what is left here is the
// lifecycle: skip an overlapping run, record what happened, decide whether a
// failure was a failure at all.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/host"
	"github.com/DnzzL/herdr-automations/internal/notify"
)

// Runner runs automations on a host. It holds the in-flight set, which used to
// be a package global that the daemon reached into to decide whether it could
// re-exec itself.
type Runner struct {
	host     host.Host
	notifier *notify.Notifier
	inFlight sync.Map
}

// New returns a Runner that works through h. Its notifier is nil, so it stays
// silent — the board and the CLI's `run` build one this way on purpose (see
// runner.record).
func New(h host.Host) *Runner { return &Runner{host: h} }

// NewWith returns a Runner that also toasts through n.
func NewWith(h host.Host, n *notify.Notifier) *Runner { return &Runner{host: h, notifier: n} }

// Default returns a Runner driving the backend automations.yaml names.
func Default() *Runner {
	b := config.BackendHerdr
	if cfg, err := config.Load(); err == nil {
		b = cfg.Backend
	}
	return New(host.For(b))
}

// Busy reports whether any automation is mid-run.
func (r *Runner) Busy() bool {
	busy := false
	r.inFlight.Range(func(_, _ any) bool { busy = true; return false })
	return busy
}

// Run executes the automation synchronously.
//
// Overlapping runs of the same automation are skipped rather than queued: if
// the 9:00 run is still working at 10:00, the 10:00 occurrence is dropped and
// recorded as such. A gate, when the automation has one, is the next thing
// asked: it can skip the run before a workspace exists.
func (r *Runner) Run(a config.Automation, trigger history.Trigger) error {
	if _, busy := r.inFlight.LoadOrStore(a.Name, true); busy {
		r.record(history.NewID(a.Name, ""), a.Name, trigger, history.StatusSkipped, host.Session{},
			"previous run still in flight")
		return fmt.Errorf("%s: previous run still in flight, skipped", a.Name)
	}
	defer r.inFlight.Delete(a.Name)

	id := history.NewID(a.Name, "")
	if len(a.Gate) > 0 {
		switch err := runGate(a); {
		case errors.Is(err, errGateSkip):
			r.record(id, a.Name, trigger, history.StatusSkipped, host.Session{}, "gate: nothing to do (exit 75)")
			// An error, like the in-flight skip: a person who asked for the run
			// must not be told it finished.
			return fmt.Errorf("%s: gate: nothing to do, skipped: %w", a.Name, ErrGateSkipped)
		case err != nil:
			r.record(id, a.Name, trigger, history.StatusFailed, host.Session{}, err.Error())
			return err
		}
	}
	r.record(id, a.Name, trigger, history.StatusScheduled, host.Session{}, "")

	session, err := r.host.Provision(a)
	if err != nil {
		r.record(id, a.Name, trigger, history.StatusFailed,
			host.Session{WorkspaceID: session.WorkspaceID, HerdrSession: session.HerdrSession}, err.Error())
		return err
	}
	r.record(id, a.Name, trigger, history.StatusRunning, session, "")

	timeout := time.Duration(a.TimeoutMinutes) * time.Minute
	if err := r.host.Do(session, a, timeout); err != nil {
		r.record(id, a.Name, trigger, statusFor(err), session, err.Error())
		return err
	}
	r.record(id, a.Name, trigger, history.StatusDone, session, "")
	// After the record: closing is also how a person cancels a run.
	if a.CloseWorkspace == config.CloseOnSuccess {
		if err := r.host.Close(session, a); err != nil {
			log.Printf("%s: done, but its workspace stays open: %v", a.Name, err)
		}
	}
	return nil
}

// gateSkipCode is EX_TEMPFAIL: the one exit status by which a gate says "not
// now" rather than "broken".
const gateSkipCode = 75

// gateTimeout is fixed on purpose (ADR 0014); a var only so a test can shorten it.
var gateTimeout = 60 * time.Second

// ErrGateSkipped is what Run returns for a run its gate skipped, so a caller
// can tell a skip from a failure.
var ErrGateSkipped = errors.New("skipped by gate")

var errGateSkip = errors.New("gate asked to skip")

// runGate runs the automation's gate in its repo, before anything is
// provisioned. nil lets the run go on, errGateSkip skips it, anything else is a
// failure whose message ends with the tail of the gate's stderr.
func runGate(a config.Automation) error {
	ctx, cancel := context.WithTimeout(context.Background(), gateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.Gate[0], a.Gate[1:]...)
	cmd.Dir = a.Repo
	var stderr tailBuffer
	cmd.Stderr = &stderr
	// A killed `sh -c` can leave a grandchild holding the pipe open.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return fmt.Errorf("gate timed out after %s%s", gateTimeout, stderr.tail())
	case errors.As(err, &exit) && exit.ExitCode() == gateSkipCode:
		return errGateSkip
	case errors.As(err, &exit):
		return fmt.Errorf("gate exited %d%s", exit.ExitCode(), stderr.tail())
	}
	return fmt.Errorf("gate could not run: %w", err)
}

// tailBuffer keeps the last 8 KiB written to it: a noisy gate cannot fill
// memory in its 60 s, and only the end is ever shown.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 8<<10 {
		t.b = t.b[len(t.b)-8<<10:]
	}
	return len(p), nil
}

// tail is the last lines of a gate's stderr as ": a | b | c", or nothing. One
// line because a toast shows only the first line of its body.
func (b *tailBuffer) tail() string {
	var lines []string
	for _, l := range strings.Split(string(b.b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	out := strings.Join(lines, " | ")
	if r := []rune(out); len(r) > 300 {
		out = "…" + string(r[len(r)-299:])
	}
	return ": " + out
}

// statusFor decides how a run that ended in err is remembered. Only a workspace
// closed under the run is not a failure.
func statusFor(err error) history.Status {
	if errors.Is(err, host.ErrCancelled) {
		return history.StatusCancelled
	}
	return history.StatusFailed
}

// record is the only writer of run history, which is what makes it the only
// caller of notify: the two cannot drift apart, and a future status inherits
// its notification for free without runner filtering anything.
func (r *Runner) record(id, name string, trigger history.Trigger, st history.Status, s host.Session, errMsg string) {
	err := history.Append(history.Record{
		RunID: id, Automation: name, Trigger: trigger, Status: st, At: time.Now(),
		Session: s.HerdrSession, WorkspaceID: s.WorkspaceID, PaneID: s.PaneID, Error: errMsg,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
	r.notifier.Outcome(name, st, errMsg)
}
