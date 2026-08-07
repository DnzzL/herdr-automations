// Package skill installs the bundled agent skill where coding agents look for
// it. Shipping SKILL.md inside the plugin is not enough: Claude Code only
// discovers skills under ~/.claude/skills (or a project's .claude/skills), so
// without this step the agent never learns the automation format.
package skill

import (
	"fmt"
	"os"
	"path/filepath"
)

const dirName = "herdr-automations"

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

// Install symlinks the bundled skill into target (default ~/.claude/skills) so
// it tracks plugin upgrades instead of going stale.
func Install(target string) error {
	root, err := Root()
	if err != nil {
		return err
	}
	source := filepath.Join(root, "skills", "creating-automations")
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		return fmt.Errorf("bundled skill not found at %s: %w", source, err)
	}

	if target == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		target = filepath.Join(home, ".claude", "skills")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}

	link := filepath.Join(target, dirName)
	switch existing, err := os.Readlink(link); {
	case err == nil && existing == source:
		fmt.Printf("Already installed: %s → %s\n", link, source)
		return nil
	case err == nil:
		if err := os.Remove(link); err != nil { // repoint a stale link
			return err
		}
	default:
		if _, err := os.Lstat(link); err == nil {
			return fmt.Errorf("%s already exists and is not our symlink; remove it first", link)
		}
	}

	if err := os.Symlink(source, link); err != nil {
		return err
	}
	fmt.Printf("Installed the agent skill: %s → %s\n", link, source)
	fmt.Println("Start a new agent session, then ask for a scheduled task in plain language.")
	return nil
}
