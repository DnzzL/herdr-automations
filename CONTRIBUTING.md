# Contributing

The scope is deliberately narrow: **an automation is one prompt on a clock.**
Sequencing, shared memory between agents, and orchestration belong to other
plugins. A PR that grows the plugin past that line is likely to be declined on
scope rather than on quality — open an issue first and we'll work out whether it
fits before you write it.

## What is most wanted

- **Agent kinds tested in the wild.** The plugin claims to work with anything
  `herdr agent start` supports; only `claude` is exercised daily. Reports of what
  breaks on `codex`, `opencode`, `gemini` or `cursor` are worth more than code.
- **Event triggers.** Herdr's plugin manifest supports `[[events]]`; cron came
  first because it covers most of the value.
- **Run cleanup policies** — which worktrees are demonstrably safe to remove.

## Working on it

```bash
go build -o bin/herdr-automations . && go test ./...
go vet ./... && gofmt -l .          # both must be clean; CI enforces them
herdr plugin link .                 # run your checkout as the installed plugin
```

`herdr plugin link` replaces any GitHub install of the plugin, and the daemon
re-executes itself when the binary changes, so a rebuild is enough to test.

Config lives in `herdr plugin config-dir dnzzl.automations`, run history in
`~/.local/state/herdr/plugins/dnzzl.automations`. Both survive uninstalls; delete
them by hand if you want a clean slate.

## House style

- **Comments explain why, not what.** The code says what it does.
- **No hidden behaviour.** If the plugin would do something the config file
  doesn't say — serialise, retry, clean up, default to a value — the answer is
  usually to report it and let the user decide instead.
- Validate in `config.Load()`. A mistake should surface when the file is written,
  not inside a scheduler goroutine at 3am.
- Every user-visible change gets a `CHANGELOG.md` entry describing what it means
  for someone using the plugin, not what was refactored.
