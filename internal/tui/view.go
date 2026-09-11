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
	cyan        = lipgloss.AdaptiveColor{Light: "30", Dark: "80"}
	blue        = lipgloss.AdaptiveColor{Light: "25", Dark: "24"}
	green       = lipgloss.AdaptiveColor{Light: "28", Dark: "78"}
	yellow      = lipgloss.AdaptiveColor{Light: "136", Dark: "220"}
	red         = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	mutedColor  = lipgloss.AdaptiveColor{Light: "244", Dark: "242"}
	borderColor = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
	labelColor  = lipgloss.AdaptiveColor{Light: "240", Dark: "245"}
	keyColor    = lipgloss.AdaptiveColor{Light: "238", Dark: "237"}
	brandText   = lipgloss.AdaptiveColor{Light: "231", Dark: "16"}

	brandStyle    = lipgloss.NewStyle().Foreground(brandText).Background(cyan).Bold(true)
	accentStyle   = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	mutedStyle    = lipgloss.NewStyle().Foreground(mutedColor)
	labelStyle    = lipgloss.NewStyle().Foreground(labelColor)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(blue).Bold(true)
	warningStyle  = lipgloss.NewStyle().Foreground(yellow)
	errorStyle    = lipgloss.NewStyle().Foreground(red)
	successStyle  = lipgloss.NewStyle().Foreground(green)
	keyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(keyColor).Bold(true)
)

const wideLayoutWidth = 76

type shortcut struct {
	key, label, event string
	essential         bool
}

type scrollMetadata struct {
	start, end, total int
	overflow          bool
	above, below      bool
}

func (m Model) View() string {
	w, h := m.width, m.height
	if w < 30 || h < 10 {
		return "skmr\nTerminal too small (minimum 30 × 10).\nq quit"
	}
	header := m.header(w)
	if m.pendingBatch != nil || m.migration != nil {
		bodyHeight := max(6, h-3)
		body := ""
		if m.pendingBatch != nil {
			body = m.batchReviewView(w, bodyHeight)
		} else {
			body = m.migrationView(w, bodyHeight)
		}
		return strings.Join([]string{header, body, m.statusLine(w), m.footer(w)}, "\n")
	}
	toolbar := m.toolbar(w)
	bodyHeight := m.bodyHeight()
	var body string
	if m.pending != nil || m.pendingTransfer != nil {
		body = m.reviewView(w, bodyHeight)
	} else if m.transferAction != "" {
		body = m.transferView(w, bodyHeight)
	} else if m.pane == paneInstructions {
		body = m.instructionsPane(w, bodyHeight)
	} else if m.pane == paneProblems {
		body = m.problemsPane(w, bodyHeight)
	} else if m.pane == paneDiff {
		body = m.diffPane(w, bodyHeight)
	} else if w >= wideLayoutWidth {
		left := m.listWidth()
		right := m.detailPane(w-left, bodyHeight)
		if m.pane == paneActions {
			right = m.actionsPane(w-left, bodyHeight)
		} else if m.pane == paneHelp {
			right = m.helpPane(w-left, bodyHeight)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.listPane(left, bodyHeight), right)
	} else {
		switch m.pane {
		case paneDetails:
			body = m.detailPane(w, bodyHeight)
		case paneActions:
			body = m.actionsPane(w, bodyHeight)
		case paneHelp:
			body = m.helpPane(w, bodyHeight)
		default:
			body = m.listPane(w, bodyHeight)
		}
	}
	sections := []string{header, toolbar}
	if m.showsViewDescription() {
		sections = append(sections, m.viewDescription(w))
	}
	sections = append(sections, body, m.statusLine(w), m.footer(w))
	return strings.Join(sections, "\n")
}

func (m Model) header(width int) string {
	return m.headerLayout(width).line
}

type renderedHeader struct {
	line                       string
	scopeStart, scopeEnd       int
	problemsStart, problemsEnd int
}

