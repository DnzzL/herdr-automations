# herdr-automations

Cron for your [Herdr](https://herdr.dev) agents. The missing **trigger
layer**: schedule a prompt (`0 9 * * 1-5` → "triage the issues") and the
daemon provisions a fresh worktree, starts the agent and submits the prompt —
while [herdr-workflows](https://github.com/aorumbayev/herdr-workflows) stays
the execution layer you can delegate multi-step runs to.

- **Prompt + cron** — one YAML entry per automation
- **Fresh worktree per run** (or the repo root), agent-agnostic (`claude`,
  `codex`, `opencode`, … anything `herdr agent start` supports)
- **MCP attach** — point `mcp_config` at a servers JSON, forwarded to the agent
- **Overlap guard** — a tick fired while the previous run is still working is
  skipped, not queued
- **Run history** — append-only JSONL, states `scheduled / running / done /
  failed / skipped`
- **Single static Go binary** — no runtime dependency on the user's machine

## Install

```bash
herdr plugin install DnzzL/herdr-automations
```

Requires Go at build time (the plugin builds itself on install) and Herdr ≥ 0.8.

## Create an automation

Three ways, same file (`herdr plugin config-dir dnzzl.automations` →
`automations.yaml`):

1. **Wizard** — `herdr-automations add` (validates the cron, previews the
   next 3 runs)
2. **By hand / by agent** — edit the YAML; the bundled skill
   (`skills/creating-automations/SKILL.md`) teaches Claude Code & friends the
   format, so "schedule a daily dependency bump on myapp" just works
3. **Delegate execution** — set `workflow: <name>` instead of `prompt:` to run
   a herdr-workflows workflow on schedule

See [`automations.example.yaml`](automations.example.yaml) for the full format.

The daemon (started by the plugin's startup hook) reloads the file within
30 seconds — no restart needed.

## Drive it

```bash
herdr-automations list             # schedule + last run per automation
herdr-automations run issue-triage # trigger now
herdr-automations history          # recent runs across automations
```

Inside Herdr: the **Automations** pane (overlay) lists everything live —
`r` runs the selected automation, `q` closes. The *"Automations: run now"*
action does the same from the action picker.

## How a run works

```
cron tick ─▶ overlap guard ─▶ herdr worktree create --branch auto/<name>-<ts>
                              (or workspace create --cwd <repo> for root mode)
                          ─▶ herdr agent start <name> --kind <agent> [-- --mcp-config …]
                          ─▶ herdr agent prompt <pane> <prompt> --wait --timeout <m>
                          ─▶ history.jsonl: done | failed
```

Everything goes through the `herdr` CLI (`HERDR_BIN_PATH`), i.e. the same
socket API agents themselves use.

## Development

```bash
go build -o bin/herdr-automations . && go test ./...
herdr plugin link .   # use the local checkout as the installed plugin
```
