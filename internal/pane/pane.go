// Package pane is the plugin's Herdr overlay pane: a live board of
// automations with their schedule and last run, plus one-key "run now".
package pane

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/DnzzL/herdr-automations/internal/cleanup"
	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/runner"
	"github.com/DnzzL/herdr-automations/internal/schedule"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	failStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
)

// row is one entry in automations.yaml as the board sees it: either an
// automation that loaded, or a diagnostic saying why one didn't. A broken entry
// gets a row so it shows up as broken rather than as a hole in the board.
type row struct {
	name string
	// line is where the entry starts, for `e`. 0 means "open the top".
	line int
	auto config.Automation
	last *history.Record
	diag *config.Diagnostic
}

// broke reports whether this row is an entry that never loaded.
func (r row) broke() bool { return r.diag != nil }

// status is the word in the status column.
func (r row) status() string {
	switch {
	case r.broke():
		return string(history.StatusInvalid)
	case r.last == nil:
		return "never"
	default:
		return string(r.last.Status)
	}
}

// style colours the status. A cancelled run is dimmed, not reddened: somebody
// closed it on purpose and there is nothing here to alarm about.
func (r row) style() lipgloss.Style {
	switch {
	case r.broke():
		return failStyle
	case r.last == nil:
		return dimStyle
	}
	switch r.last.Status {
	case history.StatusFailed:
		return failStyle
	case history.StatusDone:
		return okStyle
	case history.StatusCancelled:
		return dimStyle
	default:
		return lipgloss.NewStyle()
	}
}

// detail is the trailing column: when it next runs, or why it never will.
func (r row) detail() string {
	switch {
	case r.broke():
		return r.diag.String()
	case r.auto.Disabled:
		return "(disabled)"
	}
	next, ok := schedule.NextRun(r.auto, time.Now())
	if !ok {
		return ""
	}
	return "next " + next.Format("Mon 15:04")
}

// schedule is the cron column. Meaningless when the cron is the broken bit.
func (r row) schedule() string {
	if r.broke() {
		return "—"
	}
	return r.auto.Cron
}

type model struct {
	// runs is the board's Runner: `r` runs an automation through the same
	// lifecycle the daemon uses, in-flight set included.
	runs *runner.Runner
	// here is the Herdr session the board runs in. A run's workspace can only
	// be focused from its own session; empty means unknown, and then the board
	// tries anyway, as it did before runs recorded where they live.
	here        string
	rows        []row
	cursor      int
	width       int
	err         error
	notice      string
	noticeStyle lipgloss.Style
	// pending holds the plan waiting on a y/n. Non-nil means the board is
	// asking, and every other key is suspended until it is answered.
	pending *cleanup.Plan
}

type refreshMsg struct{}
type ranMsg struct{ err error }
type editedMsg struct{ err error }
type scannedMsg struct {
	plan cleanup.Plan
	err  error
}
type cleanedMsg struct {
	removed int
	err     error
}

// scanCleanup finds the run worktrees whose work already landed. Everything
// else -- open workspaces, unmerged commits -- is left alone: the board offers
// a tidy-up, not a verdict on your branches.
func scanCleanup() tea.Msg {
	cfg, err := config.Load()
	if err != nil {
		return scannedMsg{err: err}
	}
	plan, err := cleanup.Scan(cfg)
	if err != nil {
		return scannedMsg{err: err}
	}
	return scannedMsg{plan: plan}
}

func removeAll(plan cleanup.Plan) tea.Msg {
	removed, err := plan.Apply(nil)
	return cleanedMsg{removed: removed, err: err}
}

// togglePause flips Disabled for the named automation and saves the config.
// It reports through editedMsg so the board reloads exactly as it does after
// `e`: the file on disk is the source of truth, not the in-memory row.
func togglePause(name string) tea.Msg {
	cfg, err := config.Load()
	if err != nil {
		return editedMsg{err: err}
	}
	a := cfg.Find(name)
	if a == nil {
		return editedMsg{err: fmt.Errorf("no automation named %q", name)}
	}
	a.Disabled = !a.Disabled
	return editedMsg{err: config.Save(cfg)}
}

