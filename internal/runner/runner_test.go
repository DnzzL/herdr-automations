package runner

import "testing"

func TestSlugProducesValidBranchNames(t *testing.T) {
	cases := map[string]string{
		"Weekly sprint planning": "weekly-sprint-planning",
		"issue-triage":           "issue-triage",
		"Deps  bump!!":           "deps-bump",
		"  ~weird/name~  ":       "weird-name",
		"???":                    "automation",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
