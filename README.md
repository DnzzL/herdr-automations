# herdr-automations

**Cron for your coding agents.** Schedule a prompt — Herdr wakes an agent in a fresh worktree and runs it. While you sleep.

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go)](go.mod)
[![Herdr](https://img.shields.io/badge/herdr-%E2%89%A50.8-8A2BE2)](https://herdr.dev)

```yaml
automations:
  - name: issue-triage
    cron: "0 9 * * 1-5"
    repo: ~/Projects/myapp
    prompt: |
      Triage new GitHub issues: label them, close duplicates,
      draft replies for the ones needing more info.
```

![The Automations board: schedules, last run status, and jumping into the config](docs/board.gif)

That's the whole feature. Every weekday at 9:00, a Claude (or Codex, or opencode…) agent spawns in a fresh git worktree of `myapp`, gets that prompt, and works until it's done. You arrive to triaged issues and a run log.

## Why

[Herdr](https://herdr.dev) keeps your agents alive when you close the laptop — but *you* still have to start them. Recurring chores (issue triage, dependency bumps, nightly test-flake hunts, changelog drafts) deserve better than you retyping the same prompt every morning.

`herdr-automations` is the **trigger layer** the ecosystem was missing:

|  | [herdr-workflows](https://github.com/aorumbayev/herdr-workflows) | **herdr-automations** |
|---|---|---|
| Answers | *"what steps to run?"* | *"**when** to run them?"* |
| Model | multi-step YAML workflows, run on demand | prompt + cron, fired on schedule |
| Together | `workflow: nightly-deps` in an automation runs a herdr-workflows workflow on a schedule | |

## Highlights

- **One YAML entry per automation** — no DSL, no UI required, versionable
- **Fresh worktree per run** (branch `auto/<name>-<timestamp>`), or the repo root — your working copy is never touched
- **Agent-agnostic** — anything `herdr agent start` supports: `claude`, `codex`, `opencode`, `gemini`, `cursor`, …
- **MCP attach** — `mcp_config: path.json` hands the agent its MCP servers (GitHub, Slack, your DB…)
- **Overlap guard** — a tick that fires while the previous run is still working is *skipped*, never queued into a pile-up
- **Full run history** — append-only JSONL: `scheduled → running → done | failed | skipped`, with workspace and pane IDs to jump back into
- **Live board** — an overlay pane inside Herdr: next run, last status, `r` to run now, `enter` to jump straight into the workspace a run created
- **Agents can self-schedule** — a bundled [skill](skills/creating-automations/SKILL.md) teaches Claude Code the format: say *"bump my deps every night"* and the agent writes the entry itself
- **One static Go binary** — no Node, no Python, no runtime on the machine

## Quick start

```bash
herdr plugin install DnzzL/herdr-automations   # prebuilt binary, no toolchain needed
herdr-automations add                          # wizard: validates cron, previews next 3 runs
```

Prebuilt for macOS and Linux (arm64/amd64), checksum-verified at install; anything
else builds from source and needs Go.

**Three ways to add an automation**, all writing the same file:

1. `herdr-automations add` — the wizard: validates the cron and previews the next three runs
2. **Edit the YAML** — `herdr plugin config-dir dnzzl.automations` → `automations.yaml`
   (or press `e` on the board to open it at the right line). Full reference in
   [`automations.example.yaml`](automations.example.yaml)
3. **Ask your agent** — *"schedule a dependency bump on myapp every night"*. The bundled
   [skill](skills/creating-automations/SKILL.md) teaches Claude Code the format and it writes the entry

The daemon picks up edits within 30 seconds, no restart.

```bash
herdr-automations list             # schedule + last run per automation
herdr-automations run issue-triage # trigger now, don't wait for cron
herdr-automations history         # what ran, when, how it ended
```

### The board, inside Herdr

A live overlay listing every automation with its next run and last status:

```bash
herdr plugin pane open --plugin dnzzl.automations --entrypoint board --placement overlay
```

| Key | Does |
|---|---|
| `enter` | **Jump to the last run** — focuses the workspace that automation spawned, so you land right in the agent's terminal |
| `r` | Run the selected automation now |
| `e` | Open `automations.yaml` in `$EDITOR`, **cursor on that automation's line** |
| `j` / `k` | Move · `q` closes |

Bind it to a chord so you never type that command again — in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+a"
type = "shell"
command = "herdr plugin pane open --plugin dnzzl.automations --entrypoint board --placement overlay"
```

The plugin also registers an action (`herdr plugin action invoke run-now --plugin dnzzl.automations`)
that prompts for which automation to fire.

## How a run works

```mermaid
flowchart LR
    A[cron tick] --> B{already<br>running?}
    B -- yes --> S[skip + log]
    B -- no --> C[herdr worktree create<br>branch auto/name-ts]
    C --> D[herdr agent start<br>--kind claude -- --mcp-config …]
    D --> E[herdr agent prompt<br>--wait --timeout]
    E --> F[history.jsonl<br>done / failed]
```

Everything goes through the `herdr` CLI — the same socket API agents themselves use. No private hooks, nothing to break on Herdr upgrades.

## Full entry reference

```yaml
automations:
  - name: issue-triage            # unique, kebab-case
    cron: "0 9 * * 1-5"           # 5-field crontab, or @daily / @hourly / @weekly
    repo: ~/Projects/myapp
    workspace: worktree           # worktree (default) | root
    agent: claude                 # any `herdr agent start --kind`
    prompt: "…"                   # OR workflow: <name>  (delegates to hwf run)
    mcp_config: ~/.config/mcp/github.json   # optional → --mcp-config
    agent_args: ["--model", "opus"]         # optional, verbatim agent flags
    timeout_minutes: 60           # optional bound on the run
    disabled: true                # optional: keep it, don't schedule it
```

## FAQ

**Does my machine need to be awake?** The Herdr *server* does. Herdr's whole point is running headless — a home server, a VPS, a Mac that doesn't sleep.

**What happens to the worktrees?** They accumulate as reviewable workspaces — each run is a branch you can inspect, merge, or `herdr worktree remove`. Auto-cleanup of merged runs is on the roadmap.

**Event triggers (on push, on PR, on `worktree.created`)?** Planned — Herdr's plugin manifest already supports `[[events]]`; cron came first because it's 90% of the value.

## Development

```bash
go build -o bin/herdr-automations . && go test ./...
herdr plugin link .        # use your checkout as the installed plugin
```

PRs welcome — especially new event triggers, run cleanup policies, and agent kinds tested in the wild.

## License

[MIT](LICENSE)
