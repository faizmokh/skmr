package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/skills"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func groupedModel(t *testing.T) Model {
	m := model(t)
	group := &skills.Group{ID: "bundle", Name: "bundle", Source: "/skills/bundle", Scope: "global"}
	for i := range m.result.Skills {
		m.result.Skills[i].Group = group
	}
	return m
}
func TestGroupNavigationAndActions(t *testing.T) {
	m := groupedModel(t)
	if len(m.rows()) != 1 {
		t.Fatal("not collapsed")
	}
	details := strings.Join(m.groupDetails(m.rows()[0], 80), "\n")
	for _, want := range []string{"Other folders"} {
		if !strings.Contains(details, want) {
			t.Fatalf("group details missing %q", want)
		}
	}
	if strings.Contains(details, "Parent scopes") {
		t.Fatal("global group details should hide Parent scopes")
	}
	for _, k := range []string{"a", "c", "e", "d", " ", "m", "p", "r"} {
		next, cmd := key(m, k)
		if cmd != nil || next.pending != nil || next.transferAction != "" {
			t.Fatalf("group action %q", k)
		}
	}
	m, _ = key(m, "right")
	if len(m.rows()) != 3 || m.cursor != 0 {
		t.Fatal("expand")
	}
	m, _ = key(m, "right")
	if s, ok := m.selected(); !ok || s.Name != "alpha" {
		t.Fatal("enter")
	}
	m, _ = key(m, "left")
	if m.cursor != 0 {
		t.Fatal("parent")
	}
	m, _ = key(m, "left")
	if len(m.rows()) != 1 {
		t.Fatal("collapse")
	}
	n, _ := m.Update(tea.MouseMsg{X: 2, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = n.(Model)
	if len(m.rows()) != 3 {
		t.Fatal("mouse expand")
	}
	n, cmd := m.Update(tea.MouseMsg{X: 2, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = n.(Model)
	if s, ok := m.selected(); !ok || s.Name != "beta" || cmd == nil {
		t.Fatal("mouse child selection")
	}
	m, _ = key(m, "left")
	n, _ = m.Update(contentMsg{id: m.result.Skills[1].ID, content: "stale"})
	if n.(Model).content != "" {
		t.Fatal("late preview overwrote group")
	}
}
func TestGroupSearchFilterAndRefresh(t *testing.T) {
	m := groupedModel(t)
	m.query = "alpha"
	if len(m.rows()) != 2 || m.rows()[0].total != 2 || len(m.rows()[0].members) != 1 {
		t.Fatal("search reveal/count")
	}
	m.query = "bundle"
	if len(m.rows()) != 3 {
		t.Fatal("group search")
	}
	m, _ = key(m, "esc")
	if len(m.rows()) != 1 {
		t.Fatal("search changed expansion")
	}
	m.result.Skills[0].Managed = true
	m.filter = viewLibrary
	if len(m.rows()) != 1 || len(m.rows()[0].members) != 1 {
		t.Fatal("filter")
	}
	m.result.Skills[0].Managed = false
	m.filter = viewLibrary
	if len(m.rows()) != 0 {
		t.Fatal("empty group visible")
	}
	m.filter = viewAll
	m, _ = key(m, "right")
	m, _ = key(m, "right")
	m, _ = key(m, "j")
	selected, _ := m.selected()
	result := m.result
	result.Skills = append([]skills.Skill{{ID: "new", Name: "aardvark"}}, result.Skills...)
	n, _ := m.Update(loadedMsg{result: result})
	m = n.(Model)
	if s, ok := m.selected(); !ok || s.ID != selected.ID {
		t.Fatal("refresh lost selection")
	}
	m.selectID = selected.ID
	m.setExpanded("bundle", false)
	m.filter = viewLibrary
	n, _ = m.Update(loadedMsg{result: result})
	m = n.(Model)
	if s, ok := m.selected(); !ok || s.ID != selected.ID {
		t.Fatal("operation selection not revealed")
	}
	globalKey := m.expandedKey("bundle")
	m.service.Store += "/project"
	if m.isExpanded("bundle") {
		t.Fatal("scope expansion leaked")
	}
	m.service.Store = strings.TrimSuffix(m.service.Store, "/project")
	if !m.expanded[globalKey] {
		t.Fatal("scope expansion lost")
	}
}
func TestGroupedAdoptionAndDisable(t *testing.T) {
	m := model(t)
	path := filepath.Join(m.service.Config.Home, ".agents", "skills", "bundle", "gamma")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: gamma\ndescription: Example\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := m.service.List()
	if err != nil {
		t.Fatal(err)
	}
	var original skills.Skill
	for _, s := range before.Skills {
		if s.Name == "gamma" {
			original = s
		}
	}
	for _, action := range []string{"adopt", "disable"} {
		arg := original.ID
		if action == "adopt" {
			arg = path
		}
		plan, err := m.service.Preview(action, arg)
		if err != nil {
			t.Fatal(err)
		}
		if err = m.service.Apply(plan); err != nil {
			t.Fatal(err)
		}
		result, err := m.service.List()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range result.Skills {
			if s.ID == original.ID {
				found = true
				if s.Group == nil || s.Group.ID != original.Group.ID {
					t.Fatal("lost original group")
				}
			}
		}
		if !found {
			t.Fatal("managed skill missing")
		}
	}
}
func TestGroupLayoutsAndHitTesting(t *testing.T) {
	m := groupedModel(t)
	m.result.Skills[0].Group.Name = "long bundle 界 " + strings.Repeat("name", 20) + "\x1b]52;c;BADPAYLOAD\a"
	m.result.Skills[0].Issues = []string{"broken"}
	for i := 0; i < 30; i++ {
		s := m.result.Skills[0]
		s.ID = fmt.Sprint(i)
		s.Name = fmt.Sprintf("child-%02d", i)
		m.result.Skills = append(m.result.Skills, s)
	}
	for _, expanded := range []bool{false, true} {
		m.setExpanded("bundle", expanded)
		for _, size := range [][2]int{{100, 30}, {76, 24}, {60, 20}, {30, 10}, {20, 5}} {
			m.width, m.height = size[0], size[1]
			m.cursor = len(m.rows()) - 1
			view := m.View()
			if strings.Contains(view, "BADPAYLOAD") {
				t.Fatal("unsafe group name")
			}
			if size[0] < 30 {
				continue
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("overflow %v: %q", size, line)
				}
			}
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatal("height overflow")
			}
			h := m.bodyHeight() - 2
			for y := 0; y < h; y++ {
				index, ok := m.skillAt(2, y+m.bodyTop()+1)
				want := m.rowStart(h) + y
				if ok != (want < len(m.rows())) || ok && index != want {
					t.Fatal("hit testing disagrees with visible rows")
				}
			}
		}
	}
}