func (m Model) headerLayout(width int) renderedHeader {
	scope := strings.ToUpper(m.service.Scope())
	location := "user library"
	if m.service.Scope() == "project" {
		location = filepath.Base(m.service.Config.Project)
		if location == "." || location == string(filepath.Separator) {
			location = m.service.Config.Project
		}
	}
	prefix := brandStyle.Render(" SKMR ") + " " + labelStyle.Render("Skill library") + "  "
	scopeText := keyStyle.Render(" Tab ") + " " + accentStyle.Render(scope+":") + " " + mutedStyle.Render(terminal.Safe(location))
	if width < 60 {
		prefix = brandStyle.Render(" SKMR ") + " "
		scopeText = keyStyle.Render(" Tab ") + " " + accentStyle.Render(scope)
	}
	if m.busy {
		scopeText = accentStyle.Render(scope+":") + " " + mutedStyle.Render(terminal.Safe(location))
		if width < 60 {
			scopeText = accentStyle.Render(scope)
		}
	}
	left := prefix + scopeText
	right := fmt.Sprintf("%d skills", len(m.result.Skills))
	if m.inventory == inventoryLoading {
		right = "reading…"
	}
	problemText := ""
	if len(m.problems()) > 0 {
		problemText = m.problemSummary()
		if width < 80 {
			problemText = fmt.Sprintf("!%d", len(m.problems()))
		}
		right += "  " + warningStyle.Render(problemText)
	}
	rightWidth := ansi.StringWidth(right)
	leftWidth := max(1, width-rightWidth-1)
	displayedLeft := ansi.Truncate(left, leftWidth, "…")
	line := align(displayedLeft, right, width)
	scopeStart := ansi.StringWidth(prefix)
	scopeEnd := min(ansi.StringWidth(left), ansi.StringWidth(displayedLeft))
	if m.busy || scopeEnd <= scopeStart {
		scopeStart, scopeEnd = -1, -1
	}
	problemsStart, problemsEnd := -1, -1
	if problemText != "" {
		problemsEnd = width
		problemsStart = width - ansi.StringWidth(problemText)
	}
	return renderedHeader{line: line, scopeStart: scopeStart, scopeEnd: scopeEnd, problemsStart: problemsStart, problemsEnd: problemsEnd}
}

func (m Model) toolbar(width int) string {
	if m.searching {
		query := "/ " + terminal.Safe(m.query) + accentStyle.Render("█")
		return ansi.Truncate(mutedStyle.Render("SEARCH  ")+query, width, "…")
	}
	parts := []string{mutedStyle.Render("VIEW")}
	views := m.availableViews()
	if width < 60 {
		view := m.currentView()
		return ansi.Truncate(mutedStyle.Render("VIEW  ")+selectedStyle.Render(" "+view.shortcut+" "+view.labels.compact+" ")+"  "+mutedStyle.Render("f next"), width, "…")
	}
	for _, view := range views {
		label := view.shortcut + " " + view.labels.full
		if view.kind == m.filter {
			parts = append(parts, selectedStyle.Render(" "+label+" "))
		} else {
			parts = append(parts, mutedStyle.Render(label))
		}
	}
	left := strings.Join(parts, "  ")
	query := ""
	if m.query != "" {
		query = "/ " + terminal.Safe(m.query)
	} else if width >= 100 {
		query = "press / to search"
	}
	if query == "" || ansi.StringWidth(left)+ansi.StringWidth(query)+1 > width {
		return ansi.Truncate(left, width, "…")
	}
	return align(left, mutedStyle.Render(query), width)
}

func (m Model) viewDescription(width int) string {
	return ansi.Truncate(mutedStyle.Render(m.currentView().description), width, "…")
}

func (m Model) showsViewDescription() bool { return m.height >= 11 }

func (m Model) bodyTop() int {
	if m.showsViewDescription() {
		return 3
	}
	return 2
}

func (m Model) bodyHeight() int { return max(6, m.height-m.bodyTop()-2) }

