// herdr-automations — the trigger layer for Herdr agents: cron-scheduled
// prompts (or herdr-workflows delegations) launched in fresh worktrees.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/cleanup"
	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/daemon"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/pane"
	"github.com/DnzzL/herdr-automations/internal/runner"
	"github.com/DnzzL/herdr-automations/internal/skill"
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
  herdr-automations cleanup          Remove run worktrees whose work already landed
  herdr-automations pane             Interactive board (used by the Herdr pane)
  herdr-automations install-skill    Teach your coding agent to write automations
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
	case "cleanup":
		err = cleanupCmd(os.Args[2:])
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
	case "install-skill":
		target := ""
		if len(os.Args) > 2 {
			target = os.Args[2]
		}
		err = skill.Install(target)
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
	fmt.Printf("%-24s %-16s %-9s %-8s %-13s %s\n",
		"NAME", "CRON", "WORKSPACE", "AGENT", "MODEL", "LAST RUN")
	for _, a := range cfg.Automations {
		last := "never"
		if r, _ := history.LastRun(a.Name); r != nil {
			last = fmt.Sprintf("%s (%s)", r.Status, r.At.Format("02 Jan 15:04"))
		}
		name := a.Name
		if a.Disabled {
			name += " (disabled)"
		}
		// An unset model is not blank space — it is a decision left to the
		// agent, and worth seeing next to the ones you made yourself.
		model := a.Model
		if model == "" {
			model = "agent default"
		}
		fmt.Printf("%-24s %-16s %-9s %-8s %-13s %s\n",
			name, a.Cron, a.Workspace, a.Agent, model, last)
	}
	printCollisions(cfg)
	printWorktreeCount(cfg)
	return nil
}

// printWorktreeCount says how many run worktrees are lying around and how many
// still have a workspace open — the ones nobody has read yet. It counts and
// tells; removing them stays an explicit command.
func printWorktreeCount(cfg *config.Config) {
	candidates, err := cleanup.Scan(cfg)
	if err != nil || len(candidates) == 0 {
		return
	}
	unread, removable := 0, 0
	for _, c := range candidates {
		switch {
		case c.Verdict == cleanup.KeptOpen:
			unread++
		case c.Removable():
			removable++
		}
	}
	line := fmt.Sprintf("\n%d run worktrees, %d still open", len(candidates), unread)
	if removable > 0 {
		line += fmt.Sprintf(", %d merged — herdr-automations cleanup", removable)
	}
	fmt.Println(line)
}

// printCollisions surfaces schedules that come due together. The scheduler
// runs them all — this is a report, not a warning about something the plugin
// is about to do differently.
func printCollisions(cfg *config.Config) {
	clashes := cfg.Collisions()
	if len(clashes) == 0 {
		return
	}
	fmt.Println("\nRunning together (Herdr starts them all in parallel):")
	for _, c := range clashes {
		fmt.Printf("  %s  %s\n", c.At.Format("Mon 02 Jan 15:04"), strings.Join(c.Names, ", "))
	}
}

// cleanupCmd reports what every run worktree is worth keeping for, then asks
// once before removing the ones whose work already landed. It is never
// scheduled: an open workspace is how you know a run still wants you.
func cleanupCmd(args []string) error {
	dryRun, assumeYes := false, false
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--yes", "-y":
			assumeYes = true
		default:
			return fmt.Errorf("unknown flag %q; cleanup takes --dry-run and --yes", a)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	candidates, err := cleanup.Scan(cfg)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Println("No run worktrees.")
		return nil
	}

	var removable []cleanup.Candidate
	for _, c := range candidates {
		mark := "keep"
		if c.Removable() {
			mark = "remove"
			removable = append(removable, c)
		}
		fmt.Printf("%-7s %-48s %s\n", mark, c.Branch, c.Verdict)
	}
	if len(removable) == 0 {
		fmt.Println("\nNothing to remove.")
		return nil
	}

	if dryRun {
		fmt.Printf("\n%d would be removed. Drop --dry-run to do it.\n", len(removable))
		return nil
	}
	if !assumeYes {
		fmt.Printf("\nRemove %d worktree(s) and their branches? [y/N] ", len(removable))
		var answer string
		if _, err := fmt.Scanln(&answer); err != nil || !strings.EqualFold(answer, "y") {
			fmt.Println("Nothing removed.")
			return nil
		}
	}
	for _, c := range removable {
		if err := cleanup.Remove(c); err != nil {
			fmt.Fprintln(os.Stderr, "kept:", err)
			continue
		}
		fmt.Println("removed", c.Branch)
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
