#!/bin/sh
# Regenerates docs/board.gif.
#
#   nix-shell -p asciinema asciinema-agg tmux --run scripts/demo/record.sh
#
# The TUI runs inside tmux because tmux answers the terminal capability probes
# bubbletea sends at startup. Without a terminal that replies, the app stalls
# for ~13s and then replays every queued keystroke at once, collapsing the
# whole demo into a single frame.
set -eu

D="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$D/../.." && pwd)"
BIN="${BIN:-$ROOT/bin/herdr-automations}"
SOCK=hademo
CAST="$D/board.cast"
GIF="${GIF:-$ROOT/docs/board.gif}"

export HERDR_PLUGIN_CONFIG_DIR="$D/cfg"
export HERDR_PLUGIN_STATE_DIR="$D/state"
export EDITOR=vim VISUAL=vim TERM=xterm-256color

[ -x "$BIN" ] || { echo "build first: go build -o bin/herdr-automations ." >&2; exit 1; }
python3 "$D/seed.py" "$D/state"
tmux -L "$SOCK" kill-server 2>/dev/null || true
rm -f "$CAST"

asciinema rec "$CAST" --headless --overwrite --window-size 96x20 \
	-c "tmux -L $SOCK -f $D/tmux.conf new-session -s demo -x 96 -y 20 '$BIN pane'" &
REC=$!

# Wait for the board to actually be on screen before touching anything.
for _ in $(seq 1 40); do
	sleep 0.5
	if tmux -L "$SOCK" capture-pane -p -t demo 2>/dev/null | grep -q flaky-hunt; then break; fi
done

key() { sleep "$2"; tmux -L "$SOCK" send-keys -t demo "$1"; }

sleep 2.2
key j 0
key j 1.5
key k 1.5
key e 1.5 # opens the YAML at the selected automation's line
sleep 4.5
tmux -L "$SOCK" send-keys -t demo ':q' Enter
sleep 3
tmux -L "$SOCK" send-keys -t demo q
wait $REC

python3 "$D/trim.py" "$CAST" "Automations" "config reloaded" 2.0
agg --theme monokai --font-size 18 --idle-time-limit 2 "$CAST" "$GIF"
echo "wrote $GIF"
