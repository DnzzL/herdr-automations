// Package shim puts `herdr-automations` on the user's PATH. Herdr runs plugin
// binaries from its own install directory and never exposes them to a shell,
// and that directory moves between Herdr versions and on every plugin update.
// So the shim hardcodes nothing: each call asks Herdr where the plugin lives
// now (herdr plugin list --json reads from disk, no server needed).
package shim

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/DnzzL/herdr-automations/internal/hostpath"
)

const name = "herdr-automations"

// marker identifies a shim we wrote, so a rerun may replace it and nothing
// else.
const marker = "# Written by `herdr-automations install-cli`."

// script resolves the plugin root on every call. sed rather than jq: jq is not
// everywhere. HERDR_BIN_PATH is only trusted while it still exists, like
// hostpath.Bin.
var script = `#!/bin/sh
` + marker + ` Safe to delete.
herdr=${HERDR_BIN_PATH:-herdr}
[ -x "$herdr" ] || herdr=herdr
if ! command -v "$herdr" >/dev/null 2>&1; then
	echo "herdr-automations: herdr not found on PATH" >&2
	exit 127
fi
root=$("$herdr" plugin list --plugin ` + hostpath.PluginID + ` --json 2>/dev/null | sed -n 's/.*"plugin_root":"\([^"]*\)".*/\1/p')
if [ -z "$root" ]; then
	echo "herdr-automations: plugin not installed (herdr plugin install DnzzL/herdr-automations)" >&2
	exit 127
fi
exec "$root/bin/herdr-automations" "$@"
`

// ErrNotOurs: something other than our shim already sits at the target path.
var ErrNotOurs = errors.New("not a herdr-automations shim")

// Install writes the shim into dir (default ~/.local/bin). Rerunning is a
// no-op; an older shim of ours is rewritten. Errors: ErrNotOurs when another
// file holds the name, which is left untouched.
func Install(dir string) error {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".local", "bin")
	}
	path := filepath.Join(dir, name)

	switch existing, err := os.ReadFile(path); {
	case err == nil && string(existing) == script:
		fmt.Printf("Already installed: %s\n", path)
		return nil
	case err == nil && !bytes.Contains(existing, []byte(marker)):
		return fmt.Errorf("%s: %w; remove it or pick another directory", path, ErrNotOurs)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	fmt.Printf("Installed: %s\n", path)
	if !slices.Contains(filepath.SplitList(os.Getenv("PATH")), dir) {
		fmt.Printf("%s is not on your PATH. Add it, e.g. in ~/.profile:\n  export PATH=\"%s:$PATH\"\n", dir, dir)
	}
	return nil
}
