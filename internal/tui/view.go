package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/terminal"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
var selected = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24"))

func (m Model) View() string {
	w, h := m.width, m.height
	if w < 30 || h < 10 {
		return "skmr\nEnlarge the terminal (30 × 10).\nq quit"
	}
	label := m.service.Scope()
	if label == "project" {
		label += " · " + m.service.Config.Project
	}
	header := accent.Render("skmr") + "  " + terminal.Safe(label)
	filter := []string{"all skills", "managed", "discovered", "inherited"}[m.filter]
	query := m.query
	if m.searching {
		query += "▏"
	}
	search := fmt.Sprintf("%s · %d skills   / %s", filter, len(m.items()), query)
	bodyHeight := max(3, h-7)
	var body string
	if m.pending != nil {
		text := "Review " + m.pending.Action + "\n\n" + m.pending.String() + "\n\nApply these changes?  y apply · n/Esc cancel"
		body = panel(text, w-2, bodyHeight, m.offset)
	} else if w >= 90 {
		left := min(42, w/3)
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.listView(left, bodyHeight), "  ", panel(m.detail(), w-left-2, bodyHeight, m.offset))
	} else {
		listHeight := max(2, bodyHeight/3)
		body = m.listView(w, listHeight) + "\n" + panel(m.detail(), w, max(1, bodyHeight-listHeight-1), m.offset)
	}
	status := terminal.Safe(m.message)
	if m.busy {
		status = "Working… " + status
	}
	if len(m.result.Issues) > 0 {
		status += "  Warning: " + terminal.Safe(m.result.Issues[0])
	}
	footer := "↑↓/jk select · / search · Tab scope · f filter · ? help · q quit"
	actions := "a adopt · e enable · d disable · r restore · PgUp/PgDn details · R refresh"
	if m.pending != nil {
		actions = "y apply changes · n/Esc cancel · PgUp/PgDn review"
		footer = "Review the file changes before applying."
	}
	return strings.Join([]string{ansi.Truncate(header, w, "…"), ansi.Truncate(terminal.Safe(search), w, "…"), body, muted.Render(ansi.Truncate(status, w, "…")), ansi.Truncate(actions, w, "…"), muted.Render(ansi.Truncate(footer, w, "…"))}, "\n")
}
func (m Model) listView(width, height int) string {
	items := m.items()
	if len(items) == 0 {
		return panel("No skills found.\nAdd a SKILL.md folder to a standard skill directory, then press R.", width, height, 0)
	}
	start := max(0, m.cursor-height/2)
	start = min(start, max(0, len(items)-height))
	lines := []string{}
	for i := start; i < len(items) && len(lines) < height; i++ {
		s := items[i]
		prefix := "  "
		if i == m.cursor {
			prefix = "› "
		}
		text := ansi.Truncate(terminal.Safe(prefix+s.Name+"  ["+skillState(s)+"]"), width, "…")
		if i == m.cursor {
			text = selected.Render(text)
		}
		lines = append(lines, text)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}
func panel(text string, width, height, offset int) string {
	text = strings.ReplaceAll(terminal.Safe(text), "\t", "    ")
	lines := strings.Split(ansi.Hardwrap(text, max(1, width), true), "\n")
	offset = min(max(0, offset), max(0, len(lines)-height))
	lines = lines[offset:min(len(lines), offset+height)]
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}
