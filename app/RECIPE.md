# Automations

Scheduled agent prompts, as a side panel next to Files and Changes. A prompt, a
cron line and a repo: every weekday at 9:00 an agent starts in a fresh worktree
and works. The panel is where you find what ran while you were away, start one
now, pause them all, or write a new one with its next runs previewed.

![Automations](preview.png)

## Before you add it

This mod is the face of [herdr-automations](https://github.com/DnzzL/herdr-automations),
a Go scheduler. The panel draws and asks; the binary schedules, launches runs in
herdr-core and keeps the files. Install it first, or the panel shows an error:

```bash
git clone https://github.com/DnzzL/herdr-automations && cd herdr-automations
sh scripts/install.sh                               # prebuilt binary into bin/
ln -s "$PWD/bin/herdr-automations" ~/.local/bin/    # where the panel looks
herdr-automations service install                   # the scheduler, at login and now
```

Then put `backend: hc` at the top of the file `herdr-automations list` names, so
runs open in the app. The mod **runs programs**: `herdr-automations` (every read
and write) and `hc templates`. It reaches no network and writes no file itself.

## What you see

A card filling its block (`rounded(10)`, `border` 1px, `background`), three parts
top to bottom:

- **Header**, 42px, bottom border: "Automations" (semibold), then right-aligned
  "Active" / "All paused" (11px; `dim`, or `blocked` when paused), a 30×17 switch
  (`accent` when on, `input` when off, a 13px `background` knob), and a `+` button.
  In the detail and form views a `‹` button comes first.
- **Banner**, only when no scheduler holds the lock: `blocked` at 13% alpha,
  "Scheduler stopped: nothing will run on time." and a solid `Start` button.
- **Body**, scrolling, one of three views:
  - **List.** Section labels in 11px semibold `dim` caps.
    `INBOX` with a count pill and, when non-empty, "Mark all read" at the right.
    Each inbox row: a 7px dot (`ansi2` done, `ansi1` failed, `dim` cancelled), the
    automation, its status in the same color, "9m ago"; below, indented, the error
    or the duration and "· open" while its terminal is live. Empty: "Nothing new."
    Then `AUTOMATIONS`: per entry an 8px dot, the name (semibold), a `▶` button;
    below in 11px `muted` "Weekdays 9:00 · Mon 09:00" (or "paused", "running ·
    working"); below in `dim` "claude · sonnet · worktree". Entries that did not
    load follow on an `ansi1` 9% tint with their message.
  - **Detail.** Dot and name (15px semibold); buttons `Run now` (solid), `Pause` /
    `Resume`, `Edit`, `Delete` (`ansi1` text). Key–value rows (78px `dim` keys):
    Schedule, Next, Repo, Runs in, Agent. The prompt on a `raised` block. `HISTORY`:
    status (66px, colored), error or duration, "3h ago".
  - **Form.** "New automation" or "Edit <name>". Fields, each under an 11px label:
    NAME; SCHEDULE as pills (Hourly, Every morning, Weekdays 9:00, Mondays 9:00,
    Fridays 17:00, Custom) over a cron input; REPO with `Choose…`; RUNS IN as pills
    (New worktree, Repo root, Existing space, the last opening a pill per local
    space); AGENT as pills (claude, codex, pi, then each hc template); MODEL (hidden
    for a template); PROMPT (textarea, 4–12 rows); TIMEOUT (MINUTES); MCP CONFIG.
    Below: "Next: in 3h, Mon 09:00, Tue 09:00" in `muted`, or the problem in `ansi1`.
    `Cancel`, `Save` (solid) at the right.
- Pills: 24px, `raised`, `border`; the chosen one `selected` with an `accent` border.
  Buttons: 26px, radius 7, `raised` + `border`, or `solid` / `solid_foreground`.

## How it behaves

- **Dot colors**: `working` while a run of it is live and working, `blocked` while
  that run waits on you, `ansi1` when the last run failed or was missed, `dim` when
  paused (itself or all), else `idle`. Live means a terminal labelled
  `automation=<name>` in `terminals()`.
- **Inbox**: finished runs (done, failed, cancelled) not yet seen. Clicking one
  marks it seen, then focuses its terminal (`pane.focus {terminal}`) if still live,
  else opens the automation's detail.
- **▶ / Run now**: `herdr-automations run <name> --bg`, returns at once; the run
  appears as a new space.
- **Switch**: `pause --all` / `resume --all`. **Pause** in detail: `pause <name>`.
- **Delete** asks once: the button turns into "Confirm delete".
- **Form**: every keystroke or pill re-runs `put --dry-run`, last answer wins;
  `Save` runs `put` (with `--original <name>` when editing) and opens the detail.
- **Toasts**: a run that turns `failed` or `missed` after the panel started posts
  `notify` (error / warning); clicking it focuses the run's terminal when live.
- **Refresh**: on a change in the config folder or the history folder
  (`watchFolder`), and every 30s for next-run times and the scheduler's state.

## Built from

- One mod, `mods/automations/`: `main.js`, `mod.json` (`place: "none"`), and
  `automations.svg` (a clock, 24×24 stroke icon) for the tab bar.
- A block `{ "id": "automations", "type": "block", "content": { "mod": "automations" } }`
  added to the `panel-body` stack of the layout, next to `files` and `changes`.
- A tab-bar button: in a clone of the shipped `tabs` mod, one more
  `panelButton("automations", "Automations", "mods/automations/automations.svg")`,
  and `"automations"` always in its `enabled` list.
- `herdr`: `exec`, `hc`, `home`, `theme`, `terminals`, `spaces`, `run`
  (`pane.focus`), `notify`, `pickFolder`, `watchFolder`. `gpui-base`: `InputState`,
  `Input`, `TextareaState`, `Textarea`, `v_flex`, `h_flex`.
- The CLI it calls: `ls --json`, `history --json`, `put [--original n] [--dry-run]`
  (JSON on stdin), `rm`, `seen <id>|--all`, `run <name> --bg`,
  `pause|resume <name>|--all`, `service install`.

## Watch out

- **Shipped layouts and mods refuse structural edits.** Clone first:
  `herdr-app do layout.clone --name main --as main-plus` and
  `herdr-app do mod.clone --name tabs --as tabs-plus`, point the layout's tab bar
  block at `tabs-plus`, then add the block and the button.
- **`shared("workspace.panels")` is not re-read from `shared.json`** when the file is
  edited by hand; a panel missing from it is closed by the tab bar on every space
  switch. Add `automations` in the clone's code instead.
- **`exec` has no stdin.** `put` gets its JSON through
  `sh -c 'printf %s "$json" | "$bin" put …'`.
- **`exec` stops at 30s.** Never call `run <name>` without `--bg`.
- **Symlinked mod folders are not reloaded** on change; copy the files.
- **`toLocaleDateString` options are ignored** by the runtime: spell weekdays out.
- `InputState` / `TextareaState` only in `init` or handlers, never in `render`.
- Under `backend: hc` only `claude`, `codex` and `pi` take a prompt; the form offers
  those and the hc templates.

## Done when

- `herdr-app check mods/automations` passes.
- With the binary installed, the tab bar shows "Automations" with its clock icon,
  and the panel lists every automation `herdr-automations list` shows.
- `+`, a name, a preset, a repo and a prompt: "Next:" shows three times; `Save`
  adds the entry to `automations.yaml` with the file's comments intact.
- `▶` opens a new space within seconds; when the agent finishes, the run is in the
  inbox, and clicking it focuses its terminal.
- The switch writes `paused: true`; the header reads "All paused".
- `herdr-automations service uninstall` brings up the "Scheduler stopped" banner
  within 30s; `Start` takes it away.
