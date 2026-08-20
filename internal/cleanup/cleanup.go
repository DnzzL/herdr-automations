// Package cleanup removes the worktrees runs leave behind — but only the ones
// whose work has demonstrably landed somewhere.
//
// Accumulating worktrees are not purely litter: a run whose workspace is still
// open is a run nobody has read, which makes Herdr's sidebar the inbox for
// unattended work. That is why nothing here runs on a schedule and why the
// default is to keep. A reaper that tidied on its own would delete the only
// signal saying which runs still want a human.
package cleanup

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
)

// BranchPrefix marks the branches runs create; anything else in the repo is
// somebody's own work and is never a candidate.
const BranchPrefix = "auto/"

// Verdict is why a worktree is or isn't going away.
type Verdict string

const (
	// Removable: the branch is an ancestor of the default branch, so its
	// commits — if it made any — are already there.
	Removable Verdict = "merged"
	// KeptOpen: the workspace is still open. Whether the run was any good is
	// not something this command can know, so it doesn't guess.
	KeptOpen Verdict = "workspace still open"
	// KeptUnmerged: commits that exist nowhere else. Squash-merged branches
	// land here too, which is the safe direction to be wrong in.
	KeptUnmerged Verdict = "commits not in the default branch"
)

// Candidate is one run worktree and what should happen to it.
type Candidate struct {
	Repo    string
	Branch  string
	Path    string
	Verdict Verdict
}

func (c Candidate) Removable() bool { return c.Verdict == Removable }

// Scan classifies every run worktree across the repos the config names. Repos
// are visited once even when several automations share one.
func Scan(cfg *config.Config) ([]Candidate, error) {
	var out []Candidate
	seen := map[string]bool{}
	for _, a := range cfg.Automations {
		if seen[a.Repo] {
			continue
		}
		seen[a.Repo] = true

		worktrees, err := herdr.WorktreeList(a.Repo)
		if err != nil {
			return nil, fmt.Errorf("listing worktrees of %s: %w", a.Repo, err)
		}
		base, err := defaultBranch(a.Repo)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.Repo, err)
		}
		for _, w := range worktrees {
			if !strings.HasPrefix(w.Branch, BranchPrefix) {
				continue
			}
			out = append(out, Candidate{
				Repo: a.Repo, Branch: w.Branch, Path: w.Path,
				Verdict: verdict(a.Repo, w, base),
			})
		}
	}
	return out, nil
}

func verdict(repo string, w herdr.Worktree, base string) Verdict {
	if w.OpenWorkspaceID != "" {
		return KeptOpen
	}
	if merged(repo, w.Branch, base) {
		return Removable
	}
	return KeptUnmerged
}

// Remove drops the checkout and then the branch. Neither step is forced: git
// refuses a dirty worktree and refuses an unmerged branch, and those refusals
// are worth more than the tidiness they cost.
func Remove(c Candidate) error {
	if err := git(c.Repo, "worktree", "remove", c.Path); err != nil {
		return fmt.Errorf("%s: %w", c.Branch, err)
	}
	if err := git(c.Repo, "branch", "-d", c.Branch); err != nil {
		return fmt.Errorf("%s: checkout removed, branch kept: %w", c.Branch, err)
	}
	return nil
}

// defaultBranch asks the remote what it considers default, falling back to the
// usual names when the repo has no origin/HEAD — a fresh clone often doesn't.
func defaultBranch(repo string) (string, error) {
	out, err := output(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil && out != "" {
		return out, nil
	}
	for _, candidate := range []string{"origin/main", "origin/master", "main", "master"} {
		if err := git(repo, "rev-parse", "--verify", "--quiet", candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot tell which branch is the default one")
}

func merged(repo, branch, base string) bool {
	return git(repo, "merge-base", "--is-ancestor", branch, base) == nil
}

func git(repo string, args ...string) error {
	return exec.Command("git", append([]string{"-C", repo}, args...)...).Run()
}

func output(repo string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}
