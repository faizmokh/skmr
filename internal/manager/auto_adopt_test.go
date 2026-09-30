package manager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoAdoptOwnsWritableSkillsAndSkipsDuplicateNames(t *testing.T) {
	global, source := fixture(t)
	duplicate := filepath.Join(global.Config.Home, ".agents", "skills", "sample")
	skill(t, duplicate)
	unique := filepath.Join(global.Config.Home, ".agents", "skills", "unique")
	skill(t, unique)
	if err := global.AutoAdopt(); err != nil {
		t.Fatal(err)
	}
	manifest, err := global.load()
	if err != nil || len(manifest.Records) != 1 || manifest.Records[0].Name != "unique" {
		t.Fatalf("automatic adoption changed ambiguous copies: %+v %v", manifest, err)
	}
	if !owned(unique, manifest.Records[0].Library) {
		t.Fatal("unique skill was not linked to the library")
	}
	for _, path := range []string{source, duplicate} {
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
			t.Fatalf("duplicate copy was changed: %s: %v", path, err)
		}
	}
	conflicts, err := global.AutoAdoptConflicts()
	if err != nil || conflicts != "sample" {
		t.Fatalf("duplicate conflict not reported: %q %v", conflicts, err)
	}
}

func TestAutoAdoptProjectUsesCentralLibrary(t *testing.T) {
	global, _ := fixture(t)
	project := projectService(t, global, "auto-project")
	path := filepath.Join(project.Config.Project, ".agents", "skills", "local")
	skill(t, path)
	if err := project.AutoAdopt(); err != nil {
		t.Fatal(err)
	}
	globalManifest, err := global.load()
	if err != nil || len(globalManifest.Records) != 2 {
		t.Fatalf("personal library missing adopted skills: %+v %v", globalManifest, err)
	}
	projectManifest, err := project.load()
	if err != nil || len(projectManifest.Records) != 0 {
		t.Fatalf("project still owns content: %+v %v", projectManifest, err)
	}
	for _, record := range globalManifest.Records {
		if record.Name == "local" && !owned(path, record.Library) {
			t.Fatal("project skill does not point to central content")
		}
	}
}

func TestReturnedOriginsStayUnmanagedUntilExplicitlyAdded(t *testing.T) {
	for _, projectScope := range []bool{false, true} {
		t.Run(map[bool]string{false: "global", true: "project"}[projectScope], func(t *testing.T) {
			global, source := fixture(t)
			s := global
			if projectScope {
				s = projectService(t, global, "returned-project")
				source = filepath.Join(s.Shared(), "local")
				skill(t, source)
			}
			addition := requireOperation(t, s, OperationRequest{Action: "add", Arguments: []string{source}})
			if err := s.ApplyOperation(addition); err != nil {
				t.Fatal(err)
			}
			name := filepath.Base(source)
			if projectScope {
				removal := requireOperation(t, s, OperationRequest{Action: "remove", Arguments: []string{name}})
				if err := s.ApplyOperation(removal); err != nil {
					t.Fatal(err)
				}
			}
			returning := requireOperation(t, global, OperationRequest{Action: "restore", Arguments: []string{name}})
			if err := global.prepareOperation(&returning); err != nil {
				t.Fatal(err)
			}
			if err := s.Recover(); err != nil {
				t.Fatal(err)
			}
			// Refreshing the scope must honor the explicit return, including recovery.
			if err := s.AutoAdopt(); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(source)
			if err != nil || !info.IsDir() {
				t.Fatal("returned source was automatically reclaimed", err)
			}
			library, err := global.load()
			if err != nil || !contains(library.Returned, source) {
				t.Fatal("explicit return was not recorded", err)
			}
			addition = requireOperation(t, s, OperationRequest{Action: "add", Arguments: []string{source}})
			if err := s.ApplyOperation(addition); err != nil {
				t.Fatal(err)
			}
			library, err = global.load()
			if err != nil || contains(library.Returned, source) {
				t.Fatal("explicit add did not clear returned origin", err)
			}
		})
	}
}