func Run() error {
	m := load()
	m.runs = runner.Default()
	m.here = herdr.Client{}.CurrentSession()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func load() model {
	m := model{}
	cfg, err := config.Load()
	if err != nil {
		m.err = err
		return m
	}
	for _, a := range cfg.Automations {
		last, _ := history.LastRun(a.Name)
		m.rows = append(m.rows, row{name: a.Name, line: a.Line, auto: a, last: last})
	}
	// The entries that did not load go on the board too. A typo used to blank
	// the whole board with "config error"; now it costs one red row.
	for i := range cfg.Invalid {
		d := cfg.Invalid[i]
		name := d.Name
		if name == "" {
			name = "(unnamed)"
		}
		m.rows = append(m.rows, row{name: name, line: d.Line, diag: &d})
	}
	return m
}

func (m model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return refreshMsg{} })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case refreshMsg:
		next := load()
		next.runs, next.here = m.runs, m.here
		if next.cursor = m.cursor; next.cursor >= len(next.rows) {
			next.cursor = max(0, len(next.rows)-1)
		}
		next.notice, next.noticeStyle = m.notice, m.noticeStyle
		next.width = m.width
		// The two-second refresh rebuilds the model; a question already on
		// screen has to survive it, or answering it would land on nothing.
		next.pending = m.pending
		return next, tick()
	case editedMsg:
		next := load() // pick up whatever was just saved, including new entries
		next.runs, next.here = m.runs, m.here
		next.cursor = min(m.cursor, max(0, len(next.rows)-1))
		if msg.err != nil {
			next.setNotice(failStyle, msg.err.Error())
		} else if next.err == nil {
			next.setNotice(okStyle, "config reloaded")
		}
		return next, tick()
	case ranMsg:
		if msg.err != nil {
			m.setNotice(failStyle, msg.err.Error())
		} else {
			m.setNotice(okStyle, "run finished")
		}
		return m, nil
	case scannedMsg:
		if msg.err != nil {
			m.setNotice(failStyle, msg.err.Error())
			return m, nil
		}
		if len(msg.plan.Removable) == 0 {
			m.setNotice(dimStyle, msg.plan.Summary())
			return m, nil
		}
		// Held until answered: the board asks the same question the CLI does
		// rather than deleting because a key was pressed.
		plan := msg.plan
		m.pending = &plan
		m.setNotice(dimStyle, fmt.Sprintf(
			"remove %d merged worktree(s) and their branches? y/n", len(plan.Removable)))
		return m, nil
	case cleanedMsg:
		m.pending = nil
		if msg.err != nil {
			m.setNotice(failStyle, msg.err.Error())
		} else {
			m.setNotice(okStyle, fmt.Sprintf("removed %d worktree(s)", msg.removed))
		}
		return m, nil
	case tea.KeyMsg:
		// A pending confirmation owns the keyboard until it is answered, so no
		// stray j/k acts on a board that is asking a question.
		if m.pending != nil {
			pending := *m.pending
			if msg.String() == "y" {
				m.setNotice(dimStyle, "removing…")
				return m, func() tea.Msg { return removeAll(pending) }
			}
			m.pending = nil
			m.setNotice(dimStyle, "nothing removed")
			return m, nil
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "c":
			// Scanning shells out to git per branch, so it happens on the
			// keystroke rather than on every two-second refresh.
			m.setNotice(dimStyle, "scanning run worktrees…")
			return m, scanCleanup
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case "r":
			if m.cursor < len(m.rows) {
				if d := m.rows[m.cursor].diag; d != nil {
					m.setNotice(failStyle, d.String())
					return m, nil
				}
				a := m.rows[m.cursor].auto
				m.setNotice(dimStyle, "running "+a.Name+"…")
				runs := m.runs
				return m, func() tea.Msg { return ranMsg{err: runs.Run(a, history.TriggerManual)} }
			}
		case "p":
			if m.cursor < len(m.rows) {
				r := m.rows[m.cursor]
				if r.broke() {
					m.setNotice(failStyle, r.diag.String())
					return m, nil
				}
				verb := "pausing "
				if r.auto.Disabled {
					verb = "resuming "
				}
				m.setNotice(dimStyle, verb+r.name+"…")
				name := r.name
				return m, func() tea.Msg { return togglePause(name) }
			}
		case "e":
			// Open the YAML in $EDITOR at the selected automation's line,
			// taking over the pane until the editor exits.
			line := 0
			if m.cursor < len(m.rows) {
				line = m.rows[m.cursor].line
			}
			cmd := editorCommand(config.Path(), line)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return editedMsg{err} })
		case "enter":
			// Jump to the workspace the last run happened in, and close the
			// board so the agent lands in front of you.
			if m.cursor < len(m.rows) {
				if m.rows[m.cursor].broke() {
					m.setNotice(dimStyle, "this entry never ran — press e to fix it")
					return m, nil
				}
				last := m.rows[m.cursor].last
				if last == nil || last.WorkspaceID == "" {
					m.setNotice(dimStyle, "no run to jump to yet")
					return m, nil
				}
				// Herdr cannot move an attached client to another session,
				// and asking this one for that workspace would say "not found".
				if last.Session != "" && m.here != "" && last.Session != m.here {
					m.setNotice(dimStyle, "that run lives in session "+last.Session+
						" — herdr session attach "+last.Session)
					return m, nil
				}
				if err := (herdr.Client{}).Focus(last.WorkspaceID, last.PaneID); err != nil {
					if errors.Is(err, herdr.ErrGone) {
						m.setNotice(dimStyle,
							"workspace "+last.WorkspaceID+" was closed — nothing to jump to")
					} else {
						m.setNotice(failStyle, err.Error())
					}
					return m, nil
				}
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// setNotice stores the message as plain text; View decides how much of it
// fits. A herdr API error is long enough to blow up the pane otherwise.
func (m *model) setNotice(style lipgloss.Style, text string) {
	m.notice = strings.Join(strings.Fields(text), " ")
	m.noticeStyle = style
}

func (m model) noticeLine() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	return truncate(m.notice, width-2)
}

