# ADR 0014 — a gate can skip a run

**Status**: accepted · **Date**: 2026-11-21 · #24

## Context

Some chores only matter when something is true: a PR is waiting, a queue is
not empty. Today the automation starts, provisions a workspace and a worktree,
spends agent quota, and the agent finds nothing to do. Users asked for a cheap
check that runs first and can say "not now".

## Decision

An optional key, `gate:`, an argv list. Before a run provisions anything, the
runner executes it with `cwd` = the automation's `repo`:

- Exit 0: the run goes on.
- Exit 75 (`EX_TEMPFAIL`): the run is recorded `skipped` with the reason
  `gate: nothing to do (exit 75)`, distinct from `previous run still in
  flight`. No workspace, no worktree, no toast.
- Any other exit, a timeout, or a command that cannot start: recorded `failed`
  with the tail of stderr as reason, and toasted like any failed run.

The gate has a fixed 60 s timeout. Its stdout is not passed to the prompt.
`config.Load()` refuses a `gate:` that is present but empty. Scheduled runs and
`run` (Run now included) are both gated.

## Why

An argv list needs no shell, which Windows lacks; whoever wants one writes
`["sh", "-c", "…"]` and owns its quoting. Exit 75 is the existing convention for
"try again later", so ordinary tools can say "skip" without being confused with
a crash. A gate that breaks is a failure, never a skip: a silent skip would
hide a broken check forever.

Running before provisioning is the point: a skipped run costs one process.

## Rejected

- **A shell string** — needs `sh`, and every user inherits shell quoting rules
  the plugin cannot see.
- **A configurable timeout** — a gate is a cheap check; one that needs more
  than 60 s belongs in the prompt. A knob is one more thing to validate.
- **Passing the gate's stdout into the prompt** — turns a precheck into a
  second prompt-building channel and makes the run depend on output nobody
  reviews. Revisit if a real use appears.

## Consequences

- A manual `run` is gated too, so "Run now" can skip. The reason is in the
  history; bypassing for manual runs is a possible follow-up.
- A gate hides nothing from `history`: skipped runs are recorded.
- While the gate runs (up to 60 s) the run holds its in-flight slot but has no
  history entry yet; a second trigger then records "previous run still in flight".
- A manual `run` of a skipped automation returns an error saying so (like the
  in-flight skip), so the board and CLI never report "finished".
- On timeout only the gate's own process is killed; children it spawned (a
  `sh -c` pipeline) may outlive it.
