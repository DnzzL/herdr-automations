# ADR 0012 — a successful run may close its workspace

**Status**: accepted · **Date**: 2026-11-04 · amends [ADR 0001](0001-cleanup-is-never-scheduled.md)

## Context

ADR 0001 keeps a run's workspace open as the signal that nobody has read it.
For a chore checked on weekends, or one that runs hourly, that signal is noise:
workspaces pile up, and before the agent-name fallback an unread run made the
next one fail (#13). Users worked around it by telling the agent to run
`herdr workspace close $HERDR_WORKSPACE_ID` at the end of its prompt.

## Decision

An opt-in key, `close_workspace: never | on-success`, default `never`. With
`on-success` the runner records the run `done`, then asks the host to close
the run's workspace:

- TUI: `herdr workspace close <id>`. The worktree and its uncommitted files stay
  (measured 2026-11-04), so `cleanup` keeps its job.
- hc: `hc kill` then `hc rm` on the run's terminal. The app removes the space
  itself once its terminal is killed (measured 2026-11-04).

A run whose agent ended `blocked` is not closed: Herdr's `agent wait` counts
blocked as settled, so it is recorded `done` while waiting on a person.
`on-success` with `workspace: existing` is refused at load: closing a run's tab
can close the shared workspace with it.

## Why

The author of the automation is the one who knows whether its runs need
reading. A per-automation opt-in leaves ADR 0001 true for every automation that
does not say otherwise, and only touches runs that finished cleanly, which are
the ones with nothing to review.

Recording `done` before closing matters: closing is also how a person cancels a
run, and the hc host reads a killed terminal as cancelled.

## Rejected

- **`on-finish`, closing failures too** — a failed run's workspace is exactly
  the one to read.
- **Reaping on a timer, or `cleanup` closing workspaces** — cannot tell read
  from unread; the reason ADR 0001 exists.
- **Closing by default, with a `keep` opt-out** — changes every existing
  automation, including those whose workspaces are their inbox.

## Consequences

- A closed run is reachable only through `history` and its worktree.
- A close that fails is logged; the run stays `done` and its workspace open.
