# Changelog

What changed for someone using the plugin. Dates are release dates.

## v0.4.0 — 2026-08-20

- `herdr-automations cleanup` removes the worktrees left by runs whose work
  already landed — merged branches, and runs that produced no commit. Runs with
  a workspace still open, or with commits that exist nowhere else, are kept and
  told why. It asks before removing anything and never runs on a schedule.
- A `workflow:` automation now reports what actually happened. It used to record
  `done` as soon as the command was typed into the pane — including when
  herdr-workflows was not installed at all. The runner now refuses to start when
  `hwf` is missing, waits for the workflow to finish, and fails the run on a
  non-zero exit.
- The board cleans up too: `c` scans, says how many worktrees are finished with,
  and waits for `y/n` before removing anything.
- `list` says how many run worktrees exist and how many still have a workspace
  open: the ones nobody has read.

## v0.3.0 — 2026-08-19

- `model:` per automation, passed to the agent as `--model`. An agent kind whose
  executable has no such flag is rejected when the file loads, not at 3am.
- `list` shows the model of every automation, and says `agent default` where none
  was set, so an unattended run on an unintended model is visible.
- Schedules that come due at the same minute are reported: the wizard warns while
  the cron is still being chosen, `list` reports the week ahead. Nothing is
  serialised — Herdr runs them all, as the cron says.
- The wizard asks for a model every time.

## v0.2.1 — 2026-08-17

- A run no longer fails when the agent was slow to accept the prompt: the runner
  checks whether the agent actually started working before giving up. Agents
  loading MCP servers on a freshly woken machine were the common case.

## v0.2.0 — 2026-08-17

- Occurrences are computed off the wall clock. A run due while the laptop slept
  now fires on wake within `catch_up_minutes` instead of vanishing; anything
  later is recorded as `missed` rather than silently dropped.
- `install-skill` symlinks the bundled skill into `~/.claude/skills`, which is
  the only place agents look.
- Agent names are slugified like branch names, so an automation whose name has
  spaces or punctuation no longer fails to start.

## v0.1.1 — 2026-08-16

- Prebuilt, checksum-verified binaries for macOS and Linux (arm64/amd64):
  installing no longer needs a Go toolchain.
