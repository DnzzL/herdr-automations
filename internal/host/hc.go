package host

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/hc"
)

// hcOps is the hc host's internal seam: herdr-core, and the two git calls a
// worktree run needs now that no Herdr makes the worktree for us.
type hcOps interface {
	Run(cwd string, labels map[string]string, argv []string) (string, error)
	Wait(address string, timeout time.Duration) (hc.Wait, error)
	Get(address string) (hc.Terminal, error)
	// MainCheckout is the repo's main working tree, whatever checkout repo is.
	MainCheckout(repo string) (string, error)
	WorktreeAdd(repo, branch, path string) error
}

// NewHC returns the Host that runs automations in herdr-core, where the Herdr
// desktop app shows them (ADR 0010).
func NewHC() Host {
	home, _ := os.UserHomeDir()
	return &hcHost{ops: hcLive{}, home: home, knobs: defaultKnobs()}
}

type hcHost struct {
	ops   hcOps
	home  string
	knobs knobs
}

// Provision makes the place and starts the agent there with its prompt.
//
// Starting the agent here rather than in Do is what puts its terminal address
// in the run's history as soon as it is running. hc takes the prompt with the
// launch, so there is no separate submit step to keep apart.
func (h *hcHost) Provision(a config.Automation) (Session, error) {
	argv, err := hcArgv(a)
	if err != nil {
		return Session{}, err
	}
	space, cwd := newID("sp"), a.Repo
	switch a.Workspace {
	case config.WorkspaceWorktree:
		cwd, err = h.worktree(a)
		if err != nil {
			return Session{}, err
		}
	case config.WorkspaceExisting:
		space = a.WorkspaceID
	case config.WorkspaceRoot:
	default:
		return Session{}, fmt.Errorf("unknown workspace mode %q", a.Workspace)
	}
	// An id the app does not know becomes a new space or tab, unnamed, so it
	// is called after its folder and nests under its repo.
	labels := map[string]string{"herdr.space": space, "herdr.tab": newID("tb"), "automation": a.Name}
	address, err := h.ops.Run(cwd, labels, argv)
	if err != nil {
		return Session{WorkspaceID: space}, fmt.Errorf("start %s: %w", argv[0], err)
	}
	return Session{WorkspaceID: space, PaneID: address}, nil
}

// worktree checks out a fresh branch where the app's "New worktree…" would:
// ~/.herdr/worktrees/<main checkout's folder>/<branch, slashes as dashes>.
func (h *hcHost) worktree(a config.Automation) (string, error) {
	main, err := h.ops.MainCheckout(a.Repo)
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository: %w", a.Repo, err)
	}
	branch := fmt.Sprintf("auto/%s-%s", slug(a.Name), time.Now().Format("20060102-1504"))
	path := filepath.Join(h.home, ".herdr", "worktrees", filepath.Base(main), appSlug(branch))
	if err := h.ops.WorktreeAdd(a.Repo, branch, path); err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}
	return path, nil
}

// Do waits for the agent to finish its turn.
func (h *hcHost) Do(s Session, a config.Automation, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	started := time.Now()
	turn := 0
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("the agent was still working after %s", timeout)
		}
		w, err := h.ops.Wait(s.PaneID, min(h.knobs.waitSlice, remaining))
		if err != nil {
			// The terminal is unreadable: removed along with its space, most
			// likely. Not enough to call it a crash (see agentGoneOutcome).
			log.Printf("could not wait on %s (%v), recording it as cancelled", s.PaneID, err)
			return ErrCancelled
		}
		turn = max(turn, w.Turn)
		switch {
		case w.Reason == "exit":
			return h.exitOutcome(s)
		case w.Reason == "status" && w.Status == "error":
			return fmt.Errorf("the agent reported an error")
		case w.Reason == "status" && turn > 0 && (w.Status == "done" || w.Status == "idle"):
			return nil
		}
		// No turn yet after this long: the agent is sitting on something only
		// a person can answer, like Claude's trust dialog in a folder it has
		// never seen. It reports no status there at all, not even blocked.
		if turn == 0 && time.Since(started) > h.knobs.paneReady {
			return fmt.Errorf("the agent never started working; open %s and look for a dialog (trust prompt?)", s.PaneID)
		}
		if w.Ended {
			// blocked, or idle before the prompt landed: wait answers at once
			// for those, so pace the loop.
			time.Sleep(h.knobs.statusPoll)
		}
	}
}

// exitOutcome tells a run called off from one that crashed: closing the
// run's space in the app kills its terminal; a crash exits on its own.
func (h *hcHost) exitOutcome(s Session) error {
	t, err := h.ops.Get(s.PaneID)
	if err != nil || t.Reason == "killed" {
		return ErrCancelled
	}
	if t.ExitCode != nil {
		return fmt.Errorf("the agent exited with code %d before finishing", *t.ExitCode)
	}
	return fmt.Errorf("the agent exited before finishing")
}

// hcArgv is what hc runs: a template and its task, or the agent with its
// flags and the prompt as its first message.
func hcArgv(a config.Automation) ([]string, error) {
	if a.Template != "" {
		return []string{a.Template, a.Prompt}, nil
	}
	if !config.KindTakesPromptArg(a.Agent) {
		return nil, fmt.Errorf("agent %q cannot take its prompt under backend: hc yet", a.Agent)
	}
	argv := append([]string{a.Agent}, agentArgs(a)...)
	return append(argv, a.Prompt), nil
}

var appSlugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// appSlug is the app's own folder name for a branch (mods/worktree/main.js).
func appSlug(branch string) string {
	return strings.Trim(appSlugRe.ReplaceAllString(branch, "-"), "-")
}

// newID mints an id in the app's shape: a two-letter kind and 32 hex digits.
func newID(kind string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return kind + hex.EncodeToString(b)
}

// hcLive is the production hcOps.
type hcLive struct{ hc.Client }

func (hcLive) MainCheckout(repo string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", err
	}
	return filepath.Dir(strings.TrimSpace(string(out))), nil
}

func (hcLive) WorktreeAdd(repo, branch, path string) error {
	out, err := exec.Command("git", "-C", repo, "worktree", "add", "-b", branch, path, "HEAD").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}
