// Package runner executes one automation end to end: provision a workspace,
// start the agent, submit the prompt (or delegate to herdr-workflows), and
// record every state transition in the history log.
package runner

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
)

// inFlight guards against overlapping runs of the same automation: if the
// 9:00 run is still working at 10:00, the 10:00 tick is skipped, not queued.
var inFlight sync.Map

// Run executes the automation synchronously. trigger is "cron" or "manual".
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
		branch := fmt.Sprintf("auto/%s-%s", a.Name, time.Now().Format("20060102-1504"))
		workspaceID, err = herdr.WorktreeCreate(a.Repo, branch, label)
	case config.WorkspaceRoot:
		workspaceID, err = herdr.WorkspaceCreate(a.Repo, label)
	}
	if err != nil {
		return "", "", err
	}
	paneID, err = herdr.FirstPane(workspaceID)
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
	if err := herdr.AgentStart(a.Name, a.Agent, paneID, args); err != nil {
		return fmt.Errorf("start %s agent: %w", a.Agent, err)
	}
	if err := herdr.AgentPrompt(paneID, a.Prompt, timeout); err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	return nil
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
