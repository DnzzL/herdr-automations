# Domain language

The words this codebase uses, and what they mean here. Where a term is also a
Herdr term, the Herdr meaning wins — this is a plugin, not a peer.

## The things

**Automation** — one entry in `automations.yaml`: a cron expression, a repo, and
either a prompt or a workflow to delegate to. The unit the user thinks in. Never
"job", "task" or "schedule".

**Occurrence** — one moment a cron expression comes due. An automation has
infinitely many; most of them never become runs.

**Run** — one attempt at one occurrence. Runs are what `history` records and
what the board shows. An occurrence that was never attempted is not a run —
it's `missed`.

**Trigger** — why a run started: `cron` (on time), `catchup` (late but inside
the window) or `manual` (`r` on the board, or `herdr-automations run`).

**Session** — a provisioned workspace plus the pane its agent lives in. What
`host.Provision` hands back. The place a run happens. Predates Herdr sessions;
in prose, prefer **Herdr session** for the other one.

**Herdr session** — Herdr's: a named server with its own socket, several per
machine. Workspace and pane IDs mean something only inside one. `session:` in
`automations.yaml` names a Herdr session, never the term above.

**Work** — what a run actually does in its session. Two kinds: an *agent* taking
a prompt, or an *hwf delegation* handing off to herdr-workflows. Both answer the
same question — did it work?

**Diagnostic** — an entry in `automations.yaml` that did not load, plus the line
to go and fix. Not an error: the rest of the file still runs.

**Decision** — what `schedule.Plan` returns about one automation right now:
`fire`, `missed` or `register`. A value; nothing has happened yet.

**Candidate** — one run worktree and the verdict on it. **Plan** is all of them,
sorted into what can go and what stays.

**Verdict** — why a candidate is or isn't going away: `merged`,
`workspace still open`, `commits not in the default branch`.

## The states a run passes through

`scheduled` → `running` → one of `done`, `failed`, `cancelled`.

- **cancelled** is not a failure. Closing a run's workspace is how you call one
  off: nothing broke, somebody decided.
- **skipped** is an occurrence dropped because the previous run of the same
  automation was still working. Occurrences are dropped, never queued.
- **missed** is an occurrence the scheduler could not run at all — the machine
  was asleep past the catch-up window.
- **invalid** is an automation the scheduler could not read. Not a run that
  failed; a run that was never possible.

## Two things that are easy to confuse

**Workspace mode** (`worktree` | `root`) is what the automation asks for.
**Workspace** on its own is Herdr's: the thing with an ID that you can close.
Closing one is the gesture that cancels a run — and an open one is the only
durable signal that nobody has read a run yet, which is why `cleanup` never runs
on a schedule.

**Catch-up window** is how late an occurrence may still start.
`catch_up_minutes: -1` means "only on time", not "never at all".
