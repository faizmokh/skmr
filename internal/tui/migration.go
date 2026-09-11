package tui

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/manager"
	"github.com/faizmokh/skmr/internal/skills"
	"github.com/faizmokh/skmr/internal/terminal"
)

type migrationState struct {
	candidates []manager.AdoptionCandidate
	selected   map[string]bool
	expanded   map[string]bool
	cursor     int
	initial    bool
}

type migrationRowKind uint8

const (
	migrationSkillRow migrationRowKind = iota
	migrationConflictRow
	migrationCopyRow
)

type migrationRow struct {
	kind      migrationRowKind
	name      string
	candidate int
	conflict  string
}

func newMigration(result skills.Result, initial bool) *migrationState {
	candidates := manager.AdoptionCandidates(result)
	if len(candidates) == 0 {
		return nil
	}
	state := &migrationState{
		candidates: candidates,
		selected:   map[string]bool{},
		expanded:   map[string]bool{},
		initial:    initial,
	}
	for _, candidate := range candidates {
		if candidate.Eligible {
			state.selected[candidate.Skill.Path] = true
		}
	}
	return state
}

func (s *migrationState) rows() []migrationRow {
	byConflict := map[string][]int{}
	standalone := []int{}
	for i, candidate := range s.candidates {
		if candidate.Skill.ConflictID == "" {
			standalone = append(standalone, i)
		} else {
			byConflict[candidate.Skill.ConflictID] = append(byConflict[candidate.Skill.ConflictID], i)
		}
	}
	rows := make([]migrationRow, 0, len(s.candidates)+len(byConflict))
	for _, i := range standalone {
		rows = append(rows, migrationRow{kind: migrationSkillRow, name: s.candidates[i].Skill.Name, candidate: i})
	}
	for conflict, indexes := range byConflict {
		name := s.candidates[indexes[0]].Skill.Name
		rows = append(rows, migrationRow{kind: migrationConflictRow, name: name, candidate: indexes[0], conflict: conflict})
		if s.expanded[conflict] {
			for _, i := range indexes {
				rows = append(rows, migrationRow{kind: migrationCopyRow, name: name, candidate: i, conflict: conflict})
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].name == rows[j].name {
			return rows[i].kind < rows[j].kind
		}
		return rows[i].name < rows[j].name
	})
	return rows
}

func (s *migrationState) selectedPaths() []string {
	paths := []string{}
	for _, candidate := range s.candidates {
		if s.selected[candidate.Skill.Path] {
			paths = append(paths, candidate.Skill.Path)
		}
	}
	return paths
}

func (s *migrationState) selectedCount() int { return len(s.selectedPaths()) }

func (s *migrationState) skippedCount() int {
	names := map[string]bool{}
	for _, candidate := range s.candidates {
		names[candidate.Skill.Name] = true
	}
	return max(0, len(names)-s.selectedCount())
}

func (s *migrationState) safeCount() int {
	count := 0
	for _, candidate := range s.candidates {
		if candidate.Eligible {
			count++
		}
	}
	return count
}

func (s *migrationState) conflictCount() int {
	seen := map[string]bool{}
	for _, candidate := range s.candidates {
		if candidate.Skill.ConflictID != "" {
			seen[candidate.Skill.ConflictID] = true
		}
	}
	return len(seen)
}

func (s *migrationState) selectedConflict(conflict string) string {
	for _, candidate := range s.candidates {
		if candidate.Skill.ConflictID == conflict && s.selected[candidate.Skill.Path] {
			return candidate.Skill.Path
		}
	}
	return ""
}

func (s *migrationState) toggleCurrent() (string, bool) {
	rows := s.rows()
	if s.cursor < 0 || s.cursor >= len(rows) {
		return "", false
	}
	row := rows[s.cursor]
	if row.kind == migrationConflictRow {
		if path := s.selectedConflict(row.conflict); path != "" {
			delete(s.selected, path)
			return "", true
		}
		s.expanded[row.conflict] = true
		return "Choose one copy of " + row.name + " to keep.", false
	}
	candidate := s.candidates[row.candidate]
	if !candidate.Selectable {
		return candidate.Reason, false
	}
	path := candidate.Skill.Path
	if s.selected[path] {
		delete(s.selected, path)
		return "", true
	}
	if row.conflict != "" {
		for _, peer := range s.candidates {
			if peer.Skill.ConflictID == row.conflict {
				delete(s.selected, peer.Skill.Path)
			}
		}
	}
	s.selected[path] = true
	return "", true
}

func (s *migrationState) selectAllSafe() {
	for _, candidate := range s.candidates {
		if candidate.Eligible {
			s.selected[candidate.Skill.Path] = true
		}
	}
}

func (s *migrationState) clearSelection() { s.selected = map[string]bool{} }

