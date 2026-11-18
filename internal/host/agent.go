package host

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
)

// agentWork starts an interactive agent, hands it the prompt, and waits for it
// to settle. Every step verifies what actually happened rather than trusting
// the previous call's verdict: a freshly started agent is reported ready before
// it can accept input, and herdr's answers are correspondingly hedged.
type agentWork struct {
	ops   ops
	knobs knobs
	a     config.Automation
}

// startError marks an agent that failed to start: the one failure worth
// retrying with another model. Once an agent started it holds the pane, and
// a prompt herdr calls stalled may still run, so later failures are final.
type startError struct{ err error }

func (e startError) Error() string { return e.err.Error() }
func (e startError) Unwrap() error { return e.err }

// do tries the automation's model, then each fallback_models entry, while the
// agent fails to start. The workspace, pane and prompt are unchanged between
// attempts; only the --model flag moves.
func (w agentWork) do(s Session, timeout time.Duration) error {
	models := append([]string{w.a.Model}, w.a.FallbackModels...)
	var errs []error
	for i, model := range models {
		if i > 0 {
			log.Printf("%s: retrying with fallback model %q after: %v", w.a.Name, model, errs[i-1])
		}
		err := w.doWithModel(s, model, timeout)
		if err == nil {
			if i > 0 {
				log.Printf("%s: ran on fallback model %q", w.a.Name, model)
			}
			return nil
		}
		var start startError
		if !errors.As(err, &start) {
			errs = append(errs, err)
			break
		}
		errs = append(errs, start.err)
	}
	if len(errs) == 1 {
		return errs[0]
	}
	// Every attempt's cause, not just the last: the first model's refusal is
	// usually the one worth reading.
	parts := make([]string, len(errs))
	args := make([]any, 0, 2*len(errs))
	for i, err := range errs {
		parts[i] = "%s: %w"
		args = append(args, modelLabel(models[i]), err)
	}
	return fmt.Errorf(fmt.Sprintf("%d models tried; ", len(errs))+strings.Join(parts, "; "), args...)
}

func modelLabel(model string) string {
	if model == "" {
		return "agent default"
	}
	return strconv.Quote(model)
}

func (w agentWork) doWithModel(s Session, model string, timeout time.Duration) error {
	if err := w.start(s, model); err != nil {
		err = fmt.Errorf("start %s agent: %w", w.a.Agent, err)
		// A pane that never became a shell will not take the next model either.
		if w.ops.HasCode(err, herdr.CodePaneBusy) {
			return err
		}
		return startError{err}
	}
	if err := w.submit(s); err != nil {
		return err
	}
	if err := w.await(s, timeout); err != nil {
		return fmt.Errorf("waiting for the agent: %w", err)
	}
	return nil
}

// args assembles what gets forwarded to the agent executable for one attempt.
// Model and MCP config are sugar over agent_args, and go in front so an
// explicit agent_args entry can still override them.
func (w agentWork) args(model string) []string { return agentArgs(w.a, model) }

func agentArgs(a config.Automation, model string) []string {
	args := a.AgentArgs
	if model != "" {
		args = append([]string{"--model", model}, args...)
	}
	if a.MCPConfig != "" {
		flag, path := "--mcp-config", a.MCPConfig
		if a.Agent == "copilot" {
			flag, path = "--additional-mcp-config", "@"+path
		}
		args = append([]string{flag, path}, args...)
	}
	return args
}

// start launches the agent, retrying while herdr says the pane is not a shell
// yet. Any other error is final — a bad agent kind or a missing binary will
// not fix itself, and retrying only delays the report.
func (w agentWork) start(s Session, model string) error {
	name := agentNameFor(w.a, s.PaneID)
	deadline := time.Now().Add(w.knobs.paneReady)
	for {
		err := w.ops.AgentStart(name, w.a.Agent, s.PaneID, w.args(model))
		// An unread previous run still holds the name: take the pane-marked
		// one existing mode uses, rather than fail every run until it is read.
		if paneName := paneAgentName(w.a, s.PaneID); w.ops.HasCode(err, herdr.CodeNameTaken) && name != paneName {
			name = paneName
			continue
		}
		if err == nil || !w.ops.HasCode(err, herdr.CodePaneBusy) {
			return err
		}
		if !time.Now().Before(deadline) {
			return err
		}
		log.Printf("pane has no shell yet, retrying agent start in %s", w.knobs.paneReadyPoll)
		time.Sleep(w.knobs.paneReadyPoll)
	}
}

