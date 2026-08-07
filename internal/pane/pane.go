// Package pane is the plugin's Herdr overlay pane: a live board of
// automations with their schedule and last run, plus one-key "run now".
package pane

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	rows   []row
	cursor int
	err    error
	notice string
}

type refreshMsg struct{}
type ranMsg struct{ err error }

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
	case refreshMsg:
		next := load()
		if next.cursor = m.cursor; next.cursor >= len(next.rows) {
			next.cursor = max(0, len(next.rows)-1)
		}
		next.notice = m.notice
		return next, tick()
	case ranMsg:
		if msg.err != nil {
			m.notice = failStyle.Render(msg.err.Error())
		} else {
			m.notice = okStyle.Render("run finished")
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
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
				m.notice = "running " + a.Name + "…"
				return m, func() tea.Msg { return ranMsg{err: runner.Run(a, "manual")} }
			}
		case "enter":
			// Jump to the workspace the last run happened in, and close the
			// board so the agent lands in front of you.
			if m.cursor < len(m.rows) {
				last := m.rows[m.cursor].last
				if last == nil || last.WorkspaceID == "" {
					m.notice = dimStyle.Render("no run to jump to yet")
					return m, nil
				}
				if err := herdr.Focus(last.WorkspaceID, last.PaneID); err != nil {
					if errors.Is(err, herdr.ErrGone) {
						m.notice = dimStyle.Render(
							"workspace " + last.WorkspaceID + " was closed — nothing to jump to")
					} else {
						m.notice = failStyle.Render(err.Error())
					}
					return m, nil
				}
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m model) View() string {
	s := titleStyle.Render("Automations") +
		dimStyle.Render("  r: run now · enter: jump to last run · j/k: move · q: quit") + "\n\n"
	if m.err != nil {
		return s + failStyle.Render("config error: "+m.err.Error()) + "\n"
	}
	if len(m.rows) == 0 {
		return s + dimStyle.Render("No automations yet — run `herdr-automations add` or edit "+config.Path()) + "\n"
	}
	for i, r := range m.rows {
		line := fmt.Sprintf(" %-24s %-16s %-10s %s",
			truncate(r.auto.Name, 24), r.auto.Cron, statusLabel(r), nextRun(r.auto))
		if r.auto.Disabled {
			line = dimStyle.Render(line + "  (disabled)")
		}
		if i == m.cursor {
			line = selectedStyle.Render(line)
		}
		s += line + "\n"
	}
	if m.notice != "" {
		s += "\n" + m.notice + "\n"
	}
	return s
}

func statusLabel(r row) string {
	if r.last == nil {
		return dimStyle.Render("never")
	}
	label := string(r.last.Status)
	switch r.last.Status {
	case history.StatusFailed:
		label = failStyle.Render(label)
	case history.StatusDone:
		label = okStyle.Render(label)
	}
	return label
}

func nextRun(a config.Automation) string {
	if a.Disabled {
		return ""
	}
	sched, err := config.CronParser.Parse(a.Cron)
	if err != nil {
		return ""
	}
	return dimStyle.Render("next " + sched.Next(time.Now()).Format("Mon 15:04"))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