func (m Model) listWidth() int { return min(44, max(38, m.width/2)) }

func (m Model) filterAt(x int) (skillView, bool) {
	if m.width < 60 || m.searching {
		return viewAll, false
	}
	position := ansi.StringWidth("VIEW  ")
	for _, view := range m.availableViews() {
		width := ansi.StringWidth(view.shortcut + " " + view.labels.full)
		if view.kind == m.filter {
			width += 2
		}
		if x >= position && x < position+width {
			return view.kind, true
		}
		position += width + 2
	}
	return viewAll, false
}

func (m Model) searchStart() int {
	if m.searching {
		return 0
	}
	query := ""
	if m.query != "" {
		query = "/ " + terminal.Safe(m.query)
	} else if m.width >= 100 {
		query = "press / to search"
	}
	if query == "" {
		return m.width
	}
	return max(0, m.width-ansi.StringWidth(query))
}

func (m Model) listPane(width, height int) string {
	title := fmt.Sprintf("Skills %d/%d", len(m.items()), len(m.result.Skills))
	if m.inventory == inventoryLoading {
		title = "Skills · reading…"
	}
	return frame(title, m.listLines(width-2, height-2), width, height, true)
}

func (m Model) listLines(width, height int) []string {
	items := m.rows()
	if len(items) == 0 {
		return fill(m.emptyListLines(width), width, height)
	}
	start := m.rowStart(height)
	lines := []string{}
	for i := start; i < len(items) && len(lines) < height; i++ {
		row := items[i]
		s := row.skill
		prefix := "  "
		if i == m.cursor {
			prefix = "› "
		}
		state, style := compactState(s)
		nameText := s.Name
		if row.kind == groupRow {
			nameText = row.group.Name
			disclosure := "▸ "
			if m.isExpanded(row.group.ID) {
				disclosure = "▾ "
			}
			prefix += disclosure
			state, style = skillCount(row.total), mutedStyle
			if len(row.members) != row.total {
				state = fmt.Sprintf("%d/%d skills", len(row.members), row.total)
			}
			for _, member := range m.result.Skills {
				if member.Group != nil && member.Group.ID == row.group.ID && (len(member.Issues) > 0 || member.ConflictID != "") {
					state = "! " + state
					style = warningStyle
					break
				}
			}
		} else if row.group != nil {
			prefix = "  " + prefix
		}
		nameWidth := max(4, width-ansi.StringWidth(state)-ansi.StringWidth(prefix)-1)
		name := ansi.Truncate(terminal.Safe(nameText), nameWidth, "…")
		text := align(prefix+name, style.Render(state), width)
		if i == m.cursor {
			text = selectedStyle.Render(pad(text, width))
		}
		lines = append(lines, text)
	}
	return fill(lines, width, height)
}

func (m Model) emptyListLines(width int) []string {
	if m.inventory == inventoryLoading {
		return fillWrapped([]string{"", "Reading skill folders…", "", mutedStyle.Render("Ownership and discovery state will appear here.")}, width)
	}
	if len(m.result.Skills) == 0 {
		return fillWrapped([]string{"", "No skills found in this scope.", "", mutedStyle.Render("skmr scans standard Codex, OpenCode, and Pi skill folders.")}, width)
	}
	if m.query != "" {
		return fillWrapped([]string{"", "No skills match \"" + terminal.Safe(m.query) + "\".", "", mutedStyle.Render("Press Esc to clear search.")}, width)
	}
	return fillWrapped([]string{"", "No skills in " + m.currentView().labels.full + ".", "", mutedStyle.Render("Press 1 to view all skills.")}, width)
}

func (m Model) detailPane(width, height int) string {
	return scrollFrame("Details", m.detailContent(width-2), width, height, m.offset, false)
}

