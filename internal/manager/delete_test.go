package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faizmokh/skmr/internal/skills"
)

func disableBeforeDelete(t *testing.T, s *Service, record Record) {
	t.Helper()
	if s.Config.Project != "" {
		return // Legacy project libraries remain readable for recovery.
	}
	plan, err := s.Preview("disable", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(plan); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRejectsUnmanagedInBothScopes(t *testing.T) {
	for _, projectScope := range []bool{false, true} {
		t.Run(map[bool]string{false: "global", true: "project"}[projectScope], func(t *testing.T) {
			global, path := fixture(t)
			s := global
			if projectScope {
				s = projectService(t, global, "delete-project")
				path = filepath.Join(s.Config.Project, ".agents", "skills", "sample")
				skill(t, path)
			}
			if _, err := s.PreviewDelete(skills.ID(path)); err == nil || !strings.Contains(err.Error(), "outside the library") || absent(path) {
				t.Fatalf("unmanaged skill was eligible for deletion: %v", err)
			}
		})
	}
}

func TestDeleteManagedAndRecover(t *testing.T) {
	for _, projectScope := range []bool{false, true} {
		t.Run(map[bool]string{false: "global", true: "project"}[projectScope], func(t *testing.T) {
			global, path := fixture(t)
			s := global
			if projectScope {
				s = projectService(t, global, "managed-project")
				path = filepath.Join(s.Config.Project, ".agents", "skills", "sample")
				skill(t, path)
			}
			record := apply(t, s, "adopt", path).Record
			disableBeforeDelete(t, s, record)
			plan, err := s.PreviewDelete(record.ID)
			if err != nil || !plan.Managed || !strings.Contains(plan.String(), "Remove owned link") {
				t.Fatalf("bad preview: %+v %v", plan, err)
			}
			if err := atomicJSON(s.deletionJournalPath(), plan); err != nil {
				t.Fatal(err)
			}
			if err := s.Recover(); err != nil {
				t.Fatal(err)
			}
			manifest, err := s.load()
			if err != nil || len(manifest.Records) != 0 || !absent(plan.Source) || !absent(s.deletionJournalPath()) {
				t.Fatalf("incomplete recovery: %+v %v", manifest, err)
			}
			for _, link := range record.Links {
				if !absent(link) {
					t.Fatalf("owned link remains: %s", link)
				}
			}
		})
	}
}

func TestDeleteRecoversAfterStaging(t *testing.T) {
	s, path := fixture(t)
	record := apply(t, s, "adopt", path).Record
	disableBeforeDelete(t, s, record)
	plan, err := s.PreviewDelete(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(s.deletionJournalPath(), plan); err != nil {
		t.Fatal(err)
	}
	for _, link := range record.Links {
		if err := unlink(link, record.Library); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(plan.Source, plan.Stage); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	if !absent(plan.Stage) || !absent(s.deletionJournalPath()) {
		t.Fatal("staged deletion was not recovered")
	}
}

func TestDeleteRejectsReadOnlyAndInherited(t *testing.T) {
	global, _ := fixture(t)
	builtin := filepath.Join(global.Config.Home, ".codex", "skills", ".system", "builtin")
	skill(t, builtin)
	if _, err := global.PreviewDelete(skills.ID(builtin)); err == nil || !strings.Contains(err.Error(), "view only") {
		t.Fatal("built-in skill could be deleted", err)
	}
	project := projectService(t, global, "read-only-project")
	result, err := project.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Skills {
		if item.Inherited {
			if _, err := project.PreviewDelete(item.ID); err == nil || !strings.Contains(err.Error(), "view only") {
				t.Fatal("inherited skill could be deleted", err)
			}
		}
	}
}

func TestDeleteManagedBackupsAndReplacedLink(t *testing.T) {
	s, path := fixture(t)
	other := filepath.Join(s.Config.Home, ".pi", "agent", "skills", "sample")
	skill(t, other)
	plan, err := s.Preview("resolve", skills.ID(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Record.Origins) != 2 {
		t.Fatalf("duplicate was not captured: %+v", plan.Record.Origins)
	}
	if err := s.Apply(plan); err != nil {
		t.Fatal(err)
	}
	record := plan.Record
	// A separate, replaced discovery link must never be removed by deletion.
	link := record.Links[0]
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(link, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewDelete(record.ID); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatal("replaced link accepted", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	// Restore the owned link so disabling can remove the global placement.
	if err := os.Symlink(record.Library, link); err != nil {
		t.Fatal(err)
	}
	disableBeforeDelete(t, s, record)
	deletePlan, err := s.PreviewDelete(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deletePlan.String(), "Delete stored duplicate") {
		t.Fatal("duplicate backup omitted from preview")
	}
	backup := plan.Record.Origins[1].Backup
	if absent(backup) {
		t.Fatal("duplicate backup missing before delete")
	}
	if err := s.ApplyDelete(deletePlan); err != nil {
		t.Fatal(err)
	}
	if !absent(deletePlan.Source) || !absent(other) || !absent(backup) {
		t.Fatal("managed deletion left content or backup")
	}
}

func TestDeleteBlockedByGroupsAndRegisteredProjects(t *testing.T) {
	global, project, record := packageFixture(t)
	if _, err := global.CreateGroup("bundle", []string{record.Name}); err != nil {
		t.Fatal(err)
	}
	if _, err := global.PreviewDelete(record.ID); err == nil || !strings.Contains(err.Error(), "@bundle") {
		t.Fatal("group dependency missed", err)
	}
	if err := global.DeleteGroup("bundle"); err != nil {
		t.Fatal(err)
	}
	add, err := project.PreviewPackages("add", []string{record.Name})
	if err != nil {
		t.Fatal(err)
	}
	if err := project.ApplyPackages(add); err != nil {
		t.Fatal(err)
	}
	registry, err := global.loadProjectRegistry()
	if err != nil || len(registry.Projects) != 1 {
		t.Fatalf("project was not registered: %+v %v", registry, err)
	}
	if _, err := global.PreviewDelete(record.ID); err == nil || !strings.Contains(err.Error(), project.Config.Project) {
		t.Fatal("project dependency missed", err)
	}
	if err := os.Remove(global.registryPath()); err != nil {
		t.Fatal(err)
	}
	syncPlan, err := project.PreviewPackages("sync", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := project.ApplyPackages(syncPlan); err != nil {
		t.Fatal(err)
	}
	registry, err = global.loadProjectRegistry()
	if err != nil || len(registry.Projects) != 1 {
		t.Fatalf("sync did not enroll existing project: %+v %v", registry, err)
	}
	remove, err := project.PreviewPackages("remove", []string{record.Name})
	if err != nil {
		t.Fatal(err)
	}
	if err := project.ApplyPackages(remove); err != nil {
		t.Fatal(err)
	}
	registry, err = global.loadProjectRegistry()
	if err != nil || len(registry.Projects) != 0 {
		t.Fatalf("empty project remained registered: %+v %v", registry, err)
	}
	if _, err := global.PreviewDelete(record.ID); err != nil {
		t.Fatal(err)
	}
}
