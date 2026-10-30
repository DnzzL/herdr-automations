# ADR 0010 — the daemon belongs to no Herdr session

**Status**: accepted · **Date**: 2026-10-19

## Context

Herdr runs several named sessions per machine, each with its own server and
socket. Plugins are user-global, so every server's startup hook launches the
daemon, and the machine-wide lock lets exactly one win. The winner inherits its
server's socket, and every cron run lands in that session — chosen by start
order, which `automations.yaml` has no say in (#9).

Two further consequences: a manual run (`r`, `run`) lands in the *caller's*
session, so the same automation runs in different places depending on how it
was triggered; and when the owning session stops, the daemon survives it,
keeps the lock, and every run fails against a dead socket.

## Decision

One daemon per machine, as before. It no longer belongs to the session that
started it:

- An automation may name a Herdr session: `session: automations`. Every Herdr
  call for that run carries `--session <name>`, whoever triggered it — cron,
  `r` or `run`. Without the key, a run goes where it goes today: the session of
  the process that runs it.
- A session that is not running fails the run with Herdr's own
  `server_not_running`. The plugin never starts a session.
- Failure toasts go to every running session, not just the run's: a dedicated
  automation session is precisely the one nobody is looking at.
- The daemon outlives the session that started it, and exits only once no
  session is running at all.
- History records each run's session, so the board's `enter` can tell "closed"
  apart from "lives in another session".

## Rejected: one daemon per session

Each startup hook would run its own daemon, locked per session, serving only
the automations that name it. It fits "a daemon per server start", but it
partitions one file between several writers of `history.jsonl` and
`schedule.json`, needs a rule for which daemon serves entries without
`session:`, and keeps the start-order race for exactly those entries.

## Consequences

- `workspace_id` in `existing` mode belongs to the automation's session, so
  that combination becomes well-defined.
- The losing startup hook is not an error: it exits 0 and says which daemon is
  already serving.
- The board can focus only within its own session; Herdr offers no API to move
  an attached client. For a run elsewhere it says where to attach.
- Nothing here may grow a "start the session if it's missing" flag, for the
  same reason ADR 0009 refuses to recreate a closed workspace.
