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
	if a.MCPConfig != "" {
		args = append([]string{"--mcp-config", a.MCPConfig}, args...)
	}
	// Herdr requires agent names to be lowercase, 1-32 chars, [a-z0-9-_].
	if err := herdr.AgentStart(agentName(a.Name), a.Agent, paneID, args); err != nil {
		return fmt.Errorf("start %s agent: %w", a.Agent, err)
	}
	if err := herdr.AgentPrompt(paneID, a.Prompt, timeout); err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	return nil
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