func (m Model) detailContent(width int) []string {
	if m.inventory == inventoryLoading {
		return fillWrapped([]string{"", "Reading skill details…", "", mutedStyle.Render("Details will appear when the inventory is ready.")}, width)
	}
	if row, ok := m.selectedRow(); ok && row.kind == groupRow {
		return m.groupDetails(row, width)
	}
	s, ok := m.selected()
	if !ok {
		return fillWrapped([]string{"", "Select a skill to inspect it.", "", mutedStyle.Render(m.currentView().description)}, width)
	}
	state, stateStyle := compactState(s)
	lines := []string{align(accentStyle.Render(terminal.Safe(s.Name)), stateStyle.Render(state), width)}
	lines = append(lines, wrap(mutedStyle.Render(skillStateDescription(s)), width)...)
	if s.Description != "" {
		lines = append(lines, "")
		lines = append(lines, wrap(terminal.Safe(s.Description), width)...)
	}
	lines = append(lines, "")
	if s.Group != nil {
		lines = append(lines, field("Group", terminal.Safe(s.Group.Name), width)...)
	}
	lines = append(lines, field("Source", terminal.Safe(s.Path), width)...)
	lines = append(lines, field("Scope", terminal.Safe(s.Scope), width)...)
	owner := "outside library"
	if s.Managed || s.Inherited {
		owner = "global"
		if s.OwnerProject != "" {
			owner = terminal.Safe(s.OwnerProject)
		}
	}
	lines = append(lines, field("Owner", owner, width)...)
	agents := terminal.Safe(strings.Join(s.Agents, ", "))
	if agents == "" {
		agents = "none detected"
	}
	lines = append(lines, field("Visible to", agents, width)...)
	lines = append(lines, field("ID", terminal.Safe(s.ID), width)...)
	if s.ConflictKind != "" {
		lines = append(lines, field("Copies", fmt.Sprintf("%s · %d", skills.ConflictLabel(s.ConflictKind), s.ConflictCount), width)...)
		if s.Canonical {
			lines = append(lines, field("Kept copy", "yes", width)...)
		}
		for _, path := range s.Unresolved {
			lines = append(lines, field("View only", terminal.Safe(path), width)...)
		}
		if skills.HasCopyDifferences(s.ConflictKind) {
			lines = append(lines, mutedStyle.Render("Open Actions (x) to compare copies."))
		}
	}
	if len(s.Issues) > 0 {
		lines = append(lines, "", warningStyle.Render(fmt.Sprintf("! Problems (%d)", len(s.Issues))))
		for _, issue := range s.Issues {
			lines = append(lines, wrap("  "+terminal.Safe(issue), width)...)
		}
	}
	lines = append(lines, "", mutedStyle.Render("Open Actions (x) to read SKILL.md."))
	return lines
}

func (m Model) helpPane(width, height int) string {
	return scrollFrame("Help", m.helpContent(width-2), width, height, m.offset, false)
}

func (m Model) helpContent(width int) []string {
	return fillWrapped([]string{
		"skmr organizes skill folders in a library and controls which library skills agents can discover.",
		"",
		accentStyle.Render("Navigate"),
		"  ↑/k  select     Enter  open or toggle group",
		"  ←/→  group      PgUp/PgDn  scroll",
		"  Esc  back       q  quit",
		"",
		accentStyle.Render("Browse"),
		"  /  search       f or 1–4  change view",
		"  Tab  scope      R  refresh",
		"  !  problems",
		"",
		accentStyle.Render("Act"),
		"  x  open skill actions",
		"  In Actions: ↑/↓ to choose, Enter to select.",
		"  Displayed action shortcuts work only in Actions.",
		"  Read instructions, compare copies, open owner, or manage a skill.",
		"",
		accentStyle.Render("Mouse"),
		"  Click skills, views, scope, problems, or footer actions.",
		"  Scroll the active list or reading pane.",
		"",
		mutedStyle.Render("Recovery: skmr doctor --recover"),
	}, width)
}

