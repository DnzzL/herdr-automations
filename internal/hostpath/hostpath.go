// Package hostpath answers "where is it" for everything the plugin needs to
// find: the herdr binary, the config directory, the state directory, the
// plugin checkout.
//
// Every one of those follows the same shape — Herdr's environment variable
// wins, then we ask the herdr CLI, then we fall back to Herdr's own layout —
// and every one of them used to be answered in whichever package happened to
// need it. That cost us the fix in f30e866 twice: `herdr.bin` learned to
// distrust a HERDR_BIN_PATH whose file is gone, `config.herdrBin` never did,
// and a stale path there resolves the config directory to a guess. A guess
// that reads as an empty automations.yaml, which is every automation silently
// disappearing.
package hostpath

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PluginID must match the id in herdr-plugin.toml: it locates the same
// directories Herdr hands the daemon when the CLI is run from a plain shell.
const PluginID = "dnzzl.automations"

// Bin locates the herdr CLI. HERDR_BIN_PATH is herdr telling a plugin exactly
// which binary to call back into, so it wins — but only if it still exists. A
// long-running herdr whose binary was moved or uninstalled under it (a package
// manager switch, say) keeps advertising the old path, and taking it on faith
// made every call fail with a fork/exec error the user could do nothing about.
func Bin() string {
	if b := os.Getenv("HERDR_BIN_PATH"); b != "" && usable(b) {
		return b
	}
	return "herdr"
}

// HCBin locates herdr-core's CLI. A launchd or systemd service starts with a
// bare PATH that rarely holds ~/.local/bin, where the desktop app installs it,
// so that is the fallback rather than a bare "hc" that would not resolve.
func HCBin() string {
	if b := os.Getenv("HC_BIN"); b != "" && usable(b) {
		return b
	}
	if b, err := exec.LookPath("hc"); err == nil {
		return b
	}
	home, _ := os.UserHomeDir()
	if b := filepath.Join(home, ".local", "bin", "hc"); usable(b) {
		return b
	}
	return "hc"
}

// usable reports whether path is something we could actually execute.
func usable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// ConfigDir resolves the config directory. Under Herdr the env var is
// authoritative; from a plain shell we ask the herdr CLI, so
// `herdr-automations list` in a terminal always sees what the daemon sees.
func ConfigDir() string {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	if out, err := exec.Command(Bin(), "plugin", "config-dir", PluginID).Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return d
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr", "plugins", "config", PluginID)
}

// StateDir resolves where run history and scheduler state live. Herdr exposes
// no state-dir command, so outside a plugin context we mirror its layout.
func StateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "herdr", "plugins", PluginID)
}

// Root locates the plugin checkout: Herdr sets it, otherwise derive it from
// the binary's location (bin/herdr-automations lives inside the checkout).
func Root() (string, error) {
	if r := os.Getenv("HERDR_PLUGIN_ROOT"); r != "" {
		return r, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(exe)), nil
}

// InstalledBinary is where Herdr has the plugin's binary now, or "" when it
// cannot say. A plugin update installs into a new checkout and leaves the old
// one in place, so a long-running process can only learn it is stale by
// asking. The install-cli shim asks the same question on every call.
func InstalledBinary() string {
	out, err := exec.Command(Bin(), "plugin", "list", "--plugin", PluginID, "--json").Output()
	if err != nil {
		return ""
	}
	return installedBinary(out)
}

func installedBinary(raw []byte) string {
	var list struct {
		Result struct {
			Plugins []struct {
				ID   string `json:"plugin_id"`
				Root string `json:"plugin_root"`
			} `json:"plugins"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return ""
	}
	for _, p := range list.Result.Plugins {
		if p.ID == PluginID && p.Root != "" {
			return filepath.Join(p.Root, "bin", "herdr-automations")
		}
	}
	return ""
}
