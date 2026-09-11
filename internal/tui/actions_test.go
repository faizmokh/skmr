package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/skills"
)

func TestSkillShortcutsRequireActions(t *testing.T) {
	for _, pane := range []paneMode{paneList, paneDetails} {
		for _, state := range []skills.Skill{{}, {Managed: true, Enabled: true}, {Managed: true}, {Inherited: true}, {ReadOnly: true}, {ConflictID: "copies", ConflictKind: "divergent"}} {
			m := model(t)
			state.ID, state.Name, state.Path = m.result.Skills[0].ID, "alpha", m.result.Skills[0].Path
			m.result.Skills[0] = state
			m.width, m.height, m.pane = 60, 18, pane
			for _, shortcut := range []string{"i", "v", "o", "a", "c", "m", "p", "r", "e", "d", " "} {
				next, cmd := key(m, shortcut)
				if cmd != nil || next.pane != pane || next.busy || next.pending != nil || next.transferAction != "" || next.service != m.service {
					t.Fatalf("shortcut %q ran outside Actions for %+v in pane %v", shortcut, state, pane)
				}
				if containsShortcut(m.shortcuts(), shortcut) {
					t.Fatalf("footer advertises standalone action %q", shortcut)
				}
			}
		}
	}
}

func TestActionsReadOnlyAndDivergentAvailability(t *testing.T) {
	for _, tc := range []struct {
		skill  skills.Skill
		events string
	}{
		{skills.Skill{ConflictID: "copies", ConflictKind: "divergent"}, "icv"},
		{skills.Skill{ConflictID: "copies", ConflictKind: "agent_config"}, "icv"},
		{skills.Skill{ReadOnly: true, ConflictID: "copies", ConflictKind: "divergent"}, "iv"},
		{skills.Skill{Inherited: true, ReadOnly: true, ConflictID: "copies", ConflictKind: "divergent"}, "ivo"},
		{skills.Skill{Issues: []string{"invalid"}}, "i"},
	} {
		got := ""
		for _, action := range actionsForSkill(tc.skill) {
			got += action.event
		}
		if got != tc.events {
			t.Fatalf("got %q want %q", got, tc.events)
		}
	}
	m := model(t)
	m.result.Skills[0].ReadOnly = true
	m, _ = key(m, "x")
	for _, shortcut := range []string{"a", "c", "e", "d", " ", "m", "p", "r", "o", "v"} {
		next, cmd := key(m, shortcut)
		if cmd != nil || next.pane != paneActions || next.busy || next.transferAction != "" {
			t.Fatalf("unavailable action %q ran", shortcut)
		}
	}
	m, _ = key(m, "enter")
	if m.pane != paneInstructions {
		t.Fatal("read-only instructions unavailable")
	}
}

func TestActionsFooterClickAndReturn(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {80, 24}, {60, 18}, {30, 10}} {
		for _, pane := range []paneMode{paneList, paneDetails} {
			m := model(t)
			m.result.Skills[0].ReadOnly = true
			m.width, m.height, m.pane = size[0], size[1], pane
			hit := -1
			for x := 0; x < m.width; x++ {
				if m.footerKeyAt(x) == "x" {
					hit = x
					break
				}
			}
			if hit < 0 {
				t.Fatalf("Actions not clickable at %v pane %v: %s", size, pane, m.footer(m.width))
			}
			next, cmd := m.Update(tea.MouseMsg{X: hit, Y: m.height - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			m = next.(Model)
			if cmd != nil || m.pane != paneActions {
				t.Fatal("footer did not open Actions")
			}
			cancelled, _ := key(m, "esc")
			if cancelled.pane != pane {
				t.Fatal("cancel lost originating pane")
			}
			m, _ = key(m, "enter")
			if m.pane != paneInstructions {
				t.Fatal("Enter did not read instructions")
			}
			m, _ = key(m, "esc")
			if m.pane != pane {
				t.Fatal("reader lost originating pane")
			}
		}
	}
}

func TestActionSelectionVisibleAfterNavigationAndResize(t *testing.T) {
	m := model(t)
	m.result.Skills[0].Managed = true
	m.result.Skills[0].ConflictID = "copies"
	m.result.Skills[0].ConflictKind = "divergent"
	m, _ = key(m, "x")
	actions := actionsForSkill(m.result.Skills[0])
	for _, size := range [][2]int{{100, 30}, {30, 10}, {80, 10}, {60, 18}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		for _, direction := range []string{"down", "up"} {
			for range len(actions) {
				m, _ = key(m, direction)
				view := ansi.Strip(m.View())
				if !strings.Contains(view, "› "+actions[m.actionCursor].label) {
					t.Fatalf("selection hidden at %v: %s", size, view)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > m.width {
						t.Fatalf("width overflow: %q", line)
					}
				}
				if len(strings.Split(view, "\n")) > m.height {
					t.Fatal("height overflow")
				}
			}
		}
	}
}

func TestActionsEnterStartsPreview(t *testing.T) {
	m := model(t)
	m, _ = key(m, "x")
	m, _ = key(m, "down")
	m, cmd := key(m, "enter")
	if cmd == nil {
		t.Fatal("Enter did not dispatch Add to library")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.pending == nil || m.pending.Action != "adopt" {
		t.Fatal("adoption did not require review")
	}
	m, _ = key(m, "esc")
	if m.pending != nil || m.pane != paneList {
		t.Fatal("cancel did not return to list")
	}
}

func TestGroupAndEmptySelectionHaveNoActions(t *testing.T) {
	for _, m := range []Model{groupedModel(t), model(t)} {
		if len(m.rows()) > 1 {
			m.result.Skills = nil
		}
		next, cmd := key(m, "x")
		if cmd != nil || next.pane == paneActions || containsShortcut(m.shortcuts(), "x") {
			t.Fatal("Actions available without a selected skill")
		}
	}
}
