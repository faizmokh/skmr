package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/skills"
	"github.com/faizmokh/skmr/internal/terminal"
)

var (
	cyan        = lipgloss.Color("80")
	blue        = lipgloss.Color("24")
	green       = lipgloss.Color("78")
	yellow      = lipgloss.Color("220")
	red         = lipgloss.Color("203")
	mutedColor  = lipgloss.Color("242")
	borderColor = lipgloss.Color("238")

	brandStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(cyan).Bold(true)
	accentStyle   = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	mutedStyle    = lipgloss.NewStyle().Foreground(mutedColor)
	labelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(blue).Bold(true)
	warningStyle  = lipgloss.NewStyle().Foreground(yellow)
	errorStyle    = lipgloss.NewStyle().Foreground(red)
	successStyle  = lipgloss.NewStyle().Foreground(green)
	keyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("237")).Bold(true)
)

const wideLayoutWidth = 76

type shortcut struct {
	key, label, event string
}

func (m Model) View() string {
	w, h := m.width, m.height
	if w < 30 || h < 10 {
		return "skmr\nTerminal too small (minimum 30 × 10).\nq quit"
	}
	header := m.header(w)
	toolbar := m.toolbar(w)
	bodyHeight := m.bodyHeight()
	var body string
	if m.pending != nil {
		body = m.reviewView(w, bodyHeight)
	} else if w >= wideLayoutWidth {
		left := m.listWidth()
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.listPane(left, bodyHeight), m.detailPane(w-left, bodyHeight))
	} else {
		listHeight := m.stackedListHeight()
		body = m.listPane(w, listHeight) + "\n" + m.detailPane(w, max(3, bodyHeight-listHeight))
	}
	return strings.Join([]string{header, toolbar, body, m.statusLine(w), m.footer(w)}, "\n")
}

func (m Model) header(width int) string {
	scope := strings.ToUpper(m.service.Scope())
	location := "user library"
	if m.service.Scope() == "project" {
		location = filepath.Base(m.service.Config.Project)
		if location == "." || location == string(filepath.Separator) {
			location = m.service.Config.Project
		}
	}
	left := brandStyle.Render(" SKMR ") + " " + accentStyle.Render(scope) + " " + mutedStyle.Render(terminal.Safe(location))
	warnings := len(m.result.Issues)
	for _, skill := range m.result.Skills {
		warnings += len(skill.Issues)
	}
	right := fmt.Sprintf("%d skills", len(m.result.Skills))
	if warnings > 0 {
		right += "  " + warningStyle.Render(fmt.Sprintf("! %d", warnings))
	}
	return align(left, right, width)
}

func (m Model) toolbar(width int) string {
	names := []string{"all", "managed", "discovered", "inherited"}
	parts := []string{mutedStyle.Render("FILTER")}
	for i, name := range names {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == m.filter {
			parts = append(parts, selectedStyle.Render(" "+label+" "))
		} else {
			parts = append(parts, mutedStyle.Render(label))
		}
	}
	query := "press / to search"
	if m.query != "" || m.searching {
		query = "/ " + terminal.Safe(m.query)
		if m.searching {
			query += accentStyle.Render("█")
		}
	}
	return align(strings.Join(parts, "  "), mutedStyle.Render(query), width)
}

func (m Model) bodyHeight() int { return max(6, m.height-4) }

func (m Model) listWidth() int { return min(44, max(32, m.width*2/5)) }

func (m Model) stackedListHeight() int {
	return min(max(4, len(m.items())+2), max(3, m.bodyHeight()/2))
}

func (m Model) filterAt(x int) int {
	position := ansi.StringWidth("FILTER  ")
	for i, name := range []string{"all", "managed", "discovered", "inherited"} {
		width := ansi.StringWidth(fmt.Sprintf("%d %s", i+1, name))
		if i == m.filter {
			width += 2
		}
		if x >= position && x < position+width {
			return i
		}
		position += width + 2
	}
	return -1
}

func (m Model) searchStart() int {
	query := "press / to search"
	if m.query != "" || m.searching {
		query = "/ " + terminal.Safe(m.query)
		if m.searching {
			query += "█"
		}
	}
	return max(0, m.width-ansi.StringWidth(query))
}

func (m Model) listPane(width, height int) string {
	return frame(fmt.Sprintf("Skills %d/%d", len(m.items()), len(m.result.Skills)), m.listLines(width-2, height-2), width, height, true)
}

