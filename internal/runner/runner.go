// Package runner executes one automation end to end: provision a workspace,
// start the agent, submit the prompt (or delegate to herdr-workflows), and
// record every state transition in the history log.
package runner

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
)

// inFlight guards against overlapping runs of the same automation: if the
// 9:00 run is still working at 10:00, the 10:00 tick is skipped, not queued.
var inFlight sync.Map

// Busy reports whether any automation is mid-run.
func Busy() bool {
	busy := false
	inFlight.Range(func(_, _ any) bool { busy = true; return false })
	return busy
}

// Run executes the automation synchronously. trigger is "cron", "catchup" or
// "manual".
func Run(a config.Automation, trigger string) error {
	if _, busy := inFlight.LoadOrStore(a.Name, true); busy {
		record(runID(a.Name), a.Name, trigger, history.StatusSkipped, "", "",
			"previous run still in flight")
		return fmt.Errorf("%s: previous run still in flight, skipped", a.Name)
	}
	defer inFlight.Delete(a.Name)

	id := runID(a.Name)
	record(id, a.Name, trigger, history.StatusScheduled, "", "", "")

	workspaceID, paneID, err := provision(a)
	if err != nil {
		record(id, a.Name, trigger, history.StatusFailed, workspaceID, "", err.Error())
		return err
	}
	record(id, a.Name, trigger, history.StatusRunning, workspaceID, paneID, "")

	if err := execute(a, paneID); err != nil {
		record(id, a.Name, trigger, history.StatusFailed, workspaceID, paneID, err.Error())
		return err
	}
	record(id, a.Name, trigger, history.StatusDone, workspaceID, paneID, "")
	return nil
}

func provision(a config.Automation) (workspaceID, paneID string, err error) {
	label := "auto: " + a.Name
	switch a.Workspace {
	case config.WorkspaceWorktree:
		branch := fmt.Sprintf("auto/%s-%s", slug(a.Name), time.Now().Format("20060102-1504"))
		workspaceID, paneID, err = herdr.WorktreeCreate(a.Repo, branch, label)
	case config.WorkspaceRoot:
		workspaceID, paneID, err = herdr.WorkspaceCreate(a.Repo, label)
	}
	return workspaceID, paneID, err
}

func execute(a config.Automation, paneID string) error {
	timeout := time.Duration(a.TimeoutMinutes) * time.Minute

	if a.Workflow != "" {
		// Delegation: herdr-workflows owns multi-step execution.
		return herdr.PaneRun(paneID, "hwf", "run", a.Workflow)
	}

	args := a.AgentArgs
	if a.Model != "" {
		args = append([]string{"--model", a.Model}, args...)
	}
	if a.MCPConfig != "" {
		args = append([]string{"--mcp-config", a.MCPConfig}, args...)
	}
	// Herdr requires agent names to be lowercase, 1-32 chars, [a-z0-9-_].
	if err := herdr.AgentStart(agentName(a.Name), a.Agent, paneID, args); err != nil {
		return fmt.Errorf("start %s agent: %w", a.Agent, err)
	}
	if err := submit(paneID, a.Prompt); err != nil {
		return err
	}
	if err := herdr.AgentWait(paneID, timeout); err != nil {
		return fmt.Errorf("waiting for the agent: %w", err)
	}
	return nil
}

// submit gets the prompt in front of the agent and confirms it started working.
//
// A freshly started agent is reported ready before it can actually accept
// input — it may still be connecting MCP servers, especially right after the
// machine wakes. herdr then reports agent_prompt_stalled even though the text
// reached the composer, so each step here verifies the status rather than
// trusting the previous call's verdict.
func submit(paneID, prompt string) error {
	err := herdr.AgentSubmit(paneID, prompt)
	if err == nil {
		return nil
	}
	if !herdr.HasCode(err, herdr.CodeStalled) {
		return fmt.Errorf("prompt: %w", err)
	}

	// The text is probably in the composer, just not submitted. Give the agent
	// a moment, then press Enter for it, then fall back to typing it again.
	if working(paneID, 20*time.Second) {
		return nil
	}
	log.Printf("prompt stalled on %s, submitting the pending composer", paneID)
	if err := herdr.AgentSubmitPending(paneID); err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	if working(paneID, 20*time.Second) {
		return nil
	}

	log.Printf("still idle on %s, retyping the prompt", paneID)
	if err := herdr.AgentSubmit(paneID, prompt); err != nil && !herdr.HasCode(err, herdr.CodeStalled) {
		return fmt.Errorf("prompt: %w", err)
	}
	if working(paneID, 30*time.Second) {
		return nil
	}
	return fmt.Errorf("prompt: the agent never started working; it may still be initialising")
}

// working polls until the agent leaves idle, or the window closes.
func working(paneID string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		status, err := herdr.AgentStatus(paneID)
		if err == nil && status != "idle" && status != "unknown" {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Second)
	}
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

// agentName fits an automation name into Herdr's agent-name rules: lowercase,
// [a-z0-9-_], at most 32 characters.
func agentName(name string) string {
	s := slug(name)
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-")
	}
	return s
}

func runID(name string) string {
	return fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
}

func record(id, name, trigger string, st history.Status, wsID, paneID, errMsg string) {
	err := history.Append(history.Record{
		RunID: id, Automation: name, Trigger: trigger, Status: st, At: time.Now(),
		WorkspaceID: wsID, PaneID: paneID, Error: errMsg,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}
