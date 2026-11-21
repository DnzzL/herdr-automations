# ADR 0013 — Windows is community-supported and hc-only

**Status**: accepted · **Date**: 2026-11-04

## Context

The Herdr desktop app and herdr-core (`hc`) run on Windows x86_64, and the
Automations panel loads there, but herdr-automations did not even build: the
scheduler lock used `flock` and `run --bg` used `Setsid` (#21). The maintainer
has no Windows machine, so nothing on Windows can be checked by hand. The Herdr
TUI, its plugin manifest, the board pane, toasts, `install-cli` and hwf
workflows are a Unix story, and probably do not run there at all.

## Decision

1. **Windows is community-supported.** It is built, vetted and tested in CI
   (a `windows-latest` job on every push and pull request) and released, but
   the maintainer never tries it by hand. A Windows bug is fixed when a
   contributor brings a fix or a failing test, not on the maintainer's word.
   What CI cannot prove lives in a manual checklist for contributors.
2. **On Windows, herdr-automations is the desktop app's backend and nothing
   else.** `backend: hc` is the only backend: a missing `backend` and `auto`
   mean hc, an explicit `herdr` is a Diagnostic. The TUI-side features stay
   unsupported there.
3. **Same contracts, other primitives.** The scheduler lock keeps its contract
   (one holder per machine, released by the OS however the holder dies, PID
   readable by others) with `LockFileEx`. Windows locks are mandatory, so it
   locks a byte far past the PID instead of the PID itself. Detached runs start
   in their own process group with no console, where Unix uses `Setsid`.

## Rejected

- **Full support, TUI backend included** — doubles the surface to port
  (`sh`-based builds, the shim, the board pane) for a host that likely does not
  exist on Windows, with nobody able to check it.
- **No Windows build, a contributor fork instead** — every change on main would
  break it silently; a CI job costs minutes and catches that.

## Consequences

- A change that breaks Windows fails CI, even though nobody on the project runs
  it.
- Release notes promise nothing about Windows fixes.
- Unix-only tests (`sh`, `flock`, launchd, systemd, the shim, the TUI) are
  guarded by build tags; contract tests run on both.