func (m Model) listLines(width, height int) []string {
	items := m.items()
	if len(items) == 0 {
		return fill([]string{"", mutedStyle.Render("  No matching skills"), "", "  Esc clear search  ·  f change filter"}, width, height)
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
		state, style := compactState(s)
		nameWidth := max(4, width-ansi.StringWidth(state)-3)
		name := ansi.Truncate(terminal.Safe(s.Name), nameWidth, "…")
		text := align(prefix+name, style.Render(state), width)
		if i == m.cursor {
			text = selectedStyle.Render(pad(text, width))
		}
		lines = append(lines, text)
	}
	return fill(lines, width, height)
}

func (m Model) detailPane(width, height int) string {
	title := "Details"
	if m.showHelp {
		title = "Help"
	}
	return frame(title, m.detailLines(width-2, height-2), width, height, false)
}

func (m Model) detailLines(width, height int) []string {
	if m.showHelp {
		lines := []string{
			accentStyle.Render("Navigate"),
			"  ↑/k  up       ↓/j  down       g/G  first/last",
			"  PgUp/PgDn scroll instructions",
			"",
			accentStyle.Render("Find and organize"),
			"  /  search      f  cycle filter  Tab  switch scope",
			"  R  refresh",
			"",
			accentStyle.Render("Manage selected skill"),
			"  a  adopt       e  enable        d  disable",
			"  c  resolve     r  restore       ?  close help",
			"  q  quit",
			"",
			accentStyle.Render("Mouse"),
			"  Click skills, filters, scope, search, or footer actions",
			"  Scroll the list or the detail/review pane",
			"",
			mutedStyle.Render("Recovery: skmr doctor --recover"),
		}
		return scroll(fillWrapped(lines, width), width, height, m.offset)
	}
	s, ok := m.selected()
	if !ok {
		return fill([]string{"", mutedStyle.Render("  Nothing to show"), "", "  Esc clear search  ·  f change filter"}, width, height)
	}
	state, stateStyle := compactState(s)
	lines := []string{align(accentStyle.Render(terminal.Safe(s.Name)), stateStyle.Render(state), width)}
	if s.Description != "" {
		lines = append(lines, wrap(terminal.Safe(s.Description), width)...)
	}
	lines = append(lines, "")
	lines = append(lines, field("Source", terminal.Safe(s.Path), width)...)
	lines = append(lines, field("Scope", terminal.Safe(s.Scope), width)...)
	lines = append(lines, field("Agents", terminal.Safe(strings.Join(s.Agents, ", ")), width)...)
	lines = append(lines, field("ID", terminal.Safe(s.ID), width)...)
	if s.ConflictKind != "" {
		lines = append(lines, field("Conflict", fmt.Sprintf("%s · %d copies", s.ConflictKind, s.ConflictCount), width)...)
		if s.Canonical {
			lines = append(lines, field("Canonical", "yes", width)...)
		}
		for _, path := range s.Unresolved {
			lines = append(lines, field("External", terminal.Safe(path), width)...)
		}
	}
	if len(s.Issues) > 0 {
		lines = append(lines, "", warningStyle.Render(fmt.Sprintf("! Problems (%d)", len(s.Issues))))
		for _, issue := range s.Issues {
			lines = append(lines, wrap("  "+terminal.Safe(issue), width)...)
		}
	}
	lines = append(lines, "", section("SKILL.md", width))
	content := strings.ReplaceAll(terminal.Safe(m.content), "\t", "    ")
	if content == "" {
		content = "Loading preview…"
	}
	lines = append(lines, wrap(content, width)...)
	return scroll(lines, width, height, m.offset)
}

func (m Model) reviewView(width, height int) string {
	title := "Confirm " + m.pending.Action
	lines := []string{
		warningStyle.Render("Review every filesystem change before applying."),
		"",
	}
	lines = append(lines, wrap(terminal.Safe(m.pending.String()), max(1, width-4))...)
	lines = append(lines, "", successStyle.Render("y apply changes")+"  "+mutedStyle.Render("n/Esc cancel"))
	return frame(title, scroll(lines, width-2, height-2, m.offset), width, height, true)
}

func (m Model) statusLine(width int) string {
	message := terminal.Safe(m.message)
	style := mutedStyle
	prefix := "●"
	if m.busy {
		prefix = "◌"
		style = accentStyle
	} else if strings.HasPrefix(message, "Done:") {
		prefix = "✓"
		style = successStyle
	} else if strings.Contains(strings.ToLower(message), "could not") || strings.Contains(strings.ToLower(message), "error") {
		prefix = "!"
		style = errorStyle
	}
	if len(m.result.Issues) > 0 {
		message += "  " + fmt.Sprintf("! %s", terminal.Safe(m.result.Issues[0]))
	}
	return ansi.Truncate(style.Render(prefix+" "+message), width, "…")
}

