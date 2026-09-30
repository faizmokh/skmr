package manager

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faizmokh/skmr/internal/skills"
)

func requireOperation(t *testing.T, s *Service, r OperationRequest) OperationPlan {
	t.Helper()
	p, err := s.PreviewOperation(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Discard)
	return p
}

func TestDeletePreviewRechecksNewProjectDependency(t *testing.T) {
	global, path := fixture(t)
	record := apply(t, global, "adopt", path).Record
	disableBeforeDelete(t, global, record)
	deletion := requireOperation(t, global, OperationRequest{Action: "delete", Arguments: []string{record.ID}})
	project := projectService(t, global, "late-placement")
	placement := requireOperation(t, project, OperationRequest{Action: "add", Arguments: []string{record.Name}})
	if err := project.ApplyOperation(placement); err != nil {
		t.Fatal(err)
	}
	if err := global.ApplyOperation(deletion); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("delete ignored the placement created after preview: %v", err)
	}
	if absent(record.Library) || !owned(filepath.Join(project.Shared(), record.Name), record.Library) || global.hasPending() {
		t.Fatal("blocked delete modified content or left an operation journal")
	}
}

func TestSyncReportsDuplicateLibraryNamesBeforeLinkChanges(t *testing.T) {
	global, path := fixture(t)
	record := apply(t, global, "adopt", path).Record
	project := projectService(t, global, "name-conflict")
	placement := requireOperation(t, project, OperationRequest{Action: "add", Arguments: []string{record.Name}})
	if err := project.ApplyOperation(placement); err != nil {
		t.Fatal(err)
	}
	library, err := global.load()
	if err != nil {
		t.Fatal(err)
	}
	duplicate := record
	duplicate.ID = "duplicate"
	duplicate.Library = filepath.Join(global.Store, "library", duplicate.ID, duplicate.Name)
	library.Records = append(library.Records, duplicate)
	if err = global.save(library); err != nil {
		t.Fatal(err)
	}
	if _, err := project.PreviewOperation(OperationRequest{Action: "sync"}); err == nil || !strings.Contains(err.Error(), "library name conflict for "+record.Name) {
		t.Fatalf("duplicate name did not stop preview: %v", err)
	}
	if !owned(filepath.Join(project.Shared(), record.Name), record.Library) || global.hasPending() {
		t.Fatal("name conflict changed placement or wrote a journal")
	}
}

func TestOpeningLegacyDirectRequestsRegistersDeletionDependency(t *testing.T) {
	global, path := fixture(t)
	record := apply(t, global, "adopt", path).Record
	disableBeforeDelete(t, global, record)
	project := projectService(t, global, "old-placement")
	if err := os.MkdirAll(project.Store, 0755); err != nil {
		t.Fatal(err)
	}
	pins := PackageManifest{Version: packageManifestVersion, Requests: []string{record.Name}, Skills: []PackageRef{{Name: record.Name, ID: record.ID}}}
	if err := atomicJSON(project.packageManifestPath(), pins); err != nil {
		t.Fatal(err)
	}
	if err := project.Upgrade(); err != nil {
		t.Fatal(err)
	}
	if _, err := global.PreviewOperation(OperationRequest{Action: "delete", Arguments: []string{record.ID}}); err == nil || !strings.Contains(err.Error(), project.Config.Project) {
		t.Fatalf("opened project was not registered: %v", err)
	}
}

func assertSingleJournal(t *testing.T, global, project *Service) {
	t.Helper()
	if absent(global.operationPath()) || !global.hasPending() || !project.hasPending() {
		t.Fatal("operation does not protect both scopes")
	}
	for _, service := range []*Service{global, project} {
		for _, name := range []string{"journal.json", "delete-journal.json", "batch.json", "packages-journal.json", "remote-journal.json", "remote-add-journal.json", "upgrade-journal.json"} {
			if !absent(filepath.Join(service.Store, name)) {
				t.Fatalf("new operation wrote an old journal: %s", name)
			}
		}
	}
}

