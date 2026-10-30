# ADR 0011 — the Herdr app is a second host, and the panel only talks to the CLI

**Status**: accepted · **Date**: 2026-10-30

## Context

The Herdr desktop app (`herdr-app`) does not run on the TUI's server. Its
terminals live in herdr-core (`hc`), its extensions are JavaScript mods, and it
has no plugin manifest and no `[[startup]]`. The two share no agents: a run
started through `herdr` never appears in the app.

A mod lives only while the window is open. A scheduler has to fire at 3am with
the window closed.

## Decision

1. **A second `host.Host`, not a new seam.** `host.NewHC()` implements the same
   `Provision` / `Do` as the herdr host (ADR 0004). `Session.WorkspaceID` is the
   app space id, `Session.PaneID` the hc terminal address. A top-level
   `backend: herdr | hc | auto` picks one (`auto`: hc when `hc server status`
   answers). Unset means herdr: a file written before backends existed keeps
   running where it did, rather than moving to the app on upgrade. The runner,
   schedule, history and cleanup do not change.
2. **The hc host copies the app's own "New worktree…".** `git worktree add`
   into `~/.herdr/worktrees/<repo>/<slug(branch)>`, then
   `hc run --bg --keep --no-parent --label herdr.space=<new id> --label
   herdr.tab=<new id> --label automation=<name> -- <agent> <prompt>`.
   The app adopts a terminal whose space id it does not know as a new, unnamed
   space. Like one made from the button, it nests under its repo only while a
   space is open on the repo's main checkout.
   `workspace: existing` keeps ADR 0009: `workspace_id` is the app's space id,
   and a run whose space is gone fails.
3. **The daemon is an OS service.** `service install` writes a launchd agent
   (macOS) or a systemd user unit (Linux). It replaces `[[startup]]` for app
   users. It runs the `install-cli` shim when there is one: a plugin update
   moves the checkout, and the service daemon leaves when its binary is gone so
   launchd relaunches it through the shim onto the new build.
4. **The panel's only seam is the CLI.** The mod runs
   `herdr-automations … --json` through `exec` and watches the config folder.
   It never parses YAML or cron, and never writes the files itself. Read state
   for the inbox is a `read` event in `history.jsonl`.
5. **Sessions are a TUI notion.** `session:` (ADR 0010) is refused under
   `backend: hc`. The rule that the daemon exits with the last session applies
   only to a daemon a TUI session started for `backend: herdr`: hc needs no
   session, and the login service owns its lifetime (launchd would relaunch it).
6. **Under hc, failures are announced by the panel.** The app has no
   toast an outside process can raise. `notify` becomes a no-op on the hc host;
   the panel toasts new `failed` / `missed` entries (ADR 0007's table still
   decides which).

## Rejected

- **`herdr-app do space.new` to create the run's space.** Same path as the
  button, but it needs an open window, takes no labels, and a fallback for the
  closed window would mean two provisioning paths to test.
- **A scheduler inside the mod.** Dies with the window.
- **Rewriting the daemon in TypeScript to share code with the mod.** Mods run
  in QuickJS without Node APIs; next to nothing would be shared, and six
  thousand tested lines would be thrown away.
- **`hc run --name` as the overlap guard.** A finished agent idling at its
  prompt keeps the name. The label stays the guard.

## Consequences

- Announcements under hc only reach you when a Herdr window is open; the inbox
  count covers the rest.
- Space adoption by label is documented by the app but unproven here: the first
  test of the port pins it, window open and closed.
