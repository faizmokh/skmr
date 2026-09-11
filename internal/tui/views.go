package tui

import "github.com/faizmokh/skmr/internal/skills"

type skillView uint8

const (
	viewAll skillView = iota
	viewLibrary
	viewOtherFolders
	viewParentScopes
)

type responsiveViewLabels struct {
	full    string
	compact string
}

type viewDefinition struct {
	kind        skillView
	shortcut    string
	labels      responsiveViewLabels
	description string
	projectOnly bool
	includes    func(skills.Skill) bool
}

var skillViews = []viewDefinition{
	{
		kind:        viewAll,
		shortcut:    "1",
		labels:      responsiveViewLabels{full: "All skills", compact: "All"},
		description: "Add local skills to the library to control agent discovery.",
		includes:    func(skills.Skill) bool { return true },
	},
	{
		kind:        viewLibrary,
		shortcut:    "2",
		labels:      responsiveViewLabels{full: "Library", compact: "Lib"},
		description: "Skills stored and controlled by this scope's library.",
		includes:    func(s skills.Skill) bool { return s.Managed && !s.Inherited },
	},
	{
		kind:        viewOtherFolders,
		shortcut:    "3",
		labels:      responsiveViewLabels{full: "Other folders", compact: "Other"},
		description: "Skills found here but not stored by skmr.",
		includes:    func(s skills.Skill) bool { return !s.Managed && !s.Inherited },
	},
	{
		kind:        viewParentScopes,
		shortcut:    "4",
		labels:      responsiveViewLabels{full: "Parent scopes", compact: "Parents"},
		description: "Skills visible here but owned by a parent scope.",
		projectOnly: true,
		includes:    func(s skills.Skill) bool { return s.Inherited },
	},
}

func (m Model) availableViews() []viewDefinition {
	out := make([]viewDefinition, 0, len(skillViews))
	for _, view := range skillViews {
		if view.projectOnly && m.service.Scope() != "project" {
			continue
		}
		out = append(out, view)
	}
	return out
}

func (m Model) currentView() viewDefinition {
	for _, view := range m.availableViews() {
		if view.kind == m.filter {
			return view
		}
	}
	return skillViews[0]
}

func (m *Model) selectView(view skillView) {
	m.filter = viewAll
	for _, available := range m.availableViews() {
		if available.kind == view {
			m.filter = view
			break
		}
	}
	m.cursor = 0
}

func (m *Model) cycleView() {
	views := m.availableViews()
	for i, view := range views {
		if view.kind == m.filter {
			m.selectView(views[(i+1)%len(views)].kind)
			return
		}
	}
	m.selectView(viewAll)
}

func viewForShortcut(shortcut string) (skillView, bool) {
	for _, view := range skillViews {
		if view.shortcut == shortcut {
			return view.kind, true
		}
	}
	return viewAll, false
}
