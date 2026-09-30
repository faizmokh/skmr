package manager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeMovesProjectSkillIntoPersonalLibrary(t *testing.T) {
	global, _ := fixture(t)
	project := projectService(t, global, "legacy-project")
	path := filepath.Join(project.Config.Project, ".agents", "skills", "legacy")
	skill(t, path)
	record := apply(t, project, "adopt", path).Record
	if err := project.Upgrade(); err != nil {
		t.Fatal(err)
	}
	projectManifest, err := project.load()
	if err != nil || len(projectManifest.Records) != 0 {
		t.Fatalf("project still owns skill: %+v %v", projectManifest, err)
	}
	globalManifest, err := global.load()
	if err != nil || len(globalManifest.Records) != 1 || globalManifest.Records[0].Enabled {
		t.Fatalf("personal library does not own disabled skill: %+v %v", globalManifest, err)
	}
	if globalManifest.Records[0].ID != record.ID || !owned(path, globalManifest.Records[0].Library) {
		t.Fatal("project placement does not target central content")
	}
	packages, err := project.loadPackages()
	if err != nil || len(packages.Requests) != 1 || packages.Requests[0] != "legacy" {
		t.Fatalf("project request missing: %+v %v", packages, err)
	}
	if !absent(project.upgradePath()) || !absent(filepath.Dir(record.Library)) {
		t.Fatal("upgrade cleanup is incomplete")
	}
	if _, err := global.Preview("restore", record.ID); err == nil {
		t.Fatal("restore bypassed the active project placement")
	}
	remove, err := project.PreviewPackages("remove", []string{"legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if err = project.ApplyPackages(remove); err != nil {
		t.Fatal(err)
	}
	restore, err := global.Preview("restore", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = global.Apply(restore); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
		t.Fatal("restored skill did not return to its project origin", err)
	}
}

func TestUpgradeRecoversAfterCentralCopy(t *testing.T) {
	global, _ := fixture(t)
	project := projectService(t, global, "legacy-project")
	path := filepath.Join(project.Config.Project, ".agents", "skills", "legacy")
	skill(t, path)
	apply(t, project, "adopt", path)
	plan, err := project.prepareProjectUpgrade(global)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(project.Store, 0755); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(project.upgradePath(), plan); err != nil {
		t.Fatal(err)
	}
	entry := plan.Entries[0]
	if err = copyPackage(filepath.Dir(entry.Source.Library), filepath.Dir(entry.Destination.Library), entry.Digest); err != nil {
		t.Fatal(err)
	}
	if err = project.Recover(); err != nil {
		t.Fatal(err)
	}
	if !owned(path, entry.Destination.Library) || !absent(project.upgradePath()) {
		t.Fatal("upgrade recovery did not finish")
	}
}

func TestUpgradePreservesDisabledProjectSkill(t *testing.T) {
	global, _ := fixture(t)
	project := projectService(t, global, "disabled-project")
	path := filepath.Join(project.Config.Project, ".agents", "skills", "disabled")
	skill(t, path)
	record := apply(t, project, "adopt", path).Record
	apply(t, project, "disable", record.ID)
	if err := project.Upgrade(); err != nil {
		t.Fatal(err)
	}
	packages, err := project.loadPackages()
	if err != nil || len(packages.Requests) != 0 || len(packages.Skills) != 0 {
		t.Fatalf("disabled skill was placed in project: %+v %v", packages, err)
	}
	if !absent(path) {
		t.Fatal("disabled project link remains")
	}
	library, err := global.load()
	if err != nil || len(library.Records) != 1 || library.Records[0].Enabled {
		t.Fatalf("disabled skill was not stored centrally: %+v %v", library, err)
	}
}

func TestUpgradeSnapshotsLegacyGroupWithoutItsDefinition(t *testing.T) {
	global, source := fixture(t)
	record := apply(t, global, "adopt", source).Record
	project := projectService(t, global, "legacy-group-project")
	if err := os.MkdirAll(project.Store, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := PackageManifest{Version: packageManifestVersion, Requests: []string{"@deleted"}, Skills: []PackageRef{{ID: record.ID, Name: record.Name}}}
	if err := atomicJSON(project.packageManifestPath(), legacy); err != nil {
		t.Fatal(err)
	}
	if err := project.Upgrade(); err != nil {
		t.Fatal(err)
	}
	after, err := project.loadPackages()
	if err != nil || len(after.Requests) != 1 || after.Requests[0] != record.Name {
		t.Fatalf("group preset was not snapshotted: %+v %v", after, err)
	}
}

func TestUpgradeFlattensNestedGroupDefinitions(t *testing.T) {
	global, source := fixture(t)
	record := apply(t, global, "adopt", source).Record
	legacy := groupManifest{Version: packageManifestVersion, Groups: []InstallGroup{
		{Name: "base", Members: []string{record.Name}},
		{Name: "nested", Members: []string{"@base", record.Name}},
	}}
	if err := atomicJSON(filepath.Join(global.Store, "groups.json"), legacy); err != nil {
		t.Fatal(err)
	}
	if err := global.Upgrade(); err != nil {
		t.Fatal(err)
	}
	groups, err := global.Groups()
	if err != nil || len(groups) != 2 || len(groups[1].Members) != 1 || groups[1].Members[0] != record.Name {
		t.Fatalf("nested preset was not flattened: %+v %v", groups, err)
	}
}

func TestUpgradeStopsBeforeChangingOccupiedProjectLink(t *testing.T) {
	global, _ := fixture(t)
	project := projectService(t, global, "occupied-project")
	path := filepath.Join(project.Config.Project, ".agents", "skills", "local")
	skill(t, path)
	record := apply(t, project, "adopt", path).Record
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := project.Upgrade(); err == nil {
		t.Fatal("occupied link was overwritten")
	}
	if absent(record.Library) || !absent(project.upgradePath()) {
		t.Fatal("failed preflight changed project content")
	}
}

func TestUpgradeRecoveryStopsAfterUnrelatedLibraryChange(t *testing.T) {
	global, globalPath := fixture(t)
	project := projectService(t, global, "stale-project")
	path := filepath.Join(project.Config.Project, ".agents", "skills", "local")
	skill(t, path)
	apply(t, project, "adopt", path)
	plan, err := project.prepareProjectUpgrade(global)
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(project.upgradePath(), plan); err != nil {
		t.Fatal(err)
	}
	apply(t, global, "adopt", globalPath)
	if err = project.Recover(); err == nil {
		t.Fatal("recovery accepted unrelated library change")
	}
	if absent(project.upgradePath()) || absent(filepath.Dir(plan.Entries[0].Source.Library)) {
		t.Fatal("stale recovery destroyed project state")
	}
}