func (m Model) instructionsPane(width, height int) string {
	item, ok := m.selected()
	if !ok {
		return frame("SKILL.md", fill([]string{"No skill selected."}, width-2, height-2), width, height, true)
	}
	content := strings.ReplaceAll(terminal.Safe(m.content), "\t", "    ")
	if content == "" {
		content = "Loading instructions…"
	}
	return scrollFrame("SKILL.md · "+terminal.Safe(item.Name), wrap(content, width-2), width, height, m.offset, true)
}

func (m Model) actionsPane(width, height int) string {
	item, ok := m.selected()
	if !ok {
		return frame("Actions", fill([]string{"No skill selected."}, width-2, height-2), width, height, true)
	}
	actions := actionsForSkill(item)
	lines := []string{accentStyle.Render(terminal.Safe(item.Name)), mutedStyle.Render("Choose an action or use its key."), ""}
	for i, action := range actions {
		prefix := "  "
		if i == m.actionCursor {
			prefix = "› "
		}
		line := align(prefix+action.label, keyStyle.Render(" "+action.key+" "), width-2)
		if i == m.actionCursor {
			line = selectedStyle.Render(pad(line, width-2))
		}
		lines = append(lines, line)
	}
	// Keep the selection visible even when the terminal is resized.
	pageSize := max(1, height-2)
	selectedLine := 3 + m.actionCursor
	offset := min(max(0, m.offset), max(0, len(lines)-pageSize))
	if selectedLine < offset {
		offset = selectedLine
	} else if selectedLine >= offset+pageSize {
		offset = selectedLine - pageSize + 1
	}
	return scrollFrame("Actions", lines, width, height, offset, true)
}

func (m Model) problemsPane(width, height int) string {
	lines := []string{"Problems found while reading this scope.", mutedStyle.Render("Resolve filesystem conflicts first. Use skmr doctor --recover for interrupted operations."), ""}
	for _, item := range m.problems() {
		lines = append(lines, warningStyle.Render("! "+item.title))
		if item.detail != "" {
			lines = append(lines, wrap(mutedStyle.Render("  "+item.detail), width-2)...)
		}
		lines = append(lines, "")
	}
	return scrollFrame("Problems", fillWrapped(lines, width-2), width, height, m.offset, true)
}

func (m Model) diffPane(width, height int) string {
	source, _, ok := m.selectedDiffTarget()
	if !ok {
		return frame("Differences", fill([]string{"No different copies selected."}, width-2, height-2), width, height, true)
	}
	peers := m.diffPeers(source)
	lines := []string{}
	if len(peers) > 1 {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("Copy %d/%d. Press [ or ] to compare another copy.", m.diffTarget+1, len(peers))))
	}
	lines = append(lines, sideBySideDiffLines(m.diff, width-2, m.diffHorizontal)...)
	if m.diffLoading {
		lines = append(lines, "", "Comparing package contents…")
	}
	return scrollFrame("Differences", lines, width, height, m.offset, true)
}

func sideBySideDiffLines(diff copyDiff, width, horizontal int) []string {
	separator := lipgloss.NewStyle().Foreground(borderColor).Render(" │ ")
	separatorWidth := ansi.StringWidth(separator)
	leftWidth := max(1, (width-separatorWidth)/2)
	rightWidth := max(1, width-separatorWidth-leftWidth)
	join := func(left, right string) string {
		return pad(left, leftWidth) + separator + pad(right, rightWidth)
	}
	lines := []string{
		join(accentStyle.Render("SELECTED"), accentStyle.Render("COMPARISON")),
		join(mutedStyle.Render(tailPath(diff.sourcePath, leftWidth)), mutedStyle.Render(tailPath(diff.targetPath, rightWidth))),
	}
	for _, row := range sideBySideRows(diff.lines) {
		switch row.kind {
		case diffHeader:
			lines = append(lines, accentStyle.Render(ansi.Truncate("── "+terminal.Safe(row.label), width, "…")))
		case diffHunk:
			lines = append(lines, accentStyle.Render(ansi.Truncate(terminal.Safe(row.label), width, "…")))
		case diffNotice:
			lines = append(lines, warningStyle.Render(ansi.Truncate(terminal.Safe(row.label), width, "…")))
		default:
			lines = append(lines, join(renderSideDiffCell(row.left, leftWidth, horizontal), renderSideDiffCell(row.right, rightWidth, horizontal)))
		}
	}
	return lines
}

