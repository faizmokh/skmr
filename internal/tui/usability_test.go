package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/manager"
	"github.com/faizmokh/skmr/internal/skills"
)

func TestResponsiveRenderSizesAndViewLabels(t *testing.T) {
	for _, project := range []bool{false, true} {
		m := model(t)
		if project {
			path := filepath.Join(m.service.Config.Home, "project")
			if err := os.MkdirAll(path, 0755); err != nil {
				t.Fatal(err)
			}
			service, err := manager.New(manager.Config{Home: m.service.Config.Home, Project: path})
			if err != nil {
				t.Fatal(err)
			}
			m.service = service
		}
		for _, size := range [][2]int{{30, 10}, {40, 12}, {60, 18}, {80, 24}, {100, 30}} {
			m.width, m.height = size[0], size[1]
			view := m.View()
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("%v scope=%v overflow: %q", size, project, line)
				}
			}
			if lines := len(strings.Split(view, "\n")); lines > size[1] {
				t.Fatalf("%v scope=%v rendered %d lines", size, project, lines)
			}
		}

		m.width, m.height = 80, 24
		view := m.View()
		for _, label := range []string{"All skills", "Library", "Other folders"} {
			if !strings.Contains(view, label) {
				t.Fatalf("80-column view omitted %q", label)
			}
		}
		if project && !strings.Contains(view, "Parent scopes") {
			t.Fatal("80-column project view omitted Parent scopes")
		}
		if strings.Contains(view, "press / to search") {
			t.Fatal("80-column view should hide passive search guidance")
		}

		m.width = 59
		view = m.View()
		if !strings.Contains(view, "All") || strings.Contains(view, "Other folders") {
			t.Fatal("sub-60 view should use one compact view label")
		}
	}
}

