---
name: creating-automations
description: Create or edit a scheduled Herdr automation (cron + prompt for a coding agent). Use when the user asks to schedule an agent task, run a prompt every day/week, automate a recurring chore (triage, dependency bumps, report generation), or edit existing automations.
---

# Creating Herdr automations

Automations are plain YAML entries in one file. Your job: translate the
user's intent into a valid entry, write it, and tell them when it will run.

## Config file location

Ask the shell, don't guess:

```bash
herdr plugin config-dir dnzzl.automations   # → <dir>/automations.yaml
```

Fallback when not running under Herdr: `~/.config/herdr-automations/automations.yaml`.

## Entry format

```yaml
automations:
  - name: issue-triage            # unique, kebab-case
    cron: "0 9 * * 1-5"           # 5-field crontab, or @daily / @hourly
    repo: ~/Projects/myapp        # repo the agent works on
    workspace: worktree           # worktree (default, fresh branch per run) | root
    agent: claude                 # any kind herdr supports: claude, codex, opencode…
    prompt: |
      Triage new GitHub issues: label them, close duplicates,
      draft replies for the ones needing more info.
    mcp_config: ~/.config/mcp/github.json   # optional, passed as --mcp-config
    timeout_minutes: 60           # optional, default 60
    # disabled: true              # keep the entry but stop scheduling it
```

Instead of `prompt`, an entry may delegate to a herdr-workflows workflow:

```yaml
    workflow: nightly-deps        # runs `hwf run nightly-deps` in the pane
```

Exactly one of `prompt` / `workflow` is required.

## Rules

- Read the existing file first; append, never clobber other entries.
- Names must be unique. Cron is validated on load — a bad entry blocks the
  whole file, so double-check the expression.
- Times are local to the machine running the Herdr server.
- The daemon reloads the file within 30 seconds; no restart needed.

## Verify

```bash
herdr-automations list          # entry present, cron parsed
herdr-automations run <name>    # optional immediate test run
herdr-automations history <name>
```

Tell the user the next scheduled run time and how to test immediately.
