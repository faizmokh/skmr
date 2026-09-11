package manager

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func packageFixture(t *testing.T) (*Service, *Service, Record) {
	t.Helper()
	global, source := fixture(t)
	record := apply(t, global, "adopt", source).Record
	apply(t, global, "disable", record.ID)
	project := projectService(t, global, "package-project")
	if err := os.Mkdir(filepath.Join(project.Config.Project, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	return global, project, record
}

func TestProjectPackageLifecycle(t *testing.T) {
	_, project, record := packageFixture(t)
	plan, err := project.PreviewPackages("add", []string{record.Name})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.AddLinks) != 1 || len(plan.After.Skills) != 1 {
		t.Fatalf("bad add plan: %+v", plan)
	}
	if err = project.ApplyPackages(plan); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project.Shared(), record.Name)
	if !owned(link, record.Library) {
		t.Fatal("project link does not target the central library")
	}
	report, err := project.Doctor()
	if err != nil || !report.Healthy {
		t.Fatalf("intentional package link failed diagnostics: %+v %v", report, err)
	}

	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	syncPlan, err := project.PreviewPackages("sync", nil)
	if err != nil || len(syncPlan.AddLinks) != 1 {
		t.Fatalf("sync did not repair a missing link: %+v %v", syncPlan, err)
	}
	if err = project.ApplyPackages(syncPlan); err != nil {
		t.Fatal(err)
	}

	remove, err := project.PreviewPackages("remove", []string{record.Name})
	if err != nil {
		t.Fatal(err)
	}
	if err = project.ApplyPackages(remove); err != nil {
		t.Fatal(err)
	}
	if !absent(link) || len(remove.After.Skills) != 0 || len(remove.After.Requests) != 0 {
		t.Fatalf("remove left package state: %+v", remove.After)
	}
}

func TestGroupsResolveNestedAndPreserveSharedSkills(t *testing.T) {
	global, project, sample := packageFixture(t)
	secondPath := filepath.Join(global.Config.Home, ".agents", "skills", "second")
	skill(t, secondPath)
	second := apply(t, global, "adopt", secondPath).Record
	apply(t, global, "disable", second.ID)
	if _, err := global.CreateGroup("base", []string{sample.Name}); err != nil {
		t.Fatal(err)
	}
	if _, err := global.CreateGroup("ios", []string{"@base", second.Name}); err != nil {
		t.Fatal(err)
	}
	plan, err := project.PreviewPackages("add", []string{"@ios", sample.Name})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.After.Skills) != 2 || len(plan.After.Requests) != 2 {
		t.Fatalf("group resolution was not deduplicated: %+v", plan.After)
	}
	if err = project.ApplyPackages(plan); err != nil {
		t.Fatal(err)
	}
	remove, err := project.PreviewPackages("remove", []string{"@ios"})
	if err != nil {
		t.Fatal(err)
	}
	if len(remove.After.Skills) != 1 || remove.After.Skills[0].Name != sample.Name {
		t.Fatalf("shared explicit dependency was removed: %+v", remove.After)
	}
}

func TestGroupCycleAndOccupiedProjectPath(t *testing.T) {
	global, project, record := packageFixture(t)
	if _, err := global.CreateGroup("one", []string{"@one"}); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatal("self-referencing group was accepted", err)
	}
	if _, err := global.CreateGroup("one", []string{"@missing"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatal("unknown nested group was accepted", err)
	}
	occupied := filepath.Join(project.Shared(), record.Name)
	if err := os.MkdirAll(occupied, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := project.PreviewPackages("add", []string{record.Name}); err == nil || !strings.Contains(err.Error(), "occupied") {
		t.Fatal("occupied project path was accepted", err)
	}
}

func TestProjectPackageRecovery(t *testing.T) {
	for _, stage := range []string{"journal", "links", "manifest"} {
		t.Run(stage, func(t *testing.T) {
			_, project, record := packageFixture(t)
			plan, err := project.PreviewPackages("add", []string{record.Name})
			if err != nil {
				t.Fatal(err)
			}
			if err = mkdir(project.Store); err != nil {
				t.Fatal(err)
			}
			if err = atomicJSON(filepath.Join(project.Store, "packages-journal.json"), plan); err != nil {
				t.Fatal(err)
			}
			if stage == "links" || stage == "manifest" {
				for _, link := range plan.AddLinks {
					if err = project.link(link.Path, link.Target); err != nil {
						t.Fatal(err)
					}
				}
			}
			if stage == "manifest" {
				if err = atomicJSON(project.packageManifestPath(), plan.After); err != nil {
					t.Fatal(err)
				}
			}
			if err = project.Recover(); err != nil {
				t.Fatal(err)
			}
			if project.hasPending() || !owned(filepath.Join(project.Shared(), record.Name), record.Library) {
				t.Fatal("package recovery did not finish the installation")
			}
			installed, err := project.loadPackages()
			if err != nil || !reflect.DeepEqual(installed, plan.After) {
				t.Fatalf("package manifest was not recovered: %+v %v", installed, err)
			}
		})
	}
}