func tailPath(path string, width int) string {
	path = terminal.Safe(path)
	if ansi.StringWidth(path) <= width {
		return path
	}
	return ansi.TruncateLeft(path, ansi.StringWidth(path)-max(1, width-1), "…")
}

func scrollFrame(title string, lines []string, width, height, offset int, active bool) string {
	innerHeight := max(1, height-2)
	meta := scrollRange(len(lines), innerHeight, offset)
	if meta.overflow {
		title += fmt.Sprintf(" %d–%d/%d", meta.start, meta.end, meta.total)
		if meta.above {
			title += " ↑"
		}
		if meta.below {
			title += " ↓"
		}
	}
	start := max(0, meta.start-1)
	return frame(title, fill(lines[start:meta.end], width-2, innerHeight), width, height, active)
}

func scrollRange(total, pageSize, offset int) scrollMetadata {
	pageSize = max(1, pageSize)
	offset = min(max(0, offset), max(0, total-pageSize))
	end := min(total, offset+pageSize)
	start := 0
	if total > 0 {
		start = offset + 1
	}
	return scrollMetadata{start: start, end: end, total: total, overflow: total > pageSize, above: offset > 0, below: end < total}
}

func (m Model) reviewView(width, height int) string {
	title := "Confirm"
	preview := ""
	if m.pendingTransfer != nil {
		title += " " + m.pendingTransfer.plan.Action
		preview = m.pendingTransfer.plan.String()
	} else {
		title += " " + actionLabel(m.pending.Action)
		preview = m.pending.String()
	}
	lines := []string{
		warningStyle.Render("Review every filesystem change before applying."),
		"",
	}
	lines = append(lines, wrap(terminal.Safe(preview), max(1, width-4))...)
	return scrollFrame(title, lines, width, height, m.offset, true)
}

func (m Model) transferView(width, height int) string {
	value := m.transferInput
	if value == "" {
		value = "global or /path/to/project"
	}
	lines := []string{
		accentStyle.Render("Destination scope"),
		"",
		"> " + terminal.Safe(value) + accentStyle.Render("█"),
		"",
		mutedStyle.Render("Type global or an existing project directory."),
		mutedStyle.Render("Enter review  Esc cancel"),
	}
	name := strings.ToUpper(m.transferAction[:1]) + m.transferAction[1:]
	return frame(name+" skill", fill(lines, width-2, height-2), width, height, true)
}

func actionLabel(action string) string {
	if action == "adopt" {
		return "add to library"
	}
	if action == "restore" {
		return "remove from library"
	}
	return action
}

func (m Model) statusLine(width int) string {
	message := terminal.Safe(m.notice.text)
	prefix, style := "●", mutedStyle
	switch m.notice.level {
	case noticeProgress:
		prefix, style = "◌", accentStyle
	case noticeSuccess:
		prefix, style = "✓", successStyle
	case noticeWarning:
		prefix, style = "!", warningStyle
	case noticeError:
		prefix, style = "!", errorStyle
	}
	return ansi.Truncate(style.Render(prefix+" "+message), width, "…")
}

func (m Model) footer(width int) string {
	all := m.shortcuts()
	visible := packShortcuts(width, all)
	return hints(width, visible, len(visible) < len(all))
}