func (m Model) footer(width int) string {
	return hints(width, m.shortcuts())
}

func (m Model) shortcuts() []shortcut {
	if m.pending != nil {
		return []shortcut{{"y", "apply", "y"}, {"n", "cancel", "n"}, {"PgUp/Dn", "review", ""}, {"q", "quit", "q"}}
	}
	if m.searching {
		return []shortcut{{"type", "search", ""}, {"Enter", "accept", "enter"}, {"Esc", "finish", "esc"}, {"⌫", "delete", "backspace"}}
	}
	toggle := "e"
	if skill, ok := m.selected(); ok && skill.Managed && skill.Enabled {
		toggle = "d"
	}
	return []shortcut{{"↑↓", "select", ""}, {"/", "search", "/"}, {"a", "adopt", "a"}, {"c", "resolve", "c"}, {"e/d", "toggle", toggle}, {"r", "restore", "r"}, {"?", "help", "?"}, {"q", "quit", "q"}}
}

func (m Model) footerKeyAt(x int) string {
	position := 0
	for _, item := range m.shortcuts() {
		width := ansi.StringWidth(" " + item.key + "  " + item.label)
		if x >= position && x < position+width {
			return item.event
		}
		position += width + 2
	}
	return ""
}

func compactState(skill skills.Skill) (string, lipgloss.Style) {
	if skill.ConflictKind != "" {
		return "! " + skill.ConflictKind, warningStyle
	}
	if len(skill.Issues) > 0 {
		return "! warning", warningStyle
	}
	if skill.Inherited {
		return "↳ inherited", mutedStyle
	}
	if skill.ReadOnly {
		return "◇ read-only", mutedStyle
	}
	if skill.Managed && skill.Enabled {
		return "● enabled", successStyle
	}
	if skill.Managed {
		return "○ disabled", mutedStyle
	}
	return "· discovered", labelStyle
}

func frame(title string, lines []string, width, height int, active bool) string {
	width = max(3, width)
	height = max(3, height)
	inner := width - 2
	color := borderColor
	if active {
		color = cyan
	}
	border := lipgloss.NewStyle().Foreground(color)
	titleText := ansi.Truncate(" "+terminal.Safe(title)+" ", max(1, inner-1), "…")
	topFill := max(0, inner-ansi.StringWidth(titleText))
	out := []string{border.Render("╭─") + accentStyle.Render(titleText) + border.Render(strings.Repeat("─", max(0, topFill-1))+"╮")}
	lines = fill(lines, inner, height-2)
	for _, line := range lines {
		out = append(out, border.Render("│")+pad(line, inner)+border.Render("│"))
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(out, "\n")
}

func field(label, value string, width int) []string {
	prefix := labelStyle.Render(fmt.Sprintf("%-7s", label))
	available := max(1, width-7)
	parts := wrap(value, available)
	lines := make([]string, 0, len(parts))
	for i, part := range parts {
		if i == 0 {
			lines = append(lines, prefix+part)
		} else {
			lines = append(lines, strings.Repeat(" ", 7)+part)
		}
	}
	return lines
}

func section(title string, width int) string {
	text := " " + title + " "
	return labelStyle.Render(text + strings.Repeat("─", max(0, width-len(text))))
}

func hints(width int, values []shortcut) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, keyStyle.Render(" "+value.key+" ")+" "+mutedStyle.Render(value.label))
	}
	return ansi.Truncate(strings.Join(parts, "  "), width, "…")
}

func fillWrapped(lines []string, width int) []string {
	out := []string{}
	for _, line := range lines {
		out = append(out, wrap(line, width)...)
	}
	return out
}

func wrap(value string, width int) []string {
	return strings.Split(ansi.Hardwrap(value, max(1, width), true), "\n")
}

func scroll(lines []string, width, height, offset int) []string {
	offset = min(max(0, offset), max(0, len(lines)-height))
	lines = lines[offset:min(len(lines), offset+height)]
	return fill(lines, width, height)
}

func fill(lines []string, width, height int) []string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return lines
}

func align(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return ansi.Truncate(left, max(1, width-ansi.StringWidth(right)-1), "…") + " " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

func pad(value string, width int) string {
	value = ansi.Truncate(value, width, "…")
	return value + strings.Repeat(" ", max(0, width-ansi.StringWidth(value)))
}
