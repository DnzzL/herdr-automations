package pane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DnzzL/herdr-automations/internal/cleanup"
	"github.com/DnzzL/herdr-automations/internal/history"
)

// board loads the model over a config file written for the test.
func board(t *testing.T, yaml string) model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "automations.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return load()
}

// press sends a keystroke and hands back the board it produced.
func press(t *testing.T, m model, key string) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(model), cmd
}

func send(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}

const oneGoodOneBroken = `automations:
  - {name: fine, cron: "@daily", repo: /x, prompt: p}
  - {name: broken, cron: "0 99 * * *", repo: /x, prompt: p}
`

func TestBoardShowsABrokenEntryInsteadOfHidingIt(t *testing.T) {
	// A typo used to blank the whole board with "config error". It costs one
	// red row now.
	m := board(t, oneGoodOneBroken)

	if m.err != nil {
		t.Fatalf("a bad entry must not fail the board: %v", m.err)
	}
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want the good one and the broken one", len(m.rows))
	}
	broken := m.rows[1]
	if broken.name != "broken" || !broken.broke() {
		t.Fatalf("second row = %+v, want the broken entry with its diagnostic", broken)
	}
	if got := broken.status(); got != string(history.StatusInvalid) {
		t.Errorf("status = %q, want invalid", got)
	}
	// The line is what `e` opens the editor on.
	if broken.line != 3 {
		t.Errorf("line = %d, want 3", broken.line)
	}
	if !strings.Contains(m.View(), "broken") {
		t.Error("the broken entry is not on screen")
	}
}

func TestRunningABrokenEntrySaysWhyRatherThanRunningNothing(t *testing.T) {
	m := board(t, oneGoodOneBroken)
	m.cursor = 1

	m, cmd := press(t, m, "r")
	if cmd != nil {
		t.Error("nothing should be run for an entry that never loaded")
	}
	if !strings.Contains(m.notice, "automations.yaml:3") {
		t.Errorf("notice = %q, want it to point at the line to fix", m.notice)
	}
}

func TestJumpingToABrokenEntryPointsAtTheEditorInstead(t *testing.T) {
	m := board(t, oneGoodOneBroken)
	m.cursor = 1

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd != nil {
		t.Error("there is no workspace to jump to")
	}
	if !strings.Contains(m.notice, "never ran") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestAPendingConfirmationSurvivesTheRefresh(t *testing.T) {
	// The board rebuilds itself every two seconds. A question already on screen
	// has to survive it, or answering it lands on nothing.
	m := board(t, oneGoodOneBroken)
	m = send(t, m, scannedMsg{plan: cleanup.Plan{
		Removable: []cleanup.Candidate{{Branch: "auto/x", Verdict: cleanup.Removable}},
	}})
	if m.pending == nil {
		t.Fatal("want the board asking")
	}

	m = send(t, m, refreshMsg{})
	if m.pending == nil {
		t.Fatal("the question was dropped by the refresh")
	}
	if m.notice == "" {
		t.Error("the question text went with it")
	}
}

func TestAPendingConfirmationOwnsTheKeyboard(t *testing.T) {
	// No stray j/k acting on a board that is asking a question.
	m := board(t, oneGoodOneBroken)
	m = send(t, m, scannedMsg{plan: cleanup.Plan{
		Removable: []cleanup.Candidate{{Branch: "auto/x", Verdict: cleanup.Removable}},
	}})

	m, cmd := press(t, m, "j")
	if cmd != nil {
		t.Error("j must not act while a question is pending")
	}
	if m.cursor != 0 {
		t.Errorf("cursor moved to %d", m.cursor)
	}
	// Anything other than y is a no.
	if m.pending != nil {
		t.Error("want the question answered no")
	}
	if !strings.Contains(m.notice, "nothing removed") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestAnsweringYesRemovesWhatWasOffered(t *testing.T) {
	m := board(t, oneGoodOneBroken)
	m = send(t, m, scannedMsg{plan: cleanup.Plan{
		Removable: []cleanup.Candidate{{Branch: "auto/x", Verdict: cleanup.Removable}},
	}})

	_, cmd := press(t, m, "y")
	if cmd == nil {
		t.Fatal("want the removal started")
	}
}

func TestAScanThatRemovesNothingSaysWhatIsBeingHeld(t *testing.T) {
	// "Nothing to remove" alone reads as "no run worktrees exist", which sends
	// you looking for worktrees that are right there.
	m := board(t, oneGoodOneBroken)
	m = send(t, m, scannedMsg{plan: cleanup.Plan{
		Kept: []cleanup.Candidate{{Branch: "auto/x", Verdict: cleanup.KeptOpen}},
	}})

	if m.pending != nil {
		t.Error("nothing to remove is not a question")
	}
	if !strings.Contains(m.notice, "workspace still open") {
		t.Errorf("notice = %q, want the reason named", m.notice)
	}
}

func TestTheCursorSurvivesTheConfigShrinkingUnderIt(t *testing.T) {
	m := board(t, oneGoodOneBroken)
	m.cursor = 1

	// The file loses an entry between refreshes.
	if err := os.WriteFile(filepath.Join(os.Getenv("HERDR_PLUGIN_CONFIG_DIR"), "automations.yaml"),
		[]byte("automations:\n  - {name: fine, cron: \"@daily\", repo: /x, prompt: p}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, refreshMsg{})

	if m.cursor >= len(m.rows) {
		t.Fatalf("cursor = %d with %d rows: the next keystroke would panic", m.cursor, len(m.rows))
	}
}

func TestAnEmptyConfigSaysHowToStart(t *testing.T) {
	m := board(t, "automations: []\n")
	if got := m.View(); !strings.Contains(got, "herdr-automations add") {
		t.Errorf("view = %q, want it to say how to add one", got)
	}
}

func TestALongNoticeIsCutToTheWidthOfThePane(t *testing.T) {
	// A herdr API error is long enough to blow up the pane otherwise.
	m := board(t, oneGoodOneBroken)
	m.width = 40
	m.setNotice(failStyle, strings.Repeat("very long error ", 20))

	if got := len([]rune(m.noticeLine())); got > 40 {
		t.Fatalf("notice line is %d runes wide in a 40-wide pane", got)
	}
}

func TestANoticeIsFlattenedToOneLine(t *testing.T) {
	m := board(t, oneGoodOneBroken)
	m.setNotice(failStyle, "first line\nsecond line")

	if strings.Contains(m.notice, "\n") {
		t.Errorf("notice = %q, want it on one line", m.notice)
	}
}

func TestJumpingToARunInAnotherSessionSaysWhereItIs(t *testing.T) {
	m := board(t, `automations:
  - {name: fine, cron: "@daily", repo: /x, prompt: p}
`)
	if err := history.Append(history.Record{RunID: "fine-1", Automation: "fine",
		Status: history.StatusDone, Session: "automations", WorkspaceID: "w7"}); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, refreshMsg{})
	m.here = "default"

	m, cmd := press(t, m, "enter")
	if cmd != nil {
		t.Fatal("the board quit, as if it had jumped somewhere")
	}
	if !strings.Contains(m.notice, "herdr session attach automations") || strings.Contains(m.notice, "closed") {
		t.Fatalf("notice = %q, want where to attach, not that it was closed", m.notice)
	}
}
