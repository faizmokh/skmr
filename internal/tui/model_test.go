package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/manager"
	"github.com/faizmokh/skmr/internal/skills"
)

func model(t *testing.T) Model {
	t.Helper()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s, e := manager.New(manager.Config{Home: home})
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"alpha", "beta"} {
		p := filepath.Join(home, ".agents", "skills", name)
		if e = os.MkdirAll(p, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Example\n---\nPreview text"), 0644); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.MarkSetup("skipped"); e != nil {
		t.Fatal(e)
	}
	m := New(s)
	next, _ := m.Update(m.Init()())
	return next.(Model)
}
func key(m Model, k string) (Model, tea.Cmd) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	switch k {
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	}
	n, c := m.Update(msg)
	return n.(Model), c
}

// actionKey exercises an operation through the Actions menu.
func actionKey(m Model, k string) (Model, tea.Cmd) {
	m, _ = key(m, "x")
	return key(m, k)
}
func TestNavigationSearchAndPreview(t *testing.T) {
	m := model(t)
	if s, _ := m.selected(); s.Name != "alpha" {
		t.Fatal("initial selection")
	}
	m, cmd := key(m, "j")
	if s, _ := m.selected(); s.Name != "beta" {
		t.Fatal("navigation failed")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(m.content, "Preview text") {
		t.Fatal("preview not loaded")
	}
	m, _ = key(m, "/")
	m, _ = key(m, "alpha")
	if len(m.items()) != 1 {
		t.Fatal("filter failed")
	}
	m, _ = key(m, "enter")
	m, _ = key(m, "esc")
	if len(m.items()) != 2 {
		t.Fatal("clear failed")
	}
	m, _ = key(m, "f")
	if len(m.items()) != 0 {
		t.Fatal("library view failed")
	}
}
func TestConfirmCancelAndApply(t *testing.T) {
	m := model(t)
	m, cmd := actionKey(m, "a")
	n, _ := m.Update(cmd())
	m = n.(Model)
	if m.pending == nil {
		t.Fatal("adoption skipped preview")
	}
	original := m.pending.Record.Original
	m, _ = key(m, "n")
	st, e := os.Lstat(original)
	if e != nil || !st.IsDir() {
		t.Fatal("cancel changed files")
	}
	m, cmd = actionKey(m, "a")
	n, _ = m.Update(cmd())
	m = n.(Model)
	m, cmd = key(m, "y")
	n, cmd = m.Update(cmd())
	m = n.(Model)
	n, _ = m.Update(cmd())
	m = n.(Model)
	s, _ := m.selected()
	if !s.Managed {
		t.Fatal("adoption failed")
	}
	m, cmd = actionKey(m, "d")
	n, cmd = m.Update(cmd())
	m = n.(Model)
	if m.pending != nil {
		t.Fatal("disable requested unnecessary confirmation")
	}
	n, cmd = m.Update(cmd())
	m = n.(Model)
	n, _ = m.Update(cmd())
	m = n.(Model)
	s, _ = m.selected()
	if s.Enabled {
		t.Fatal("disable failed")
	}
}

func adoptSelected(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := actionKey(m, "a")
	next, _ := m.Update(cmd())
	m = next.(Model)
	m, cmd = key(m, "y")
	next, cmd = m.Update(cmd())
	m = next.(Model)
	next, _ = m.Update(cmd())
	return next.(Model)
}

func TestSpaceTogglesManagedSkill(t *testing.T) {
	m := adoptSelected(t, model(t))
	selected, _ := m.selected()
	if !selected.Enabled {
		t.Fatal("skill was not enabled after adoption")
	}
	m, cmd := actionKey(m, " ")
	next, cmd := m.Update(cmd())
	m = next.(Model)
	next, cmd = m.Update(cmd())
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	selected, _ = m.selected()
	if selected.Enabled {
		t.Fatal("space did not disable the skill")
	}
}

func TestMovePromptAppliesAndOpensDestination(t *testing.T) {
	m := adoptSelected(t, model(t))
	project := filepath.Join(m.service.Config.Home, "destination")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	m, _ = actionKey(m, "m")
	if m.transferAction != "move" {
		t.Fatal("move prompt did not open")
	}
	cancelled, _ := key(m, "esc")
	if cancelled.transferAction != "" {
		t.Fatal("move prompt did not cancel")
	}
	m = cancelled
	m, _ = actionKey(m, "m")
	m, _ = key(m, project)
	m, cmd := key(m, "enter")
	if cmd == nil {
		t.Fatal("move preview did not start")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.pendingTransfer == nil {
		t.Fatal("move preview not shown")
	}
	m, cmd = key(m, "y")
	next, cmd = m.Update(cmd())
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	selected, _ := m.selected()
	if m.service.Config.Project != project || !selected.Managed || selected.OwnerProject != project {
		t.Fatalf("destination did not open with moved skill selected: %+v", selected)
	}
}
func TestReadOnlyAndResize(t *testing.T) {
	m := model(t)
	m.result.Skills[0].ReadOnly = true
	m, cmd := actionKey(m, "a")
	if cmd != nil || m.pane != paneActions || m.busy {
		t.Fatal("view-only adoption allowed")
	}
	m.result.Skills[0] = skills.Skill{Name: "evil\x1b]52;c;YXR0YWNr\a", Description: "\x1b[2Jbad", Path: "/path", Issues: []string{"broken"}}
	for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 30}, {Width: 60, Height: 20}, {Width: 30, Height: 10}, {Width: 20, Height: 5}} {
		n, _ := m.Update(size)
		m = n.(Model)
		view := m.View()
		if strings.Contains(view, "YXR0YWNr") || strings.Contains(view, "\x1b[2J") {
			t.Fatal("unsafe view")
		}
		if size.Width >= 30 {
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size.Width {
					t.Fatalf("line overflows %d: %q", size.Width, line)
				}
			}
			if len(strings.Split(view, "\n")) > size.Height {
				t.Fatal("height overflow")
			}
		}
	}
}
func TestScopeSwitch(t *testing.T) {
	m := model(t)
	p := filepath.Join(m.service.Config.Home, "project")
	if e := os.MkdirAll(p, 0755); e != nil {
		t.Fatal(e)
	}
	m.project = p
	n, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = n.(Model)
	if m.service.Scope() != "project" || cmd == nil {
		t.Fatal("scope switch failed")
	}
	n, _ = m.Update(cmd())
	m = n.(Model)
	for _, s := range m.items() {
		if !s.Inherited {
			t.Fatal("global not inherited")
		}
	}
	ownerID := m.items()[0].ID
	m, cmd = actionKey(m, "o")
	if m.service.Scope() != "global" || cmd == nil {
		t.Fatal("owning scope did not open")
	}
	n, _ = m.Update(cmd())
	m = n.(Model)
	selected, _ := m.selected()
	if selected.ID != ownerID {
		t.Fatal("owning scope did not retain selection")
	}
}