func (m model) View() string {
	s := titleStyle.Render("Automations") +
		dimStyle.Render("  r: run · p: pause/resume · enter: jump to last run · e: edit · c: cleanup · j/k: move · q: quit") + "\n\n"
	if m.err != nil {
		return s + failStyle.Render("config error: "+m.err.Error()) + "\n"
	}
	if len(m.rows) == 0 {
		return s + dimStyle.Render("No automations yet — run `herdr-automations add` or edit "+config.Path()) + "\n"
	}
	for i, r := range m.rows {
		// Pad the plain text first: styling before padding would make the
		// escape codes count toward the column widths.
		name := fmt.Sprintf("%-24s", truncate(r.name, 24))
		cron := fmt.Sprintf("%-16s", truncate(r.schedule(), 16))
		status := fmt.Sprintf("%-8s", r.status())
		detail := r.detail()

		var line string
		switch {
		case i == m.cursor:
			// One reverse-video span over the whole row: any nested color
			// would end the highlight mid-line.
			line = selectedStyle.Render(" " + name + " " + cron + " " + status + " " + detail + " ")
		case r.auto.Disabled:
			line = dimStyle.Render(" " + name + " " + cron + " " + status + " " + detail)
		default:
			line = " " + name + " " + cron + " " +
				r.style().Render(status) + " " + dimStyle.Render(detail)
		}
		s += line + "\n"
	}
	if m.notice != "" {
		s += "\n" + m.noticeStyle.Render(m.noticeLine()) + "\n"
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
