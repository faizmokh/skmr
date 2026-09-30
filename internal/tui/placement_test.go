package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/faizmokh/skmr/internal/manager"
)

// completeReview drives the same preview/apply/reload messages as the running TUI.
func completeReview(t *testing.T, m Model, cmd tea.Cmd, action string) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("operation did not start")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.pending == nil || m.pending.Action != action {
		t.Fatalf("missing %s review: %+v (%s)", action, m.pending, m.notice.text)
	}
	m, cmd = key(m, "y")
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if m.notice.level == noticeError {
		t.Fatal(m.notice.text)
	}
	if cmd != nil {
		next, _ = m.Update(cmd())
		m = next.(Model)
	}
	return m
}

func TestScopeLoadAutomaticallyOwnsWritableSkills(t *testing.T) {
	m := model(t)
	m.autoAdopt = true
	next, _ := m.Update(m.load()())
	m = next.(Model)
	if m.notice.level == noticeError {
		t.Fatal(m.notice.text)
	}
	for _, item := range m.items() {
		if !item.Managed || !item.Enabled {
			t.Fatalf("writable skill was not automatically owned: %+v", item)
		}
	}
	m, cmd := actionKey(m, "r")
	m = completeReview(t, m, cmd, "restore")
	item, ok := m.selected()
	if !ok || item.Managed {
		t.Fatalf("reload reclaimed returned origin: %+v", item)
	}
}

func TestProjectPathAddSyncRemoveAndLibraryDelete(t *testing.T) {
	m := model(t)
	project := filepath.Join(m.service.Config.Home, "app")
	source := filepath.Join(m.service.Config.Home, "authoring", "outside")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: outside\ndescription: Example\n---\noriginal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := manager.New(manager.Config{Home: m.service.Config.Home, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	m = New(s)
	next, _ := m.Update(m.Init()())
	m = next.(Model)
	m, _ = key(m, "I")
	m, _ = key(m, "l")
	m, _ = key(m, source)
	m, cmd := key(m, "enter")
	m = completeReview(t, m, cmd, "add")
	m.selectView(viewLibrary)
	if len(m.items()) != 1 || !m.items()[0].Installed || m.currentView().labels.full != "Project placements" {
		t.Fatalf("project placement missing from its view: %+v", m.items())
	}
	m.selectView(viewOtherFolders)
	for _, item := range m.items() {
		if item.Name == "outside" {
			t.Fatal("managed placement appeared as an unmanaged folder")
		}
	}
	link := filepath.Join(s.Shared(), "outside")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	m, cmd = key(m, "S")
	m = completeReview(t, m, cmd, "sync")
	if actual, err := os.Readlink(link); err != nil || actual != target {
		t.Fatal("sync did not restore central placement", actual, err)
	}
	m.selectView(viewLibrary)
	m, cmd = actionKey(m, "o")
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.service.Scope() != "global" || len(m.items()) != 1 || m.items()[0].Name != "outside" {
		t.Fatalf("placement did not open in the library: %+v", m.items())
	}
	// A project placement protects the content even while globally disabled.
	m, cmd = actionKey(m, "D")
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.pending != nil || !strings.Contains(m.notice.text, "project") {
		t.Fatal("library delete did not respect project dependency", m.notice.text)
	}
	m = New(s)
	next, _ = m.Update(m.Init()())
	m = next.(Model)
	m.selectView(viewLibrary)
	m, cmd = actionKey(m, "Z")
	m = completeReview(t, m, cmd, "remove")
	if _, err = os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("project remove left its link", err)
	}
	global, err := manager.New(manager.Config{Home: s.Config.Home})
	if err != nil {
		t.Fatal(err)
	}
	m = New(global)
	next, _ = m.Update(m.Init()())
	m = next.(Model)
	for i, item := range m.items() {
		if item.Name == "outside" {
			m.cursor = i
		}
	}
	m, cmd = actionKey(m, "D")
	_ = completeReview(t, m, cmd, "delete")
	if _, err = os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatal("delete removed authoring source", err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("library content survived delete", err)
	}
}

func TestProjectGroupPresetRemainsAfterPresetDeletion(t *testing.T) {
	m := adoptSelected(t, model(t))
	plan, err := m.service.PreviewOperation(manager.OperationRequest{Action: "group-create", Arguments: []string{"starter", "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.service.ApplyOperation(plan); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(m.service.Config.Home, "preset-project")
	if err = os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	s, err := manager.New(manager.Config{Home: m.service.Config.Home, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	m = New(s)
	next, _ := m.Update(m.Init()())
	m = next.(Model)
	m, cmd := key(m, "G")
	next, _ = m.Update(cmd())
	m = next.(Model)
	m, cmd = key(m, "enter")
	m = completeReview(t, m, cmd, "add")
	m, cmd = key(m, "G")
	next, _ = m.Update(cmd())
	m = next.(Model)
	m, cmd = key(m, "d")
	m = completeReview(t, m, cmd, "group-delete")
	m, _ = key(m, "esc")
	m, cmd = key(m, "S")
	_ = completeReview(t, m, cmd, "sync")
	if _, err := os.Stat(filepath.Join(s.Shared(), "alpha", "SKILL.md")); err != nil {
		t.Fatal("preset deletion changed project requests", err)
	}
}

func TestRemoteAddAndUpdateThroughTUIManager(t *testing.T) {
	m := model(t)
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *" --list"*) printf '◇  Available Skills\n│    remote\n│      Example\n└  Use --skill <name> to install\n'; exit 0 ;;
esac
path="$(pwd -P)/.agents/skills/remote"
mkdir -p "$path"
printf '%s\n' '---' 'name: remote' 'description: Example' '---' "version ${SKMR_TEST_VERSION:-1}" > "$path/SKILL.md"
printf '[{"name":"remote","status":"installed","path":"%s"}]\n' "$path"
`
	if err := os.WriteFile(filepath.Join(bin, "npx"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	m, _ = key(m, "I")
	m, _ = key(m, "p")
	m, _ = key(m, "https://github.com/example/skills")
	m, cmd := key(m, "enter")
	next, cmd := m.Update(cmd())
	m = completeReview(t, next.(Model), cmd, "add")
	m.selectView(viewLibrary)
	if len(m.items()) != 1 || !m.items()[0].Remote || !m.items()[0].Enabled {
		t.Fatalf("remote import and global placement did not complete: %+v", m.items())
	}
	t.Setenv("SKMR_TEST_VERSION", "2")
	m, cmd = actionKey(m, "u")
	m = completeReview(t, m, cmd, "update")
	item, _ := m.selected()
	body, err := os.ReadFile(filepath.Join(item.Path, "SKILL.md"))
	if err != nil || !strings.Contains(string(body), "version 2") {
		t.Fatal("TUI update did not replace content", string(body), err)
	}
}
