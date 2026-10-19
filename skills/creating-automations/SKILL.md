---
name: herdr-automations
description: Schedule a recurring agent task with herdr-automations (cron + prompt), or edit/inspect existing ones. Use whenever the user wants something to run on a schedule — "every morning", "each Monday", "nightly", "every week", "on a cron", "recurring", "automate this", "schedule an agent", "run this while I sleep" — and for triage/dependency-bump/report/standup chores that repeat. Also use to list, disable, or debug scheduled automations and their run history.
---

# Scheduling Herdr automations

An automation is a cron schedule plus a prompt. At the scheduled time the
daemon spawns the agent in a fresh git worktree of a repo and submits the
prompt. Your job: translate the user's intent into a valid YAML entry, write
it, and tell them when it will next run.

## Where the file lives

Ask the CLI rather than guessing:

```bash
herdr plugin config-dir dnzzl.automations    # → <dir>/automations.yaml
```

Fallback when Herdr isn't installed: `~/.config/herdr-automations/automations.yaml`.

## Entry format

```yaml
automations:
  - name: issue-triage            # unique
    cron: "0 9 * * 1-5"           # 5-field crontab, or @daily / @hourly / @weekly
    repo: ~/Projects/myapp        # repo the agent works in
    workspace: worktree           # worktree (default, fresh branch per run) | root | existing
    # workspace_id: w7           # required by, and only by, workspace: existing
    # session: automations       # optional Herdr session every run happens in
    agent: claude                 # any kind `herdr agent start` supports
    model: sonnet                 # optional → passed as --model
    prompt: |
      Triage new GitHub issues: label them, close duplicates,
      draft replies for the ones needing more info.
    mcp_config: ~/.config/mcp/github.json   # optional → passed as --mcp-config
    agent_args: ["--verbose"]               # optional, verbatim agent flags
    timeout_minutes: 60           # optional, default 60
    catch_up_minutes: 120         # optional: how late a sleep-delayed run may start; -1 never
    # disabled: true              # keep the entry, stop scheduling it
```

Instead of `prompt`, an entry may delegate to a herdr-workflows workflow:

```yaml
    workflow: nightly-deps        # runs `hwf run nightly-deps` in the pane
```

Exactly one of `prompt` / `workflow` is required.

## Rules

- **Read the existing file first and append.** Never rewrite entries you didn't
  come to change.
- Names must be unique. Spaces are allowed; the branch name is slugified.
- Every entry is validated on load. A bad one is skipped with a diagnostic and
  the rest still run, so check `herdr-automations list` after writing: it names
  what did not load, with the line to fix.
- Times are local to the machine running the Herdr server.
- **Always set `model`** on kinds that accept it (`claude`, `codex`, `cursor`,
  `gemini`, `opencode`). Omitting it leaves an unattended run on whatever the
  agent defaults to. Match the model to the job: a summary or a triage pass is
  not a reason to reach for the most expensive one. Any other kind must use
  `agent_args` — a `model` on those fails to load.
- Automations due at the same minute **all start**: Herdr is a multi-agent
  runtime and nothing here serialises. `herdr-automations list` reports the
  overlaps; stagger the crons if running them at once is not what you want.
- The daemon reloads within 30 seconds; no restart needed.
- Occurrences missed while the machine slept run on wake within `catch_up_minutes`
  (default 120), otherwise they appear as `missed` in the history.
- `workspace: worktree` means the agent never touches the user's working copy.
  Only choose `root` when the task must see uncommitted local state.
- `workspace: existing` puts each run in a fresh tab of one workspace the user
  already created, instead of a new workspace per run. It needs a `workspace_id`
  from `herdr workspace list`, and like `root` it works directly in `repo` with
  no worktree. Propose it for a frequent schedule — hourly and up — where a
  workspace per run would bury the sidebar. Never invent the ID: if the user has
  not given one, ask, or write the entry with `worktree`.
- `session:` names the Herdr session runs happen in, whether triggered by cron
  or by hand. Add it only when the user asks for runs to land in a specific or
  dedicated session; `herdr session list` shows the names. The session must be
  running: the plugin never starts one, and a run aimed at a stopped one fails.
- A failed run, a missed run, or a broken entry raises a Herdr toast; a
  successful or skipped run stays quiet on purpose. There is no event, push, or
  PR trigger and none is planned — if the user asks for one, propose a cron
  automation that polls instead (`gh pr list`, `git fetch`, …).

## Choosing the schedule

Ask only when it's genuinely ambiguous. Otherwise translate directly:
"every morning" → `0 9 * * *`, "weekdays at 9" → `0 9 * * 1-5`,
"each Monday at 9am" → `0 9 * * 1`, "nightly" → `@daily`,
"every week" → `0 9 * * 1`.

## Verify

```bash
herdr-automations list             # entry present, cron parsed
herdr-automations run <name>       # optional immediate test run
herdr-automations history <name>   # what happened
```

Report the next scheduled run time and how to trigger it immediately.