func (s *migrationState) navigateConflict(direction string) {
	rows := s.rows()
	if s.cursor < 0 || s.cursor >= len(rows) {
		return
	}
	row := rows[s.cursor]
	if direction == "right" && row.kind == migrationConflictRow {
		if s.expanded[row.conflict] {
			s.cursor = min(s.cursor+1, len(rows)-1)
		} else {
			s.expanded[row.conflict] = true
		}
	}
	if direction == "left" {
		if row.kind == migrationConflictRow {
			s.expanded[row.conflict] = false
		} else if row.kind == migrationCopyRow {
			for i, candidateRow := range rows {
				if candidateRow.kind == migrationConflictRow && candidateRow.conflict == row.conflict {
					s.cursor = i
					break
				}
			}
		}
	}
}

func (m Model) migrationView(width, height int) string {
	state := m.migration
	title := "Add skills"
	if state.initial {
		title = "Set up your skill library"
	}
	innerWidth := max(1, width-2)
	innerHeight := max(1, height-2)
	selected := state.selectedCount()
	header := []string{
		fmt.Sprintf("Selected: %d", selected),
		mutedStyle.Render("Selected folders move into the library and stay available from .agents/skills."),
	}
	if conflicts := state.conflictCount(); conflicts > 0 {
		message := fmt.Sprintf("%d duplicate-name groups need a kept-copy choice or can be skipped.", conflicts)
		if conflicts == 1 {
			message = "1 duplicate-name group needs a kept-copy choice or can be skipped."
		}
		header = append(header, warningStyle.Render(message))
	}
	header = append(header, "")
	rowHeight := max(1, innerHeight-len(header))
	rows := state.rows()
	state.cursor = min(max(0, state.cursor), max(0, len(rows)-1))
	start := min(max(0, state.cursor-rowHeight/2), max(0, len(rows)-rowHeight))
	lines := append([]string{}, header...)
	for i := start; i < len(rows) && len(lines) < innerHeight; i++ {
		row := rows[i]
		candidate := state.candidates[row.candidate]
		prefix := "  "
		if i == state.cursor {
			prefix = "› "
		}
		mark := "[ ]"
		status := ""
		style := labelStyle
		name := candidate.Skill.Name
		switch row.kind {
		case migrationConflictRow:
			disclosure := "▸"
			if state.expanded[row.conflict] {
				disclosure = "▾"
			}
			if state.selectedConflict(row.conflict) != "" {
				mark = "[x]"
				status = "copy chosen"
				style = successStyle
			} else {
				status = "choose copy"
				style = warningStyle
			}
			name = disclosure + " " + name
		case migrationCopyRow:
			mark = "( )"
			if state.selected[candidate.Skill.Path] {
				mark = "(x)"
				style = successStyle
			}
			name = "  " + name
			status = tailPath(candidate.Skill.Path, max(8, innerWidth/2))
		default:
			if state.selected[candidate.Skill.Path] {
				mark = "[x]"
				status = "selected"
				style = successStyle
			} else if !candidate.Selectable {
				status = candidate.Reason
				style = warningStyle
			}
		}
		status = ansi.Truncate(terminal.Safe(status), max(8, innerWidth/2), "…")
		left := prefix + mark + " " + terminal.Safe(name)
		nameWidth := max(4, innerWidth-ansi.StringWidth(status)-1)
		line := align(ansi.Truncate(left, nameWidth, "…"), style.Render(terminal.Safe(status)), innerWidth)
		if i == state.cursor {
			line = selectedStyle.Render(pad(line, innerWidth))
		}
		lines = append(lines, line)
	}
	return frame(title, fill(lines, innerWidth, innerHeight), width, height, true)
}

func (m Model) migrationRowAt(y int) (int, bool) {
	if m.migration == nil {
		return 0, false
	}
	bodyHeight := max(6, m.height-3)
	innerHeight := max(1, bodyHeight-2)
	headerSize := 3
	if m.migration.conflictCount() > 0 {
		headerSize++
	}
	rowHeight := max(1, innerHeight-headerSize)
	rows := m.migration.rows()
	start := min(max(0, m.migration.cursor-rowHeight/2), max(0, len(rows)-rowHeight))
	row := y - 2 - headerSize
	if row < 0 || row >= rowHeight {
		return 0, false
	}
	index := start + row
	return index, index >= 0 && index < len(rows)
}

func (m Model) batchReviewView(width, height int) string {
	lines := []string{
		warningStyle.Render("Review every filesystem change before applying."),
		"",
	}
	lines = append(lines, wrap(terminal.Safe(m.pendingBatch.String()), max(1, width-4))...)
	return scrollFrame("Confirm add skills", lines, width, height, m.offset, true)
}
