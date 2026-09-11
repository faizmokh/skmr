package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faizmokh/skmr/internal/manager"
	"github.com/faizmokh/skmr/internal/skills"
)

func TestSkillViewsPartitionLocalAndParentSkills(t *testing.T) {
	m := model(t)
	m.result.Skills = []skills.Skill{
		{ID: "library", Name: "library", Managed: true},
		{ID: "other", Name: "other"},
		{ID: "parent-library", Name: "parent-library", Managed: true, Inherited: true},
		{ID: "parent-other", Name: "parent-other", Inherited: true},
	}

	m.filter = viewLibrary
	assertSkillIDs(t, m.items(), "library")
	m.filter = viewOtherFolders
	assertSkillIDs(t, m.items(), "other")

	project := filepath.Join(m.service.Config.Home, "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	service, err := manager.New(manager.Config{
		Home:       m.service.Config.Home,
		DataHome:   m.service.Config.DataHome,
		ConfigHome: m.service.Config.ConfigHome,
		Project:    project,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.service = service
	m.filter = viewParentScopes
	assertSkillIDs(t, m.items(), "parent-library", "parent-other")
}

func TestViewAvailabilityAndCyclingFollowScope(t *testing.T) {
	m := model(t)
	if len(m.availableViews()) != 3 {
		t.Fatal("global scope should hide Parent scopes")
	}
	m.selectView(viewParentScopes)
	if m.filter != viewAll {
		t.Fatal("unavailable Parent scopes view should fall back to All skills")
	}
	m.filter = viewOtherFolders
	m.cycleView()
	if m.filter != viewAll {
		t.Fatal("global view cycle should wrap after Other folders")
	}

	project := filepath.Join(m.service.Config.Home, "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	service, err := manager.New(manager.Config{Home: m.service.Config.Home, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	m.service = service
	if len(m.availableViews()) != 4 {
		t.Fatal("project scope should show Parent scopes")
	}
	m.filter = viewOtherFolders
	m.cycleView()
	if m.filter != viewParentScopes {
		t.Fatal("project view cycle should include Parent scopes")
	}
}

func TestStateDescriptionsExplainLibraryControl(t *testing.T) {
	tests := []struct {
		skill skills.Skill
		want  string
	}{
		{skills.Skill{Managed: true, Enabled: true}, "exposed through skmr's shared discovery folder"},
		{skills.Skill{Managed: true}, "not exposed through skmr's shared discovery folder"},
		{skills.Skill{}, "Add it to the library"},
		{skills.Skill{ReadOnly: true}, "inspect this copy but cannot move or change it"},
		{skills.Skill{Inherited: true, OwnerProject: "/work/project"}, "Owned by /work/project"},
	}
	for _, test := range tests {
		if got := skillStateDescription(test.skill); !strings.Contains(got, test.want) {
			t.Fatalf("state description %q does not contain %q", got, test.want)
		}
	}
}

func TestEmptyStatesDistinguishCause(t *testing.T) {
	m := model(t)
	m.result.Skills = nil
	if got := strings.Join(m.emptyListLines(80), "\n"); !strings.Contains(got, "No skills found in this scope") {
		t.Fatal("missing no-skills empty state")
	}

	m = model(t)
	m.query = "missing"
	if got := strings.Join(m.emptyListLines(80), "\n"); !strings.Contains(got, `No skills match "missing"`) {
		t.Fatal("missing search empty state")
	}

	m.query = ""
	m.filter = viewLibrary
	if got := strings.Join(m.emptyListLines(80), "\n"); !strings.Contains(got, "No skills in Library") {
		t.Fatal("missing empty-view state")
	}
}

func assertSkillIDs(t *testing.T, items []skills.Skill, want ...string) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("got %d skills, want %d: %+v", len(items), len(want), items)
	}
	for i := range want {
		if items[i].ID != want[i] {
			t.Fatalf("skill %d is %q, want %q", i, items[i].ID, want[i])
		}
	}
}
