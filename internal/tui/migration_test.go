package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/manager"
)

func firstRunMigrationModel(t *testing.T) Model {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := manager.New(manager.Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, name, body string) {
		t.Helper()
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: Example skill\n---\n" + body
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".codex", "skills", "alpha"), "alpha", "alpha")
	write(filepath.Join(home, ".pi", "agent", "skills", "beta"), "beta", "beta")
	write(filepath.Join(home, ".agents", "skills", "duplicate"), "duplicate", "shared")
	write(filepath.Join(home, ".codex", "skills", "duplicate"), "duplicate", "legacy")
	write(filepath.Join(home, ".codex", "skills", ".system", "builtin"), "builtin", "system")
	invalid := filepath.Join(home, ".pi", "agent", "skills", "invalid")
	if err := os.MkdirAll(invalid, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalid, "SKILL.md"), []byte("bad"), 0644); err != nil {
		t.Fatal(err)
	}
	m := New(service)
	next, _ := m.Update(m.Init()())
	return next.(Model)
}

func migrationRowIndex(m Model, kind migrationRowKind, name string) int {
	for i, row := range m.migration.rows() {
		if row.kind == kind && row.name == name {
			return i
		}
	}
	return -1
}

func TestFirstRunMigrationSelectionReviewAndApply(t *testing.T) {
	m := firstRunMigrationModel(t)
	if m.migration == nil || !m.migration.initial || m.migration.selectedCount() != 2 {
		t.Fatalf("first-run picker=%+v", m.migration)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Set up your skill library") || strings.Contains(view, "builtin") || !strings.Contains(view, "SKILL.md needs YAML frontmatter") {
		t.Fatalf("bad first-run view: %s", view)
	}

	conflict := migrationRowIndex(m, migrationConflictRow, "duplicate")
	if conflict < 0 {
		t.Fatal("duplicate group missing")
	}
	m.migration.cursor = conflict
	m, _ = key(m, "right")
	m, _ = key(m, "right")
	if row := m.migration.rows()[m.migration.cursor]; row.kind != migrationCopyRow {
		t.Fatal("right did not enter duplicate choices")
	}
	m, _ = key(m, " ")
	if m.migration.selectedCount() != 3 {
		t.Fatal("kept-copy choice was not selected")
	}
	m, cmd := key(m, "enter")
	if cmd == nil {
		t.Fatal("review did not start")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.pendingBatch == nil || !strings.Contains(ansi.Strip(m.View()), "Confirm add skills") {
		t.Fatal("combined review missing")
	}
	m, _ = key(m, "n")
	if m.pendingBatch != nil || m.migration == nil {
		t.Fatal("review did not return to selection")
	}
	m, cmd = key(m, "enter")
	next, _ = m.Update(cmd())
	m = next.(Model)
	m, cmd = key(m, "y")
	next, load := m.Update(cmd())
	m = next.(Model)
	if load == nil || m.migration != nil || m.notice.level != noticeSuccess {
		t.Fatalf("batch did not complete: %+v", m.notice)
	}
	next, _ = m.Update(load())
	m = next.(Model)
	if m.filter != viewLibrary || len(m.items()) != 3 {
		t.Fatalf("library result missing: filter=%v items=%d", m.filter, len(m.items()))
	}
	if !strings.Contains(m.notice.text, "Added 3 skills. Skipped 1.") {
		t.Fatalf("completion counts missing: %s", m.notice.text)
	}
	setup, err := os.ReadFile(filepath.Join(m.service.Store, "setup.json"))
	if err != nil || !strings.Contains(string(setup), `"outcome": "completed"`) {
		t.Fatalf("completion was not persisted: %s %v", setup, err)
	}
}

func TestMigrationSkipPersistsAndManualActionReopens(t *testing.T) {
	m := firstRunMigrationModel(t)
	m, cmd := key(m, "esc")
	if cmd == nil || !m.busy {
		t.Fatal("setup skip was not persisted")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.migration != nil || !strings.Contains(m.notice.text, "Press A") {
		t.Fatal("skip did not return to the library")
	}
	m, _ = key(m, "A")
	if m.migration == nil || m.migration.initial {
		t.Fatal("manual add-skills action did not reopen the picker")
	}
	m, _ = key(m, "esc")
	if m.migration != nil || m.notice.text != "No changes made." {
		t.Fatal("manual picker did not cancel cleanly")
	}
}

func TestMigrationClearSelectAllDisabledMouseAndBounds(t *testing.T) {
	m := firstRunMigrationModel(t)
	m, _ = key(m, "n")
	if m.migration.selectedCount() != 0 {
		t.Fatal("clear selection failed")
	}
	m, _ = key(m, "a")
	if m.migration.selectedCount() != 2 {
		t.Fatal("select all included unsafe skills")
	}
	invalid := migrationRowIndex(m, migrationSkillRow, "invalid")
	m.migration.cursor = invalid
	m, _ = key(m, " ")
	if m.notice.level != noticeWarning || m.migration.selectedCount() != 2 {
		t.Fatal("invalid skill was selectable")
	}

	m.migration.cursor = migrationRowIndex(m, migrationSkillRow, "alpha")
	y := -1
	for rowY := 0; rowY < m.height; rowY++ {
		if index, ok := m.migrationRowAt(rowY); ok && index == m.migration.cursor {
			y = rowY
			break
		}
	}
	if y < 0 {
		t.Fatal("selected row has no mouse target")
	}
	next, _ := m.Update(tea.MouseMsg{X: 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.migration.selectedCount() != 1 {
		t.Fatal("mouse did not toggle a migration row")
	}

	for _, size := range [][2]int{{30, 10}, {60, 18}, {100, 30}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > m.width {
				t.Fatalf("%v overflow: %q", size, line)
			}
		}
		if len(strings.Split(view, "\n")) > m.height {
			t.Fatalf("%v height overflow", size)
		}
	}
}
