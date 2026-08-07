#!/bin/sh
# Regenerates docs/wizard.gif — `herdr-automations add` answering from scratch.
#
#   nix-shell -p asciinema asciinema-agg tmux --run scripts/demo/record-wizard.sh
#
# Same tmux harness as record.sh: a real terminal keeps typing responsive.
set -eu

D="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$D/../.." && pwd)"
BIN="${BIN:-$ROOT/bin/herdr-automations}"
SOCK=hawiz
CAST="$D/wizard.cast"
GIF="${GIF:-$ROOT/docs/wizard.gif}"

# The wizard offers the current directory as the default repo, and that default
# ends up on screen — so record from a neutral path instead of a personal one.
CWD=/tmp/myapp
mkdir -p "$CWD"

# The wizard echoes where it saved; keep that path neutral and short rather
# than showing the recording machine's checkout.
export HERDR_PLUGIN_CONFIG_DIR=/tmp/herdr-demo
export HERDR_PLUGIN_STATE_DIR="$D/state"
export TERM=xterm-256color

[ -x "$BIN" ] || { echo "build first: go build -o bin/herdr-automations ." >&2; exit 1; }
rm -rf "$HERDR_PLUGIN_CONFIG_DIR" "$CAST"
mkdir -p "$HERDR_PLUGIN_CONFIG_DIR"
tmux -L "$SOCK" kill-server 2>/dev/null || true

asciinema rec "$CAST" --headless --overwrite --window-size 96x20 \
	-c "tmux -L $SOCK -f $D/tmux.conf new-session -s wiz -x 96 -y 20 -c $CWD '$BIN add'" &
REC=$!

for _ in $(seq 1 40); do
	sleep 0.5
	if tmux -L "$SOCK" capture-pane -p -t wiz 2>/dev/null | grep -q "Name"; then break; fi
done

# Type an answer, then submit it: -l keeps the text literal.
answer() {
	sleep "$2"
	[ -n "$1" ] && tmux -L "$SOCK" send-keys -t wiz -l "$1"
	sleep 0.5
	tmux -L "$SOCK" send-keys -t wiz Enter
}

answer "nightly-deps" 1.5
answer "0 3 * * 1-5" 1.2 # the cron preview lands here
answer "~/Projects/myapp" 3.0
answer "" 1.4            # workspace: keep worktree
answer "" 1.2            # agent: keep claude
answer "Bump dependencies, run the test suite, open a PR if green." 1.2
answer "" 1.5            # no MCP config
answer "" 1.2            # keep the 60 minute timeout
sleep 3
wait $REC

python3 "$D/trim.py" "$CAST" "Name (kebab-case)" "Test it now with" 2.5
agg --theme monokai --font-size 18 --idle-time-limit 2 "$CAST" "$GIF"
echo "wrote $GIF"