func TestViewHierarchyHelpAndDirectFilters(t *testing.T) {
	m := model(t)
	m.content = "# Alpha\nInstructions"
	m.width, m.height = 100, 24
	view := m.View()
	for _, want := range []string{"SKMR", "Skill library", "All skills", "control agent discovery", "Skills 2/2", "Details", "outside library", "Add it to the library", "SKILL.md", "press / to search"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q", want)
		}
	}

	m, _ = key(m, "?")
	if view = m.View(); !strings.Contains(view, "Help") || !strings.Contains(view, "Navigate") {
		t.Fatal("help pane not shown")
	}
	m, _ = key(m, "esc")

	m, _ = key(m, "2")
	if m.filter != 1 || len(m.items()) != 0 {
		t.Fatal("direct filter shortcut failed")
	}
}

func TestMouseSelectionFiltersSearchAndScrolling(t *testing.T) {
	m := model(t)
	m.width, m.height = 100, 24

	next, cmd := m.Update(tea.MouseMsg{X: 2, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if skill, _ := m.selected(); skill.Name != "beta" || cmd == nil {
		t.Fatal("list click did not select the second skill")
	}

	next, _ = m.Update(tea.MouseMsg{X: 25, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.filter != viewLibrary {
		t.Fatal("view click did not select library skills")
	}

	m.filter = viewAll
	next, _ = m.Update(tea.MouseMsg{X: 99, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if !m.searching {
		t.Fatal("search click did not enter search mode")
	}

	m.searching = false
	m.cursor = 0
	next, cmd = m.Update(tea.MouseMsg{X: 2, Y: 5, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.cursor != 1 || cmd == nil {
		t.Fatal("list wheel did not move selection")
	}

	m.offset = 0
	next, _ = m.Update(tea.MouseMsg{X: 70, Y: 8, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.offset != 3 {
		t.Fatal("detail wheel did not scroll preview")
	}
}

func TestConflictIndicatorAndKeepCopyAction(t *testing.T) {
	m := model(t)
	duplicate := filepath.Join(m.service.Config.Home, ".codex", "skills", "alpha")
	if err := os.MkdirAll(duplicate, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(duplicate, "SKILL.md"), []byte("---\nname: alpha\ndescription: Conflicting copy\n---\nDifferent"), 0644); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	view := m.View()
	if !strings.Contains(view, "different copies") || !strings.Contains(view, "Copies  different copies · 2") || strings.Contains(view, "divergent") {
		t.Fatalf("conflict details missing: %s", view)
	}
	m, cmd = actionKey(m, "c")
	if cmd == nil {
		t.Fatal("keep-copy action did not create a preview command")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.pending == nil || m.pending.Action != "resolve" {
		t.Fatal("keep-copy preview not shown")
	}
}
