#!/bin/sh
# Copies the panel mod into the Herdr app's home. A copy, not a symlink: the
# app does not see changes made behind a symlink, so it never reloads.
set -eu
src="$(cd "$(dirname "$0")/.." && pwd)/app/mods/automations"
dst="${HERDR_APP_HOME:-$HOME/.config/herdr-app}/mods/automations"
[ -L "$dst" ] && rm "$dst"
mkdir -p "$dst"
cp "$src"/main.js "$src"/mod.json "$src"/*.svg "$dst"/
herdr-app check "$dst"