func (m Model) shortcuts() []shortcut {
	if m.busy {
		return []shortcut{{"q", "quit", "q", true}}
	}
	if m.pendingBatch != nil {
		return []shortcut{{"y", "add skills", "y", true}, {"n", "back", "n", true}, {"PgUp/Dn", "review", "", false}, {"q", "quit", "q", true}}
	}
	if m.migration != nil {
		escapeLabel := "cancel"
		if m.migration.initial {
			escapeLabel = "skip"
		}
		return []shortcut{{"Space", "select", " ", true}, {"a", "all safe", "a", false}, {"n", "clear", "n", false}, {"Enter", "review", "enter", true}, {"Esc", escapeLabel, "esc", true}, {"q", "quit", "q", true}}
	}
	if m.pending != nil || m.pendingTransfer != nil {
		return []shortcut{{"y", "apply", "y", true}, {"n", "cancel", "n", true}, {"PgUp/Dn", "review", "", false}, {"q", "quit", "q", true}}
	}
	if m.transferAction != "" {
		return []shortcut{{"type", "destination", "", false}, {"Enter", "review", "enter", true}, {"Esc", "cancel", "esc", true}, {"⌫", "delete", "backspace", false}}
	}
	if m.searching {
		return []shortcut{{"type", "search", "", false}, {"Enter", "keep", "enter", true}, {"Esc", "clear", "esc", true}, {"⌫", "delete", "backspace", false}}
	}
	switch m.pane {
	case paneInstructions:
		return []shortcut{{"PgUp/Dn", "scroll", "", false}, {"Esc", "back", "esc", true}, {"q", "quit", "q", true}}
	case paneProblems:
		return []shortcut{{"PgUp/Dn", "scroll", "", false}, {"Esc", "back", "esc", true}, {"q", "quit", "q", true}}
	case paneDiff:
		shortcuts := []shortcut{{"PgUp/Dn", "scroll", "", false}, {"←/→", "pan", "", false}}
		if source, ok := m.selected(); ok && len(m.diffPeers(source)) > 1 {
			shortcuts = append(shortcuts, shortcut{"[/]", "copy", "", false})
		}
		return append(shortcuts, shortcut{"Esc", "back", "esc", true}, shortcut{"q", "quit", "q", true})
	case paneHelp:
		return []shortcut{{"PgUp/Dn", "scroll", "", false}, {"Esc", "back", "esc", true}, {"q", "quit", "q", true}}
	case paneActions:
		return []shortcut{{"↑↓", "choose", "", false}, {"Enter", "select", "enter", true}, {"Esc", "back", "esc", true}, {"q", "quit", "q", true}}
	}
	out := []shortcut{{"↑↓", "select", "", false}, {"/", "search", "/", false}, {"A", "add skills", "A", false}}
	if m.pane == paneDetails && m.compact() {
		out = append(out, shortcut{"Esc", "back", "esc", true})
	}
	if row, ok := m.selectedRow(); ok && row.kind == groupRow {
		if m.query == "" {
			out = append(out, shortcut{"Enter", "expand/collapse", "enter", true})
		} else {
			out = append(out, shortcut{"→", "enter group", "right", true})
		}
	} else if _, ok := m.selected(); ok {
		if m.compact() && m.pane == paneList {
			out = append(out, shortcut{"Enter", "details", "enter", false})
		}
		out = append(out, shortcut{"x", "actions", "x", true})
	}
	if len(m.problems()) > 0 {
		out = append(out, shortcut{"!", "problems", "!", false})
	}
	return append(out, shortcut{"?", "keys", "?", true}, shortcut{"q", "quit", "q", true})
}

func (m Model) footerKeyAt(x int) string {
	position := 0
	for _, item := range packShortcuts(m.width, m.shortcuts()) {
		width := ansi.StringWidth(hintText(item, m.width < 40))
		if x >= position && x < position+width {
			return item.event
		}
		position += width + 2
	}
	return ""
}