// submit gets the prompt in front of the agent and confirms it started working.
//
// A freshly started agent is reported ready before it can actually accept
// input — it may still be connecting MCP servers, especially right after the
// machine wakes. herdr then reports agent_prompt_stalled even though the text
// reached the composer, so each step here checks the status rather than
// trusting the previous call's verdict.
func (w agentWork) submit(s Session) error {
	err := w.ops.AgentSubmit(s.PaneID, w.a.Prompt)
	if err == nil {
		return nil
	}
	if !w.ops.HasCode(err, herdr.CodeStalled) {
		return fmt.Errorf("prompt: %w", err)
	}

	// The text is probably in the composer, just not submitted. Give the agent
	// a moment, then press Enter for it, then fall back to typing it again.
	if w.working(s, w.knobs.promptSettle) {
		return nil
	}
	log.Printf("prompt stalled on %s, submitting the pending composer", s.PaneID)
	if err := w.ops.AgentSubmitPending(s.PaneID); err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	if w.working(s, w.knobs.promptSettle) {
		return nil
	}

	log.Printf("still idle on %s, retyping the prompt", s.PaneID)
	if err := w.ops.AgentSubmit(s.PaneID, w.a.Prompt); err != nil && !w.ops.HasCode(err, herdr.CodeStalled) {
		return fmt.Errorf("prompt: %w", err)
	}
	if w.working(s, w.knobs.promptSettleLast) {
		return nil
	}
	return fmt.Errorf("prompt: the agent never started working; it may still be initialising")
}

// working polls until the agent leaves idle, or the window closes.
func (w agentWork) working(s Session, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		status, err := w.ops.AgentStatus(s.PaneID)
		if err == nil && status != "idle" && status != "unknown" {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(w.knobs.statusPoll)
	}
}

// await blocks until the agent settles, the run is cancelled, or the timeout
// runs out.
//
// It waits in slices rather than handing herdr the whole timeout at once. A run
// whose workspace was closed thirteen seconds in used to sit here for the full
// forty-five minutes before anyone was told, holding the automation's in-flight
// slot the whole time and skipping the next occurrence.
func (w agentWork) await(s Session, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if last != nil {
				return last
			}
			return fmt.Errorf("the agent was still working after %s", timeout)
		}
		slice := min(w.knobs.waitSlice, remaining)

		err := w.ops.AgentWait(s.PaneID, slice)
		if err == nil {
			return nil
		}
		if w.ops.HasCode(err, herdr.CodeAgentGone) {
			return w.agentGoneOutcome(s)
		}
		// The slice expired with the agent still working, which is the normal
		// case. Keep it: if the deadline passes it is the most accurate thing
		// we have to report.
		last = err
	}
}

// agentGoneOutcome disambiguates herdr's agent_not_running: it means either
// the workspace was closed, or the agent process died inside a workspace that
// is still open. herdr.go documents both under the same code, but only the
// first is a cancellation — the second is a crash, and reporting it as
// "cancelled" hides it behind the one status the plugin deliberately never
// notifies about.
func (w agentWork) agentGoneOutcome(s Session) error {
	_, err := w.ops.AgentStatus(s.PaneID)
	switch {
	case err == nil:
		// The pane is alive and answered, so nothing closed the workspace —
		// the agent itself is what is missing.
		return fmt.Errorf("the agent exited before finishing")
	case w.ops.HasCode(err, herdr.CodeWorkspaceGone):
		return ErrCancelled
	case w.ops.HasCode(err, herdr.CodeAgentGone):
		// AgentStatus is itself agent-scoped, so a dead agent in a live
		// workspace is exactly as likely to answer with its own
		// agent_not_running as with a status string. Either shape means the
		// same thing: the workspace is fine, the agent is what is gone.
		return fmt.Errorf("the agent exited before finishing")
	default:
		// Unreadable for some other, unrelated reason: not enough to call it
		// a crash, so keep the existing behaviour rather than turning a doubt
		// into a failure.
		log.Printf("could not confirm why %s went silent (%v), recording it as cancelled", s.PaneID, err)
		return ErrCancelled
	}
}
