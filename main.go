// herdr-automations — the trigger layer for Herdr agents: cron-scheduled
// prompts (or herdr-workflows delegations) launched in fresh worktrees.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/daemon"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/pane"
	"github.com/DnzzL/herdr-automations/internal/runner"
	"github.com/DnzzL/herdr-automations/internal/wizard"
)

// Version is stamped by the release build; "dev" for local builds.
var Version = "dev"

const usage = `herdr-automations — cron for your Herdr agents

Usage:
  herdr-automations daemon           Run the scheduler (started by the plugin startup hook)
  herdr-automations add              Interactive wizard: create an automation
  herdr-automations list             List automations with schedule and last run
  herdr-automations run <name>       Trigger an automation now
  herdr-automations history [name]   Show recent runs
  herdr-automations pane             Interactive board (used by the Herdr pane)
  herdr-automations version          Print the version

Config:  %s
History: %s
`

func main() {
	if len(os.Args) < 2 {
		fmt.Printf(usage, config.Path(), config.StateDir())
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "daemon":
		err = daemon.Run()
	case "add":
		err = wizard.Run()
	case "list":
		err = list()
	case "run":
		err = runCmd(os.Args[2:])
	case "history":
		name := ""
		if len(os.Args) > 2 {
			name = os.Args[2]
		}
		err = showHistory(name)
	case "pane":
		err = pane.Run()
	case "version", "--version", "-v":
		fmt.Println(Version)
	default:
		fmt.Printf(usage, config.Path(), config.StateDir())
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func list() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Automations) == 0 {
		fmt.Printf("No automations. Create one with `herdr-automations add` or edit %s\n", config.Path())
		return nil
	}
	fmt.Printf("%-24s %-16s %-9s %-8s %s\n", "NAME", "CRON", "WORKSPACE", "AGENT", "LAST RUN")
	for _, a := range cfg.Automations {
		last := "never"
		if r, _ := history.LastRun(a.Name); r != nil {
			last = fmt.Sprintf("%s (%s)", r.Status, r.At.Format("02 Jan 15:04"))
		}
		name := a.Name
		if a.Disabled {
			name += " (disabled)"
		}
		fmt.Printf("%-24s %-16s %-9s %-8s %s\n", name, a.Cron, a.Workspace, a.Agent, last)
	}
	return nil
}

func runCmd(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "--pick" {
		// Herdr action context: no arg — pick via a numbered prompt.
		if len(cfg.Automations) == 0 {
			return fmt.Errorf("no automations configured")
		}
		for i, a := range cfg.Automations {
			fmt.Printf("%d) %s (%s)\n", i+1, a.Name, a.Cron)
		}
		fmt.Print("Run which one? ")
		var n int
		if _, err := fmt.Scanln(&n); err != nil || n < 1 || n > len(cfg.Automations) {
			return fmt.Errorf("invalid selection")
		}
		return runner.Run(cfg.Automations[n-1], "manual")
	}
	a := cfg.Find(args[0])
	if a == nil {
		return fmt.Errorf("no automation named %q", args[0])
	}
	return runner.Run(*a, "manual")
}

func showHistory(name string) error {
	runs, err := history.Runs(name, 30)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("No runs yet.")
		return nil
	}
	fmt.Printf("%-20s %-24s %-9s %-7s %s\n", "AT", "AUTOMATION", "STATUS", "TRIGGER", "DETAIL")
	for _, r := range runs {
		detail := r.Error
		if detail == "" {
			detail = r.WorkspaceID
		}
		fmt.Printf("%-20s %-24s %-9s %-7s %s\n",
			r.At.Format(time.DateTime), r.Automation, r.Status, r.Trigger, detail)
	}
	return nil
}
