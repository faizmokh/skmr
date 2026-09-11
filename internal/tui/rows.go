package tui

import (
	"fmt"
	"github.com/faizmokh/skmr/internal/terminal"
	"sort"

	"github.com/faizmokh/skmr/internal/skills"
)

type rowKind uint8

const (
	skillRow rowKind = iota
	groupRow
)

type listRow struct {
	kind    rowKind
	skill   skills.Skill
	group   *skills.Group
	members []skills.Skill
	total   int
}

func (r listRow) key() string {
	if r.kind == groupRow {
		return "group:" + r.group.ID
	}
	return "skill:" + r.skill.ID
}
func (r listRow) name() string {
	if r.kind == groupRow {
		return r.group.Name
	}
	return r.skill.Name
}
func (m Model) expandedKey(id string) string { return m.service.Store + ":" + id }
func (m Model) isExpanded(id string) bool    { return m.query != "" || m.expanded[m.expandedKey(id)] }
func (m *Model) setExpanded(id string, value bool) {
	if m.expanded == nil {
		m.expanded = map[string]bool{}
	}
	m.expanded[m.expandedKey(id)] = value
}
func (m Model) rows() []listRow {
	totals := map[string]int{}
	for _, s := range m.result.Skills {
		if s.Group != nil {
			totals[s.Group.ID]++
		}
	}
	top := []listRow{}
	groups := map[string]int{}
	for _, s := range m.items() {
		if s.Group == nil {
			top = append(top, listRow{kind: skillRow, skill: s})
			continue
		}
		i, ok := groups[s.Group.ID]
		if !ok {
			i = len(top)
			groups[s.Group.ID] = i
			top = append(top, listRow{kind: groupRow, group: s.Group, total: totals[s.Group.ID]})
		}
		top[i].members = append(top[i].members, s)
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].name() == top[j].name() {
			return top[i].key() < top[j].key()
		}
		return top[i].name() < top[j].name()
	})
	out := []listRow{}
	for _, r := range top {
		sort.Slice(r.members, func(i, j int) bool {
			if r.members[i].Name == r.members[j].Name {
				return r.members[i].ID < r.members[j].ID
			}
			return r.members[i].Name < r.members[j].Name
		})
		out = append(out, r)
		if r.kind == groupRow && m.isExpanded(r.group.ID) {
			for _, s := range r.members {
				out = append(out, listRow{kind: skillRow, skill: s, group: r.group})
			}
		}
	}
	return out
}
func (m Model) selectedRow() (listRow, bool) {
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return listRow{}, false
	}
	return rows[m.cursor], true
}
func (m Model) rowStart(height int) int {
	return min(max(0, m.cursor-height/2), max(0, len(m.rows())-height))
}
func (m *Model) restoreSelection(key string) {
	rows := m.rows()
	m.cursor = min(m.cursor, max(0, len(rows)-1))
	for i, r := range rows {
		if r.key() == key {
			m.cursor = i
			return
		}
	}
}
func (m *Model) navigateGroup(key string) {
	r, ok := m.selectedRow()
	if !ok {
		return
	}
	if r.kind == groupRow {
		switch key {
		case "enter":
			if m.query == "" {
				m.setExpanded(r.group.ID, !m.isExpanded(r.group.ID))
			}
		case "right":
			if m.isExpanded(r.group.ID) {
				m.cursor++
			} else {
				m.setExpanded(r.group.ID, true)
			}
		case "left":
			if m.query == "" {
				m.setExpanded(r.group.ID, false)
			}
		}
	} else if key == "left" && r.group != nil {
		m.restoreSelection("group:" + r.group.ID)
	}
}

func (m Model) groupDetails(row listRow, width int) []string {
	group := row.group
	inLibrary, otherFolders, parentScopes, readonly, problems := 0, 0, 0, 0, 0
	for _, s := range m.result.Skills {
		if s.Group == nil || s.Group.ID != group.ID {
			continue
		}
		switch {
		case s.Inherited:
			parentScopes++
		case s.Managed:
			inLibrary++
		default:
			otherFolders++
		}
		if s.ReadOnly || s.Inherited {
			readonly++
		}
		if len(s.Issues) > 0 || s.ConflictID != "" {
			problems++
		}
	}
	lines := []string{accentStyle.Render(terminal.Safe(group.Name)), ""}
	fields := [][2]string{{"Source", group.Source}, {"Scope", group.Scope}, {"Market", group.Marketplace}, {"Version", group.Version}, {"Skills", fmt.Sprintf("%d total, %d matching", row.total, len(row.members))}}
	if inLibrary > 0 {
		fields = append(fields, [2]string{"In library", skillCount(inLibrary)})
	}
	if otherFolders > 0 {
		fields = append(fields, [2]string{"Other folders", skillCount(otherFolders)})
	}
	if m.service.Scope() == "project" && parentScopes > 0 {
		fields = append(fields, [2]string{"Parent scopes", skillCount(parentScopes)})
	}
	if readonly > 0 {
		fields = append(fields, [2]string{"View only", skillCount(readonly)})
	}
	if problems > 0 {
		fields = append(fields, [2]string{"Issues", skillCount(problems)})
	}
	for _, f := range fields {
		if f[1] != "" {
			lines = append(lines, field(f[0], terminal.Safe(f[1]), width)...)
		}
	}
	lines = append(lines, "", "Right expands or enters this group.", "Select a skill to inspect it or change how skmr handles it.")
	return fillWrapped(lines, width)
}

func skillCount(n int) string {
	if n == 1 {
		return "1 skill"
	}
	return fmt.Sprintf("%d skills", n)
}
