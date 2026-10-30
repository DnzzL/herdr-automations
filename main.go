// herdr-automations — the trigger layer for Herdr agents: cron-scheduled
// prompts (or herdr-workflows delegations) launched in fresh worktrees.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DnzzL/herdr-automations/internal/api"
	"github.com/DnzzL/herdr-automations/internal/cleanup"
	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/daemon"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/pane"
	"github.com/DnzzL/herdr-automations/internal/runner"
	"github.com/DnzzL/herdr-automations/internal/schedule"
	"github.com/DnzzL/herdr-automations/internal/service"
	"github.com/DnzzL/herdr-automations/internal/shim"
	"github.com/DnzzL/herdr-automations/internal/wizard"
)

// Version is stamped by the release build; "dev" for local builds.
var Version = "dev"

const usage = `herdr-automations — cron for your Herdr agents

Usage:
  herdr-automations daemon [--standby]
                                     Run the scheduler (--standby: wait while another one runs)
  herdr-automations service install|uninstall|status
                                     Run the scheduler as a login service (launchd, systemd)
  herdr-automations add              Interactive wizard: create an automation
  herdr-automations list [--json]    List automations with schedule and last run
  herdr-automations run <name> [--bg]
                                     Trigger an automation now (--bg: detach, return at once)
  herdr-automations pause <name|--all>
                                     Stop scheduling one automation, or all of them
  herdr-automations resume <name|--all>
                                     Resume what pause stopped
  herdr-automations history [name] [--json]
                                     Show recent runs
  herdr-automations put [--original <name>] [--dry-run] < automation.json
                                     Write one automation (the app panel's form)
  herdr-automations rm <name>        Delete one automation
  herdr-automations seen <run-id|--all>
                                     Mark a run, or every finished run, as opened (the app panel's inbox)
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
	args, asJSON := withoutFlag(os.Args[2:], "--json")
	switch os.Args[1] {
	case "daemon":
		_, standby := withoutFlag(args, "--standby")
		err = daemon.Run(standby)
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			// Not a failure: one daemon serves every session (ADR 0010).
			fmt.Println("daemon not started:", err)
			return
		}
	case "service":
		err = serviceCmd(args)
	case "add":
		err = wizard.Run()
	case "list", "ls":
		if asJSON {
			err = printJSON(api.List())
		} else {
			err = list()
		}
	case "cleanup":
		err = cleanupCmd(args)
	case "run":
		if rest, bg := withoutFlag(args, "--bg"); bg {
			err = runDetached(rest)
		} else {
			err = runCmd(args)
		}
	case "pause":
		err = pauseCmd(args, true)
	case "resume":
		err = pauseCmd(args, false)
	case "history":
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		if asJSON {
			err = printJSON(api.History(name, 200))
		} else {
			err = showHistory(name)
		}
	case "put":
		err = putCmd(args)
	case "rm":
		if len(args) != 1 {
			err = fmt.Errorf("usage: herdr-automations rm <name>")
		} else {
			err = config.Remove(args[0])
		}
	case "seen":
		if len(args) != 1 {
			err = fmt.Errorf("usage: herdr-automations seen <run-id|--all>")
		} else if args[0] == "--all" {
			err = history.MarkAllSeen()
		} else {
			err = history.MarkSeen(args[0])
		}
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

// pauseCmd toggles Disabled on one automation, or the top-level paused flag
// with --all. It edits that one key in place, so comments and entries that
// failed to load are left as they are.
func pauseCmd(args []string, disabled bool) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: herdr-automations %s <name|--all>", map[bool]string{true: "pause", false: "resume"}[disabled])
	}
	name, what := args[0], args[0]
	if name == "--all" {
		name, what = "", "all automations"
	}
	if err := config.SetPaused(name, disabled); err != nil {
		return err
	}
	verb := "Resumed"
	if disabled {
		verb = "Paused"
	}
	fmt.Printf("%s %s\n", verb, what)
	return nil
}

// putCmd writes one automation read as JSON on stdin and prints what Put
// found. A refused entry is an answer, not an error: the form shows Problem.
func putCmd(args []string) error {
	args, dryRun := withoutFlag(args, "--dry-run")
	original := ""
	if len(args) == 2 && args[0] == "--original" {
		original = args[1]
	} else if len(args) != 0 {
		return fmt.Errorf("usage: herdr-automations put [--original <name>] [--dry-run] < automation.json")
	}
	var in api.Automation
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return fmt.Errorf("read automation JSON: %w", err)
	}
	a := config.Automation{
		Name: in.Name, Cron: in.Cron, Repo: in.Repo, Workspace: in.Workspace, WorkspaceID: in.WorkspaceID,
		Agent: in.Agent, Model: in.Model, Template: in.Template, Prompt: in.Prompt, Workflow: in.Workflow,
		MCPConfig: in.MCPConfig, AgentArgs: in.AgentArgs, TimeoutMinutes: in.TimeoutMinutes,
		CatchUpMinutes: in.CatchUpMinutes, Disabled: in.Disabled,
	}
	return printJSON(config.Put(original, a, dryRun))
}

// runDetached starts `run <name>` in its own session and returns at once. The
// panel cannot wait an hour on exec, and the run must outlive the app.
func runDetached(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: herdr-automations run <name> --bg")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(config.StateDir(), "manual-runs.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "run", args[0])
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

func withoutFlag(args []string, flag string) ([]string, bool) {
	out, found := make([]string, 0, len(args)), false
	for _, a := range args {
		if a == flag {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

func printJSON(v any, err error) error {
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
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

// serviceCmd manages the login service that keeps the scheduler running
// without Herdr's startup hook, for the desktop app.
func serviceCmd(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: herdr-automations service install|uninstall|status")
	}
	switch args[0] {
	case "install":
		env := map[string]string{
			"PATH":                    os.Getenv("PATH"),
			"HERDR_PLUGIN_CONFIG_DIR": config.Dir(),
			"HERDR_PLUGIN_STATE_DIR":  config.StateDir(),
		}
		path, err := service.Install(env, filepath.Join(config.StateDir(), "daemon.log"))
		if err != nil {
			return err
		}
		fmt.Printf("Installed %s\nConfig: %s\n", path, config.Path())
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			return err
		}
		fmt.Println("Uninstalled; automations.yaml and history are left where they are")
	case "status":
		pid, running := daemon.Running()
		fmt.Printf("service installed: %v\nscheduler running: %v", service.Installed(), running)
		if running {
			fmt.Printf(" (pid %d)", pid)
		}
		fmt.Println()
	default:
		return fmt.Errorf("usage: herdr-automations service install|uninstall|status")
	}
	return nil
}