func commitOperationScopes(t *testing.T, s *Service, p OperationPlan, count int) {
	t.Helper()
	for _, scope := range p.Scopes[:count] {
		service, err := s.operationService(scope.Project)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(service.Store, 0755); err != nil {
			t.Fatal(err)
		}
		if scope.LibraryAfter != nil {
			if err := service.save(*scope.LibraryAfter); err != nil {
				t.Fatal(err)
			}
		}
		if scope.RequestsAfter != nil {
			if err := atomicJSON(service.packageManifestPath(), *scope.RequestsAfter); err != nil {
				t.Fatal(err)
			}
			if err := service.registerPackages(*scope.RequestsAfter); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestOneOperationRecoversLocalProjectAddAcrossBothCommits(t *testing.T) {
	for _, stage := range []string{"ready", "content", "links", "library-commit", "project-commit", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			global, _ := fixture(t)
			project := projectService(t, global, "project")
			path := filepath.Join(project.Shared(), "local")
			skill(t, path)
			p := requireOperation(t, project, OperationRequest{Action: "add", Arguments: []string{path}})
			if err := project.prepareOperation(&p); err != nil {
				t.Fatal(err)
			}
			assertSingleJournal(t, global, project)
			if stage != "ready" {
				for _, c := range p.Content {
					if err := executeContent(c); err != nil {
						t.Fatal(err)
					}
				}
			}
			if stage != "ready" && stage != "content" {
				for _, link := range p.AddLinks {
					if err := project.link(link.Path, link.Target); err != nil {
						t.Fatal(err)
					}
				}
			}
			if stage == "library-commit" || stage == "project-commit" || stage == "cleanup" {
				commitOperationScopes(t, project, p, 1)
			}
			if stage == "project-commit" || stage == "cleanup" {
				commitOperationScopes(t, project, p, len(p.Scopes))
			}
			if stage == "cleanup" {
				p.Cleanup = true
				if err := atomicJSON(global.operationPath(), p); err != nil {
					t.Fatal(err)
				}
			}
			if err := global.Recover(); err != nil {
				t.Fatal(err)
			}
			m, err := global.load()
			if err != nil || len(m.Records) != 1 || m.Records[0].Name != "local" || m.Records[0].Enabled || !owned(path, m.Records[0].Library) {
				t.Fatalf("incomplete central handoff: %+v %v", m, err)
			}
			legacy, err := project.load()
			if err != nil || len(legacy.Records) != 0 {
				t.Fatalf("project owns content: %+v %v", legacy, err)
			}
			if global.hasPending() || project.hasPending() || !absent(p.Stage) {
				t.Fatal("recovery left staging or a journal")
			}
		})
	}
}

func TestOperationPreparationRecoveryPreservesAuthoringSource(t *testing.T) {
	global, _ := fixture(t)
	path := filepath.Join(global.Config.Home, "authoring", "local")
	skill(t, path)
	before, err := skills.Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	p := requireOperation(t, global, OperationRequest{Action: "add", Arguments: []string{path}})
	if err := global.prepareOperation(&p); err != nil {
		t.Fatal(err)
	}
	p.Ready = false
	if err := os.Remove(filepath.Join(p.Content[0].Prepared, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(global.operationPath(), p); err != nil {
		t.Fatal(err)
	}
	if err := global.Recover(); err != nil {
		t.Fatal(err)
	}
	after, err := skills.Digest(path)
	if err != nil || before != after || !absent(p.Content[0].To) || !absent(p.Stage) || global.hasPending() {
		t.Fatalf("preparation touched author data: %v", err)
	}
	if err := global.ApplyOperation(requireOperation(t, global, OperationRequest{Action: "add", Arguments: []string{path}})); err != nil {
		t.Fatal(err)
	}
	if !owned(filepath.Join(global.Shared(), "local"), p.Content[0].To) {
		t.Fatal("retry did not install the owned copy")
	}
	after, err = skills.Digest(path)
	if err != nil || before != after {
		t.Fatal("local import changed its authoring source")
	}
}

func TestOperationRejectsStaleContentStateAndOccupiedLinks(t *testing.T) {
	for _, conflict := range []string{"content", "manifest", "link"} {
		t.Run(conflict, func(t *testing.T) {
			global, initial := fixture(t)
			path := filepath.Join(global.Config.Home, "authoring", "local")
			skill(t, path)
			p := requireOperation(t, global, OperationRequest{Action: "add", Arguments: []string{path}})
			link := filepath.Join(global.Shared(), "local")
			switch conflict {
			case "content":
				if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("edited"), 0644); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				apply(t, global, "adopt", initial)
			case "link":
				if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(link, []byte("unrelated"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := global.ApplyOperation(p); err == nil {
				t.Fatal("stale preview was accepted")
			}
			if global.hasPending() || !absent(p.Content[0].To) {
				t.Fatal("failed preflight changed the library")
			}
			if conflict == "link" {
				data, err := os.ReadFile(link)
				if err != nil || string(data) != "unrelated" {
					t.Fatal("occupied link was changed")
				}
			}
		})
	}
}

func TestOperationRecoveryStopsOnReplacedLinkAndResumes(t *testing.T) {
	global, _ := fixture(t)
	project := projectService(t, global, "project")
	path := filepath.Join(project.Shared(), "local")
	skill(t, path)
	p := requireOperation(t, project, OperationRequest{Action: "add", Arguments: []string{path}})
	if err := project.prepareOperation(&p); err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Content {
		if err := executeContent(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte("unrelated"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := project.Recover(); err == nil {
		t.Fatal("recovery replaced unrelated content")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "unrelated" || !project.hasPending() {
		t.Fatal("recovery did not preserve the conflict")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := project.Recover(); err != nil {
		t.Fatal(err)
	}
	if !owned(path, p.Content[0].To) {
		t.Fatal("recovery did not resume after repair")
	}
}

func TestOperationRecoveryRejectsInjectedCleanupPath(t *testing.T) {
	global, path := fixture(t)
	p := requireOperation(t, global, OperationRequest{Action: "adopt", Arguments: []string{path}})
	if err := global.prepareOperation(&p); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(global.Config.Home, "unrelated")
	skill(t, unrelated)
	p.Cleanup = true
	p.Content[0].Backup = unrelated
	p.Content[0].Identity, _ = identity(unrelated)
	if err := atomicJSON(global.operationPath(), p); err != nil {
		t.Fatal(err)
	}
	if err := global.Recover(); err == nil {
		t.Fatal("invalid cleanup path was accepted")
	}
	if _, err := os.Stat(filepath.Join(unrelated, "SKILL.md")); err != nil {
		t.Fatal("unrelated content was deleted")
	}
}

func TestOperationRelocationRecoversWithOriginalBackedUp(t *testing.T) {
	global, path := fixture(t)
	p := requireOperation(t, global, OperationRequest{Action: "adopt", Arguments: []string{path}})
	p.Content[0].Kind, p.Content[0].OldDigest = "relocate", p.Content[0].Digest
	if err := global.prepareOperation(&p); err != nil {
		t.Fatal(err)
	}
	c := p.Content[0]
	if err := move(c.From, c.Backup, c.Identity); err != nil {
		t.Fatal(err)
	}
	if err := global.Recover(); err != nil {
		t.Fatal(err)
	}
	if !owned(filepath.Join(global.Shared(), "sample"), c.To) || !absent(c.Backup) || !absent(c.Prepared) || global.hasPending() {
		t.Fatal("relocation recovery did not complete")
	}
}

func TestOperationRemoteAddAndUpdateRecoverWithoutDownloadStage(t *testing.T) {
	fakeSkillsCLI(t)
	for _, boundary := range []string{"ready", "backup", "content", "manifest"} {
		t.Run(boundary, func(t *testing.T) {
			global := remoteService(t)
			project := remoteProject(t, global)
			p := requireOperation(t, project, OperationRequest{Action: "add", Arguments: []string{"https://github.com/example/skills"}, Skills: []string{"alpha", "beta"}})
			if err := project.prepareOperation(&p); err != nil {
				t.Fatal(err)
			}
			p.Discard()
			assertSingleJournal(t, global, project)
			if err := project.Recover(); err != nil {
				t.Fatal(err)
			}
			m, err := global.load()
			if err != nil || len(m.Records) != 2 || m.Records[0].Enabled {
				t.Fatalf("remote project add: %+v %v", m, err)
			}
			for _, record := range m.Records {
				if !owned(filepath.Join(project.Shared(), record.Name), record.Library) {
					t.Fatal("remote placement missing")
				}
			}
			t.Setenv("SKMR_TEST_VERSION", "2")
			update := requireOperation(t, global, OperationRequest{Action: "update", Arguments: []string{"alpha"}})
			if err := global.prepareOperation(&update); err != nil {
				t.Fatal(err)
			}
			update.Discard()
			c := update.Content[0]
			if boundary != "ready" {
				if err := move(c.To, c.Backup, c.Identity); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "content" || boundary == "manifest" {
				if err := move(c.Prepared, c.To, c.PreparedID); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "manifest" {
				commitOperationScopes(t, global, update, len(update.Scopes))
			}
			if err := project.Recover(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(c.To, "SKILL.md"))
			if err != nil || !strings.Contains(string(data), "version 2") || !absent(c.Backup) || global.hasPending() {
				t.Fatalf("update recovery: %s %v", data, err)
			}
		})
	}
}

func TestOperationDeleteRecoversInterruptedCleanup(t *testing.T) {
	global, path := fixture(t)
	record := apply(t, global, "adopt", path).Record
	apply(t, global, "disable", record.ID)
	p := requireOperation(t, global, OperationRequest{Action: "delete", Arguments: []string{record.ID}})
	if err := global.prepareOperation(&p); err != nil {
		t.Fatal(err)
	}
	if err := executeContent(p.Content[0]); err != nil {
		t.Fatal(err)
	}
	commitOperationScopes(t, global, p, len(p.Scopes))
	p.Cleanup = true
	if err := atomicJSON(global.operationPath(), p); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(p.Content[0].Backup, record.Name, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := global.Recover(); err != nil {
		t.Fatal(err)
	}
	if !absent(p.Content[0].Backup) || global.hasPending() {
		t.Fatal("delete cleanup is incomplete")
	}
}

func TestPersonalUpgradePreflightsNestedPresetsBeforeRewritingLegacyLibrary(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "conflict"}[conflict], func(t *testing.T) {
			global, path := fixture(t)
			apply(t, global, "adopt", path)
			legacy, err := global.load()
			if err != nil {
				t.Fatal(err)
			}
			legacy.Version = legacyVersion
			legacy.Records[0].Origins = nil
			manifestPath := filepath.Join(global.Store, "manifest.json")
			if err := atomicJSON(manifestPath, legacy); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if conflict {
				cyclic := groupManifest{Version: packageManifestVersion, Groups: []InstallGroup{{Name: "cycle", Members: []string{"@cycle"}}}}
				if err := atomicJSON(filepath.Join(global.Store, "groups.json"), cyclic); err != nil {
					t.Fatal(err)
				}
			}
			err = global.Upgrade()
			after, readErr := os.ReadFile(manifestPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if conflict {
				if err == nil || !bytes.Equal(before, after) || global.hasPending() {
					t.Fatal("upgrade conflict changed old state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var current Manifest
			if err := json.Unmarshal(after, &current); err != nil || current.Version != Version || len(current.Records[0].Origins) != 1 {
				t.Fatal("legacy library was not committed in current format", err)
			}
		})
	}
}

func TestProjectUpgradeAndReturnPreserveResolvedDuplicateCopies(t *testing.T) {
	global, _ := fixture(t)
	project := projectService(t, global, "duplicates-project")
	first := filepath.Join(project.Shared(), "local")
	second := filepath.Join(project.Config.Project, ".pi", "skills", "local")
	skill(t, first)
	skill(t, second)
	if err := os.WriteFile(filepath.Join(second, "scripts", "run.sh"), []byte("second copy"), 0751); err != nil {
		t.Fatal(err)
	}
	firstDigest, _ := skills.Digest(first)
	secondDigest, _ := skills.Digest(second)
	apply(t, project, "resolve", skills.ID(first))
	if err := project.Upgrade(); err != nil {
		t.Fatal(err)
	}
	m, err := global.load()
	if err != nil || len(m.Records) != 1 || len(m.Records[0].MigratedFrom.Origins) != 2 {
		t.Fatalf("duplicate origins were not migrated: %+v %v", m, err)
	}
	remove := requireOperation(t, project, OperationRequest{Action: "remove", Arguments: []string{"local"}})
	if err := project.ApplyOperation(remove); err != nil {
		t.Fatal(err)
	}
	restore := requireOperation(t, global, OperationRequest{Action: "restore", Arguments: []string{m.Records[0].ID}})
	if err := global.ApplyOperation(restore); err != nil {
		t.Fatal(err)
	}
	gotFirst, err := skills.Digest(first)
	if err != nil || gotFirst != firstDigest {
		t.Fatal("canonical copy was not returned intact", err)
	}
	gotSecond, err := skills.Digest(second)
	if err != nil || gotSecond != secondDigest {
		t.Fatal("duplicate copy was not returned intact", err)
	}
}
