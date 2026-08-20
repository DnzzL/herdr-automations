package cleanup

import (
	"os/exec"
	"testing"

	"github.com/DnzzL/herdr-automations/internal/herdr"
)

// repoWithBranches builds a repo on main plus two run branches: one that never
// committed anything, one carrying work of its own.
func repoWithBranches(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := [][]string{
		{"init", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--allow-empty", "-m", "base"},
		{"branch", "auto/did-nothing"},
		{"checkout", "-b", "auto/did-work"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--allow-empty", "-m", "work"},
		{"checkout", "main"},
	}
	for _, args := range script {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestVerdict(t *testing.T) {
	repo := repoWithBranches(t)
	base, err := defaultBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if base != "main" {
		t.Fatalf("expected the main fallback, got %q", base)
	}

	cases := []struct {
		name string
		wt   herdr.Worktree
		want Verdict
	}{
		{"produced nothing", herdr.Worktree{Branch: "auto/did-nothing"}, Removable},
		{"has its own commits", herdr.Worktree{Branch: "auto/did-work"}, KeptUnmerged},
		{
			// An open workspace outranks everything: it means nobody has read
			// the run yet, whatever git thinks of the branch.
			"still open",
			herdr.Worktree{Branch: "auto/did-nothing", OpenWorkspaceID: "w42"},
			KeptOpen,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := verdict(repo, c.wt, base); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
