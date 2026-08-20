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
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	failStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
)

type row struct {
	auto config.Automation
	last *history.Record
}

type model struct {
	rows        []row
	cursor      int
	width       int
	err         error
	notice      string
	noticeStyle lipgloss.Style
	// pending holds worktrees waiting on a y/n. Non-empty means the board is
	// asking, and every other key is suspended until it is answered.
	pending []cleanup.Candidate
}

type refreshMsg struct{}
type ranMsg struct{ err error }
type editedMsg struct{ err error }
type scannedMsg struct {
	removable []cleanup.Candidate
	err       error
}
type cleanedMsg struct {
	removed int
	err     error
}

// scanCleanup finds the run worktrees whose work already landed. Everything
// else -- open workspaces, unmerged commits -- is left alone and unmentioned:
// the board offers a tidy-up, not a verdict on your branches.
func scanCleanup() tea.Msg {
	cfg, err := config.Load()
	if err != nil {
		return scannedMsg{err: err}
	}
	candidates, err := cleanup.Scan(cfg)
	if err != nil {
		return scannedMsg{err: err}
	}
	var removable []cleanup.Candidate
	for _, c := range candidates {
		if c.Removable() {
			removable = append(removable, c)
		}
	}
	return scannedMsg{removable: removable}
}

// removeAll reports the first refusal rather than a tally: git declines a dirty
// checkout or an unmerged branch, and that is worth reading in full.
func removeAll(candidates []cleanup.Candidate) tea.Msg {
	removed := 0
	for _, c := range candidates {
		if err := cleanup.Remove(c); err != nil {
			return cleanedMsg{removed: removed, err: err}
		}
		removed++
	}
	return cleanedMsg{removed: removed}
}

func Run() error {
	_, err := tea.NewProgram(load(), tea.WithAltScreen()).Run()
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
		m.rows = append(m.rows, row{auto: a, last: last})
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
		if len(msg.removable) == 0 {
			m.setNotice(dimStyle, "no run worktree is finished with — nothing to remove")
			return m, nil
		}
		// Held until answered: the board asks the same question the CLI does
		// rather than deleting because a key was pressed.
		m.pending = msg.removable
		m.setNotice(dimStyle, fmt.Sprintf(
			"remove %d merged worktree(s) and their branches? y/n", len(msg.removable)))
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
		if len(m.pending) > 0 {
			pending := m.pending
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
				a := m.rows[m.cursor].auto
				m.setNotice(dimStyle, "running "+a.Name+"…")
				return m, func() tea.Msg { return ranMsg{err: runner.Run(a, "manual")} }
			}
		case "e":
			// Open the YAML in $EDITOR at the selected automation's line,
			// taking over the pane until the editor exits.
			line := 0
			if m.cursor < len(m.rows) {
				line = config.LineOf(m.rows[m.cursor].auto.Name)
			}
			cmd := editorCommand(config.Path(), line)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return editedMsg{err} })
		case "enter":
			// Jump to the workspace the last run happened in, and close the
			// board so the agent lands in front of you.
			if m.cursor < len(m.rows) {
				last := m.rows[m.cursor].last
				if last == nil || last.WorkspaceID == "" {
					m.setNotice(dimStyle, "no run to jump to yet")
					return m, nil
				}
				if err := herdr.Focus(last.WorkspaceID, last.PaneID); err != nil {
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
		dimStyle.Render("  r: run · enter: jump to last run · e: edit · c: cleanup · j/k: move · q: quit") + "\n\n"
	if m.err != nil {
		return s + failStyle.Render("config error: "+m.err.Error()) + "\n"
	}
	if len(m.rows) == 0 {
		return s + dimStyle.Render("No automations yet — run `herdr-automations add` or edit "+config.Path()) + "\n"
	}
	for i, r := range m.rows {
		// Pad the plain text first: styling before padding would make the
		// escape codes count toward the column widths.
		name := fmt.Sprintf("%-24s", truncate(r.auto.Name, 24))
		cron := fmt.Sprintf("%-16s", truncate(r.auto.Cron, 16))
		status := fmt.Sprintf("%-8s", statusText(r))
		next := nextRun(r.auto)
		if r.auto.Disabled {
			next = "(disabled)"
		}

		var line string
		switch {
		case i == m.cursor:
			// One reverse-video span over the whole row: any nested color
			// would end the highlight mid-line.
			line = selectedStyle.Render(" " + name + " " + cron + " " + status + " " + next + " ")
		case r.auto.Disabled:
			line = dimStyle.Render(" " + name + " " + cron + " " + status + " " + next)
		default:
			line = " " + name + " " + cron + " " +
				statusStyle(r).Render(status) + " " + dimStyle.Render(next)
		}
		s += line + "\n"
	}
	if m.notice != "" {
		s += "\n" + m.noticeStyle.Render(m.noticeLine()) + "\n"
	}
	return s
}

func statusText(r row) string {
	if r.last == nil {
		return "never"
	}
	return string(r.last.Status)
}

func statusStyle(r row) lipgloss.Style {
	if r.last == nil {
		return dimStyle
	}
	switch r.last.Status {
	case history.StatusFailed:
		return failStyle
	case history.StatusDone:
		return okStyle
	default:
		return lipgloss.NewStyle()
	}
}

func nextRun(a config.Automation) string {
	sched, err := config.CronParser.Parse(a.Cron)
	if err != nil {
		return ""
	}
	return "next " + sched.Next(time.Now()).Format("Mon 15:04")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
