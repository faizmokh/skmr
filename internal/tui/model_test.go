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
		t.Fatal("managed filter failed")
	}
}
func TestConfirmCancelAndApply(t *testing.T) {
	m := model(t)
	m, cmd := key(m, "a")
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
	m, cmd = key(m, "a")
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
	m, cmd = key(m, "d")
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
func TestReadOnlyAndResize(t *testing.T) {
	m := model(t)
	m.result.Skills[0].ReadOnly = true
	m, cmd := key(m, "a")
	if cmd != nil || !strings.Contains(m.message, "read-only") {
		t.Fatal("read-only adoption allowed")
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
}

func TestViewHierarchyHelpAndDirectFilters(t *testing.T) {
	m := model(t)
	m.content = "# Alpha\nInstructions"
	m.width, m.height = 100, 24
	view := m.View()
	for _, want := range []string{"SKMR", "Skills 2/2", "Details", "discovered", "SKILL.md", "press / to search"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q", want)
		}
	}

	m, _ = key(m, "?")
	if view = m.View(); !strings.Contains(view, "Help") || !strings.Contains(view, "Navigate") {
		t.Fatal("help pane not shown")
	}

	m, _ = key(m, "2")
	if m.filter != 1 || len(m.items()) != 0 {
		t.Fatal("direct filter shortcut failed")
	}
}

func TestMouseSelectionFiltersSearchAndScrolling(t *testing.T) {
	m := model(t)
	m.width, m.height = 100, 24

	next, cmd := m.Update(tea.MouseMsg{X: 2, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if skill, _ := m.selected(); skill.Name != "beta" || cmd == nil {
		t.Fatal("list click did not select the second skill")
	}

	next, _ = m.Update(tea.MouseMsg{X: 18, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.filter != 1 {
		t.Fatal("filter click did not select managed skills")
	}

	m.filter = 0
	next, _ = m.Update(tea.MouseMsg{X: 99, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if !m.searching {
		t.Fatal("search click did not enter search mode")
	}

	m.searching = false
	m.cursor = 0
	next, cmd = m.Update(tea.MouseMsg{X: 2, Y: 4, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
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