func TestInventoryLoadingNeverShowsStaleOrFalseEmptyState(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := manager.New(manager.Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	m := New(service)
	view := m.View()
	if !strings.Contains(view, "Skills · reading") || !strings.Contains(view, "Reading skill folders") || !strings.Contains(view, "Reading skill details") || strings.Contains(view, "0/0") || strings.Contains(view, "No skills found") {
		t.Fatalf("initial loading state is misleading: %s", view)
	}
	footer := ansi.Strip(m.footer(m.width))
	if !strings.Contains(footer, "q") || strings.Contains(footer, "search") || strings.Contains(ansi.Strip(m.header(m.width)), "Tab") {
		t.Fatalf("loading state advertises unavailable controls: header=%q footer=%q", ansi.Strip(m.header(m.width)), footer)
	}
	next, cmd := m.Update(tea.MouseMsg{X: 1, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if next.(Model).busy != m.busy || cmd == nil {
		t.Fatal("busy-state quit is not clickable")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("busy-state footer returned the wrong command")
	}
	m, _ = key(m, "2")
	if m.filter != viewLibrary {
		t.Fatal("visible view shortcut did not work while loading")
	}

	m = model(t)
	project := filepath.Join(m.service.Config.Home, "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	m.project = project
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if cmd == nil || m.inventory != inventoryLoading || len(m.result.Skills) != 0 {
		t.Fatal("scope switch retained stale inventory")
	}
	if view = m.View(); strings.Contains(view, "alpha") || !strings.Contains(view, "Reading skill folders") {
		t.Fatalf("scope switch rendered stale inventory: %s", view)
	}
}

func TestComputedHeaderHitboxes(t *testing.T) {
	m := model(t)
	project := filepath.Join(m.service.Config.Home, "a-very-long-project-name")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	m.project = project
	m.width, m.height = 80, 24
	header := m.headerLayout(m.width)
	if header.scopeStart < 0 || header.scopeEnd <= header.scopeStart {
		t.Fatal("scope hitbox is not visible")
	}
	next, cmd := m.Update(tea.MouseMsg{X: (header.scopeStart + header.scopeEnd) / 2, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.service.Scope() != "project" || cmd == nil {
		t.Fatal("rendered scope hitbox did not switch scope")
	}

	m = model(t)
	m.width, m.height = 80, 24
	m.result.Issues = []string{"broken inventory"}
	header = m.headerLayout(m.width)
	if header.problemsStart < 0 || header.problemsEnd <= header.problemsStart {
		t.Fatal("problems hitbox is not visible")
	}
	next, _ = m.Update(tea.MouseMsg{X: (header.problemsStart + header.problemsEnd) / 2, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if next.(Model).pane != paneProblems {
		t.Fatal("rendered problems hitbox did not open Problems")
	}
}

func TestCompactPaneNavigationAndSearchClearing(t *testing.T) {
	m := model(t)
	m.width, m.height = 60, 18
	m, _ = key(m, "enter")
	if m.pane != paneDetails {
		t.Fatal("Enter did not open compact details")
	}
	m, _ = actionKey(m, "i")
	if m.pane != paneInstructions {
		t.Fatal("i did not open instructions")
	}
	m, _ = key(m, "esc")
	if m.pane != paneDetails {
		t.Fatal("Esc did not return to details")
	}
	m, _ = key(m, "esc")
	if m.pane != paneList {
		t.Fatal("Esc did not return to list")
	}
	m, _ = key(m, "/")
	m, _ = key(m, "alpha")
	m, _ = key(m, "esc")
	if m.searching || m.query != "" || len(m.items()) != 2 {
		t.Fatal("one Esc did not exit and clear search")
	}
}

func TestReviewQuitAndScrollRanges(t *testing.T) {
	m := model(t)
	m.pending = &manager.Plan{Action: "adopt"}
	m.pane = paneReview
	_, cmd := key(m, "q")
	if cmd == nil {
		t.Fatal("q did not quit review")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("review q returned the wrong command")
	}

	lines := make([]string, 74)
	for i := range lines {
		lines[i] = "line"
	}
	view := scrollFrame("Instructions", lines, 40, 20, 0, true)
	if !strings.Contains(view, "1–18/74") {
		t.Fatalf("missing scroll range: %s", view)
	}
}

func TestEveryPaneRendersWithinCompactBounds(t *testing.T) {
	base := model(t)
	base.width, base.height = 60, 18
	base.content = strings.Repeat("instruction line\n", 40)
	base.result.Skills[0].Issues = []string{"Fix the SKILL.md frontmatter"}

	tests := []struct {
		name  string
		setup func(*Model)
		want  string
	}{
		{"list", func(m *Model) { m.pane = paneList }, "Skills"},
		{"details", func(m *Model) { m.pane = paneDetails }, "Details"},
		{"instructions", func(m *Model) { m.pane = paneInstructions }, "SKILL.md"},
		{"actions", func(m *Model) { m.pane = paneActions }, "Actions"},
		{"problems", func(m *Model) { m.pane = paneProblems }, "Problems"},
		{"help", func(m *Model) { m.pane = paneHelp }, "Help"},
		{"review", func(m *Model) {
			m.pane = paneReview
			m.pending = &manager.Plan{Action: "adopt"}
		}, "Confirm add to library"},
		{"transfer", func(m *Model) { m.transferAction = "move" }, "Move skill"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := base
			test.setup(&m)
			view := m.View()
			if !strings.Contains(view, test.want) {
				t.Fatalf("missing %q: %s", test.want, view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > m.width {
					t.Fatalf("pane overflow: %q", line)
				}
			}
			if len(strings.Split(view, "\n")) > m.height {
				t.Fatal("pane height overflow")
			}
		})
	}
}

func TestActionDefinitionsDrivePickerAndFooter(t *testing.T) {
	tests := []struct {
		name  string
		skill skills.Skill
		want  []string
	}{
		{"outside", skills.Skill{}, []string{"i", "a"}},
		{"managed", skills.Skill{Managed: true}, []string{"i", "e", "m", "p", "r"}},
		{"enabled", skills.Skill{Managed: true, Enabled: true}, []string{"i", "d", "m", "p", "r"}},
		{"copies", skills.Skill{ConflictID: "copies"}, []string{"i", "c"}},
		{"parent", skills.Skill{Inherited: true}, []string{"i", "o"}},
		{"view only", skills.Skill{ReadOnly: true}, []string{"i"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions := actionsForSkill(test.skill)
			if len(actions) != len(test.want) {
				t.Fatalf("got %+v", actions)
			}
			for i, want := range test.want {
				if actions[i].event != want {
					t.Fatalf("action %d event=%q want=%q", i, actions[i].event, want)
				}
			}
		})
	}

	m := model(t)
	m, _ = key(m, "x")
	if m.pane != paneActions || !strings.Contains(m.View(), "Add to library") {
		t.Fatal("x did not open the action picker from the shared definitions")
	}
	if !containsShortcut(m.shortcuts(), "enter") {
		t.Fatal("action picker footer omitted Enter")
	}
}

func TestFooterPriorityPackingKeepsEssentialsAndMarksOverflow(t *testing.T) {
	m := model(t)
	m.width = 30
	footer := ansi.Strip(m.footer(m.width))
	for _, essential := range []string{"x", "?", "q"} {
		if !strings.Contains(footer, essential) {
			t.Fatalf("footer omitted essential %q: %q", essential, footer)
		}
	}
	if !strings.Contains(footer, "…") {
		t.Fatalf("footer did not mark omitted hints: %q", footer)
	}
}

func TestActionResultPreservesContextAndFallsBackOnlyAsNeeded(t *testing.T) {
	tests := []struct {
		action  string
		managed bool
		want    skillView
	}{
		{"adopt", true, viewLibrary},
		{"enable", true, viewLibrary},
		{"disable", true, viewLibrary},
		{"restore", false, viewOtherFolders},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			m := model(t)
			m.query = "alpha"
			m.filter = viewAll
			m.selectID = "alpha-id"
			m.selectAction = test.action
			result := skills.Result{Skills: []skills.Skill{{ID: "alpha-id", Name: "alpha", Managed: test.managed}}}
			next, _ := m.Update(loadedMsg{result: result})
			m = next.(Model)
			if m.filter != test.want || m.query != "alpha" {
				t.Fatalf("filter=%v query=%q", m.filter, m.query)
			}
			if selected, ok := m.selected(); !ok || selected.ID != "alpha-id" {
				t.Fatal("result was not selected")
			}

			m.query = "missing"
			m.selectID = "alpha-id"
			m.selectAction = test.action
			next, _ = m.Update(loadedMsg{result: result})
			m = next.(Model)
			if m.query != "" || m.filter != test.want {
				t.Fatalf("did not clear only the blocking query: filter=%v query=%q", m.filter, m.query)
			}
		})
	}
}

func TestNoticeSeverityAndDeduplicatedProblems(t *testing.T) {
	m := model(t)
	next, _ := m.Update(previewMsg{err: errors.New("preview failed")})
	m = next.(Model)
	if m.notice.level != noticeError || m.notice.text != "Could not prepare this change. preview failed" {
		t.Fatal("operation error did not use explicit error severity")
	}
	m.result.Skills = []skills.Skill{
		{ID: "one", Name: "alpha", Path: "/one", ConflictID: "copies", Issues: []string{"Different copies found in 2 locations"}},
		{ID: "two", Name: "alpha", Path: "/two", ConflictID: "copies", Issues: []string{"Different copies found in 2 locations"}},
	}
	if problems := m.problems(); len(problems) != 1 {
		t.Fatalf("got %d duplicate problems: %+v", len(problems), problems)
	}
}

func TestActionSpecificSuccessNotices(t *testing.T) {
	tests := map[string]string{
		"adopt":   "Added to library.",
		"resolve": "Kept this copy.",
		"enable":  "Enabled for agent discovery.",
		"disable": "Disabled for agent discovery.",
		"restore": "Removed from library.",
		"move":    "Moved to the destination library.",
		"copy":    "Copied to the destination library.",
	}
	for action, want := range tests {
		if got := successNotice(action); !strings.HasPrefix(got, want) {
			t.Fatalf("%s notice=%q want prefix=%q", action, got, want)
		}
	}
}

func containsShortcut(shortcuts []shortcut, event string) bool {
	for _, shortcut := range shortcuts {
		if shortcut.event == event {
			return true
		}
	}
	return false
}
