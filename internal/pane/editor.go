package pane

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// editorCommand opens the config file at line, using $VISUAL/$EDITOR and each
// editor's own way of jumping to a line. Unknown editors just get the path.
func editorCommand(path string, line int) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	if line <= 0 {
		return exec.Command(editor, path)
	}

	switch filepath.Base(editor) {
	case "vi", "vim", "nvim", "nano", "emacs", "kak":
		return exec.Command(editor, "+"+strconv.Itoa(line), path)
	case "hx", "helix":
		return exec.Command(editor, path+":"+strconv.Itoa(line))
	case "code", "codium", "cursor", "zed":
		return exec.Command(editor, "--goto", path+":"+strconv.Itoa(line))
	default:
		return exec.Command(editor, path)
	}
}
