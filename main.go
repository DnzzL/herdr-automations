// herdr-automations — the trigger layer for Herdr agents: cron-scheduled
// prompts (or herdr-workflows delegations) launched in fresh worktrees.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/cleanup"
	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/daemon"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/pane"
	"github.com/DnzzL/herdr-automations/internal/runner"
	"github.com/DnzzL/herdr-automations/internal/schedule"
	"github.com/DnzzL/herdr-automations/internal/shim"
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
  herdr-automations pause <name>     Stop scheduling an automation
  herdr-automations resume <name>    Resume a paused automation
  herdr-automations history [name]   Show recent runs
  herdr-automations cleanup          Remove run worktrees whose work already landed
  herdr-automations pane             Interactive board (used by the Herdr pane)
  herdr-automations install-cli [dir] Put this command on your PATH (default ~/.local/bin)
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
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			// Not a failure: one daemon serves every session (ADR 0010).
			fmt.Println("daemon not started:", err)
			return
		}
	case "add":
		err = wizard.Run()
	case "list":
		err = list()
	case "cleanup":
		err = cleanupCmd(os.Args[2:])
	case "run":
		err = runCmd(os.Args[2:])
	case "pause":
		err = pauseCmd(os.Args[2:], true)
	case "resume":
		err = pauseCmd(os.Args[2:], false)
	case "history":
		name := ""
		if len(os.Args) > 2 {
			name = os.Args[2]
		}
		err = showHistory(name)
	case "pane":
		err = pane.Run()
	case "install-cli":
		dir := ""
		if len(os.Args) > 2 {
			dir = os.Args[2]
		}
		err = shim.Install(dir)
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
	if len(cfg.Automations) == 0 && len(cfg.Invalid) == 0 {
		fmt.Printf("No automations. Create one with `herdr-automations add` or edit %s\n", config.Path())
		return nil
	}
	if len(cfg.Automations) == 0 {
		printDiagnostics(cfg)
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
	printDiagnostics(cfg)
	printStoppedSessions(cfg)
	printCollisions(cfg)
	printWorktrees(cfg)
	return nil
}

// printDiagnostics names the entries that did not load and the line to open.
// They are not scheduled, so nothing above mentions them — and an automation
// missing from a list is exactly the thing you do not notice.
func printDiagnostics(cfg *config.Config) {
	if len(cfg.Invalid) == 0 {
		return
	}
	subject := fmt.Sprintf("%d entries", len(cfg.Invalid))
	if len(cfg.Invalid) == 1 {
		subject = "1 entry"
	}
	fmt.Printf("\n%s did not load and will not run:\n", subject)
	for _, d := range cfg.Invalid {
		fmt.Printf("  %s\n", d)
	}
}

// printStoppedSessions names the automations aimed at a Herdr session that is
// not running. Their runs will fail, and the plugin never starts a session —
// so this is the moment to find out, not 09:00.
func printStoppedSessions(cfg *config.Config) {
	running, err := herdr.Client{}.RunningSessions()
	if err != nil {
		return
	}
	up := map[string]bool{}
	for _, s := range running {
		up[s] = true
	}
	for _, a := range cfg.Automations {
		if a.Session != "" && !a.Disabled && !up[a.Session] {
			fmt.Printf("\n%s runs in session %q, which is not running — start it, or its runs will fail\n",
				a.Name, a.Session)
		}
	}
}

// printWorktrees says what state the run worktrees are in, and only mentions
// the command when there is something for it to do. It counts and tells;
// removing them stays an explicit act.
func printWorktrees(cfg *config.Config) {
	plan, err := cleanup.Scan(cfg)
	if err != nil || plan.Empty() {
		return
	}
	line := "\n" + plan.Summary()
	if len(plan.Removable) > 0 {
		line += " — herdr-automations cleanup"
	}
	fmt.Println(line)
}

// printCollisions surfaces schedules that come due together. The scheduler
// runs them all — this is a report, not a warning about something the plugin
// is about to do differently.
func printCollisions(cfg *config.Config) {
	clashes := schedule.Collisions(cfg, time.Now())
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
	plan, err := cleanup.Scan(cfg)
	if err != nil {
		return err
	}
	if plan.Empty() {
		fmt.Println("No run worktrees.")
		return nil
	}

	for _, c := range plan.All() {
		mark := "keep"
		if c.Verdict == cleanup.Removable {
			mark = "remove"
		}
		fmt.Printf("%-7s %-48s %s\n", mark, c.Branch, c.Verdict)
	}
	if len(plan.Removable) == 0 {
		fmt.Println("\n" + plan.Summary())
		return nil
	}

	if dryRun {
		fmt.Printf("\n%d would be removed. Drop --dry-run to do it.\n", len(plan.Removable))
		return nil
	}
	if !assumeYes {
		fmt.Printf("\nRemove %d worktree(s) and their branches? [y/N] ", len(plan.Removable))
		var answer string
		if _, err := fmt.Scanln(&answer); err != nil || !strings.EqualFold(answer, "y") {
			fmt.Println("Nothing removed.")
			return nil
		}
	}
	if _, err := plan.Apply(func(c cleanup.Candidate) { fmt.Println("removed", c.Branch) }); err != nil {
		return fmt.Errorf("kept: %w", err)
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
		return runner.Default().Run(cfg.Automations[n-1], history.TriggerManual)
	}
	a := cfg.Find(args[0])
	if a == nil {
		if d := cfg.Diagnostic(args[0]); d != nil {
			return fmt.Errorf("%s did not load: %s", args[0], d)
		}
		return fmt.Errorf("no automation named %q", args[0])
	}
	return runner.Default().Run(*a, history.TriggerManual)
}

// pauseCmd toggles Disabled on a single automation and persists it. It shares
// the config's own guardrail: Save refuses while any entry in the file failed
// to load, so a broken sibling entry blocks this too.
func pauseCmd(args []string, disabled bool) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: herdr-automations %s <name>", map[bool]string{true: "pause", false: "resume"}[disabled])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	a := cfg.Find(args[0])
	if a == nil {
		if d := cfg.Diagnostic(args[0]); d != nil {
			return fmt.Errorf("%s did not load: %s", args[0], d)
		}
		return fmt.Errorf("no automation named %q", args[0])
	}
	a.Disabled = disabled
	if err := config.Save(cfg); err != nil {
		return err
	}
	verb := "Resumed"
	if disabled {
		verb = "Paused"
	}
	fmt.Printf("%s %s\n", verb, a.Name)
	return nil
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