func compactState(skill skills.Skill) (string, lipgloss.Style) {
	if skill.ConflictKind != "" {
		return "! " + skills.ConflictLabel(skill.ConflictKind), warningStyle
	}
	if len(skill.Issues) > 0 {
		return "! warning", warningStyle
	}
	if skill.Inherited {
		if skill.Managed && skill.Enabled {
			return "parent · enabled", successStyle
		}
		if skill.Managed {
			return "parent · disabled", mutedStyle
		}
		return "parent", mutedStyle
	}
	if skill.ReadOnly {
		return "◇ view only", mutedStyle
	}
	if skill.Managed && skill.Enabled {
		return "● enabled", successStyle
	}
	if skill.Managed {
		return "○ disabled", mutedStyle
	}
	return "· outside library", labelStyle
}

func skillStateDescription(skill skills.Skill) string {
	if skill.Inherited {
		owner := "the global scope"
		if skill.OwnerProject != "" {
			owner = terminal.Safe(skill.OwnerProject)
		}
		return "Owned by " + owner + ". Open that scope to make changes."
	}
	if skill.ReadOnly {
		return "skmr can inspect this copy but cannot move or change it."
	}
	if skill.Managed && skill.Enabled {
		return "Stored in this library and exposed through skmr's shared discovery folder."
	}
	if skill.Managed {
		return "Stored in this library, but not exposed through skmr's shared discovery folder."
	}
	return "Found in this scope. Add it to the library to let skmr control it."
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
	labelWidth := max(8, ansi.StringWidth(label)+1)
	prefix := labelStyle.Render(pad(label, labelWidth))
	available := max(1, width-labelWidth)
	parts := wrap(value, available)
	lines := make([]string, 0, len(parts))
	for i, part := range parts {
		if i == 0 {
			lines = append(lines, prefix+part)
		} else {
			lines = append(lines, strings.Repeat(" ", labelWidth)+part)
		}
	}
	return lines
}

func hints(width int, values []shortcut, omitted bool) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		key, label := hintParts(value, width < 40)
		parts = append(parts, keyStyle.Render(" "+key+" ")+" "+mutedStyle.Render(label))
	}
	result := strings.Join(parts, "  ")
	if omitted {
		result += mutedStyle.Render("  …")
	}
	return ansi.Truncate(result, width, "…")
}

func packShortcuts(width int, values []shortcut) []shortcut {
	included := make([]bool, len(values))
	for i, item := range values {
		included[i] = item.essential
	}
	for i := range values {
		if included[i] {
			continue
		}
		included[i] = true
		candidate := chosenShortcuts(values, included)
		if shortcutsWidth(candidate, width < 40)+3 > width {
			included[i] = false
		}
	}
	return chosenShortcuts(values, included)
}

func chosenShortcuts(values []shortcut, included []bool) []shortcut {
	out := []shortcut{}
	for i, item := range values {
		if included[i] {
			out = append(out, item)
		}
	}
	return out
}

func shortcutsWidth(values []shortcut, compact bool) int {
	width := max(0, (len(values)-1)*2)
	for _, item := range values {
		width += ansi.StringWidth(hintText(item, compact))
	}
	return width
}

func hintText(item shortcut, compact bool) string {
	key, label := hintParts(item, compact)
	return " " + key + "  " + label
}

func hintParts(item shortcut, compact bool) (string, string) {
	if !compact {
		return item.key, item.label
	}
	key, label := item.key, item.label
	if key == "Space" {
		key = "Spc"
	}
	switch label {
	case "apply":
		label = "yes"
	case "cancel":
		label = "no"
	case "select":
		label = "go"
	case "add to library":
		label = "add"
	case "keep this copy":
		label = "keep"
	case "open owner":
		label = "owner"
	case "enable":
		label = "on"
	case "disable":
		label = "off"
	case "instructions":
		label = "read"
	case "problems":
		label = "issues"
	}
	return key, label
}

func fillWrapped(lines []string, width int) []string {
	out := []string{}
	for _, line := range lines {
		out = append(out, wrap(line, width)...)
	}
	return out
}

func wrap(value string, width int) []string {
	width = max(1, width)
	return strings.Split(ansi.Hardwrap(ansi.Wordwrap(value, width, ""), width, true), "\n")
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
