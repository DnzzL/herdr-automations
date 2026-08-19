// Package wizard implements `herdr-automations add`: a plain stdin Q&A that
// appends a validated entry to automations.yaml and previews the next runs.
package wizard

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
)

func Run() error {
	in := bufio.NewReader(os.Stdin)
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	name := ask(in, "Name (kebab-case)", "")
	if name == "" {
		return fmt.Errorf("a name is required")
	}
	if cfg.Find(name) != nil {
		return fmt.Errorf("automation %q already exists", name)
	}

	var cronExpr string
	for {
		cronExpr = ask(in, "Cron (5 fields or @daily/@hourly)", "0 9 * * 1-5")
		sched, err := config.CronParser.Parse(cronExpr)
		if err != nil {
			fmt.Printf("  invalid: %v\n", err)
			continue
		}
		next := time.Now()
		fmt.Println("  next runs:")
		for i := 0; i < 3; i++ {
			next = sched.Next(next)
			fmt.Printf("    %s\n", next.Format("Mon 02 Jan 15:04"))
		}
		// Herdr runs every due automation; the scheduler never holds one back.
		// Saying so here is the only moment the cron is still up for debate.
		if clash := cfg.CollidesWith(cronExpr); len(clash) > 0 {
			fmt.Printf("  heads up: %s already due at the same time — they will all run at once\n",
				strings.Join(clash, ", "))
		}
		break
	}

	cwd, _ := os.Getwd()
	repo := ask(in, "Repo path", cwd)
	workspace := ask(in, "Workspace [worktree|root]", "worktree")
	agent := ask(in, "Agent kind", "claude")
	// Asked every time rather than defaulted in the file: an automation that
	// does not say which model it uses is one that quietly picks whatever the
	// agent picks, which is how a nightly chore ends up on a flagship model.
	model := ""
	if config.KindAcceptsModel(agent) {
		model = ask(in, "Model (e.g. sonnet, opus — empty for the agent's own default)", "")
	}

	fmt.Print("Prompt (single line; leave empty to delegate to a herdr-workflows workflow):\n> ")
	prompt, _ := in.ReadString('\n')
	prompt = strings.TrimSpace(prompt)
	workflow := ""
	if prompt == "" {
		workflow = ask(in, "Workflow name (hwf run <name>)", "")
		if workflow == "" {
			return fmt.Errorf("either a prompt or a workflow is required")
		}
	}

	mcp := ask(in, "MCP config JSON path (optional)", "")
	timeoutStr := ask(in, "Timeout minutes", "60")
	timeout, err := strconv.Atoi(timeoutStr)
	if err != nil || timeout <= 0 {
		timeout = 60
	}

	cfg.Automations = append(cfg.Automations, config.Automation{
		Name: name, Cron: cronExpr, Repo: repo,
		Workspace: config.Workspace(workspace), Agent: agent, Model: model,
		Prompt: prompt, Workflow: workflow,
		MCPConfig: mcp, TimeoutMinutes: timeout,
	})
	if err := config.Save(cfg); err != nil {
		return err
	}
	// Re-load to run full validation on what we just wrote.
	if _, err := config.Load(); err != nil {
		return fmt.Errorf("saved, but validation failed — fix %s: %w", config.Path(), err)
	}
	fmt.Printf("\nSaved %q to %s\nThe daemon picks it up within 30s. Test it now with: herdr-automations run %s\n",
		name, config.Path(), name)
	return nil
}

func ask(in *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}
