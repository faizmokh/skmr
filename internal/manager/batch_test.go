package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchAdoptAndRecovery(t *testing.T) {
	for _, recoverAfterFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "apply", true: "recover"}[recoverAfterFirst], func(t *testing.T) {
			s, first := fixture(t)
			second := filepath.Join(s.Config.Home, ".pi", "agent", "skills", "second")
			skill(t, second)
			plan, err := s.PreviewBatchAdopt([]string{first, second})
			if err != nil || len(plan.Plans) != 2 || !strings.Contains(plan.String(), "Add 2 skills") {
				t.Fatalf("bad batch preview: %+v %v", plan, err)
			}
			if recoverAfterFirst {
				if err = mkdir(s.Store); err != nil {
					t.Fatal(err)
				}
				if err = atomicJSON(filepath.Join(s.Store, "batch.json"), plan); err != nil {
					t.Fatal(err)
				}
				if err = s.execute(plan.Plans[0]); err != nil {
					t.Fatal(err)
				}
				if err = s.Recover(); err != nil {
					t.Fatal(err)
				}
			} else if err = s.ApplyBatch(plan); err != nil {
				t.Fatal(err)
			}
			manifest, err := s.load()
			if err != nil || len(manifest.Records) != 2 {
				t.Fatalf("batch manifest=%+v err=%v", manifest, err)
			}
			for _, record := range manifest.Records {
				if !owned(filepath.Join(s.Shared(), record.Name), record.Library) {
					t.Fatalf("missing shared link for %s", record.Name)
				}
			}
			if !absent(filepath.Join(s.Store, "batch.json")) {
				t.Fatal("batch journal remained after completion")
			}
		})
	}
}

func TestBatchRejectsStaleStateBeforeMutation(t *testing.T) {
	s, first := fixture(t)
	second := filepath.Join(s.Config.Home, ".pi", "agent", "skills", "second")
	skill(t, second)
	plan, err := s.PreviewBatchAdopt([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(first, "changed.txt"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyBatch(plan); err == nil || !strings.Contains(err.Error(), "state changed") {
		t.Fatal("stale batch was accepted", err)
	}
	if absent(first) || absent(second) || !absent(filepath.Join(s.Store, "batch.json")) {
		t.Fatal("stale batch mutated skill state")
	}
}

func TestBatchAdoptKeepsOneDuplicateAndTracksTheOther(t *testing.T) {
	s, unique := fixture(t)
	kept := filepath.Join(s.Config.Home, ".codex", "skills", "duplicate")
	other := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "duplicate")
	skill(t, kept)
	skill(t, other)
	if err := os.WriteFile(filepath.Join(other, "variant.txt"), []byte("other copy"), 0644); err != nil {
		t.Fatal(err)
	}

	plan, err := s.PreviewBatchAdopt([]string{unique, kept})
	if err != nil || len(plan.Plans) != 2 || plan.Plans[1].Action != "resolve" {
		t.Fatalf("bad duplicate batch plan: %+v %v", plan, err)
	}
	if err = s.ApplyBatch(plan); err != nil {
		t.Fatal(err)
	}

	manifest, err := s.load()
	if err != nil || len(manifest.Records) != 2 {
		t.Fatalf("batch manifest=%+v err=%v", manifest, err)
	}
	var duplicate Record
	for _, record := range manifest.Records {
		if record.Name == "duplicate" {
			duplicate = record
		}
	}
	if len(duplicate.Origins) != 2 || !owned(filepath.Join(s.Shared(), "duplicate"), duplicate.Library) {
		t.Fatalf("duplicate origins/link not preserved: %+v", duplicate)
	}
	for _, origin := range duplicate.Origins {
		if origin.Canonical {
			if origin.Path != kept {
				t.Fatalf("wrong kept copy: %+v", origin)
			}
		} else if origin.Path != other || absent(origin.Backup) {
			t.Fatalf("duplicate backup not tracked: %+v", origin)
		}
	}
}

func TestAdoptionCandidatesExcludeManagedSourcesAndDefaultOnlySafe(t *testing.T) {
	s, safe := fixture(t)
	system := filepath.Join(s.Config.Home, ".codex", "skills", ".system", "builtin")
	plugin := filepath.Join(s.Config.Home, ".codex", "plugins", "cache", "market", "plugin", "1", "plugin-skill")
	invalid := filepath.Join(s.Config.Home, ".pi", "agent", "skills", "invalid")
	duplicateShared := filepath.Join(s.Config.Home, ".agents", "skills", "duplicate")
	duplicateLegacy := filepath.Join(s.Config.Home, ".codex", "skills", "duplicate")
	for _, path := range []string{system, plugin, duplicateShared, duplicateLegacy} {
		skill(t, path)
	}
	if err := os.MkdirAll(invalid, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalid, "SKILL.md"), []byte("bad"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	candidates := AdoptionCandidates(result)
	paths := map[string]AdoptionCandidate{}
	for _, candidate := range candidates {
		paths[candidate.Skill.Path] = candidate
	}
	if _, ok := paths[system]; ok {
		t.Fatal("system skill became a candidate")
	}
	if _, ok := paths[plugin]; ok {
		t.Fatal("plugin skill became a candidate")
	}
	if paths[invalid].Selectable || paths[invalid].Reason == "" {
		t.Fatal("invalid skill was not disabled with a reason")
	}
	if paths[duplicateShared].Eligible || !paths[duplicateShared].Selectable || paths[duplicateLegacy].Eligible {
		t.Fatal("duplicate choices were not classified correctly")
	}
	safePaths := SafeAdoptionPaths(result)
	if len(safePaths) != 1 || safePaths[0] != safe {
		t.Fatalf("safe paths=%v", safePaths)
	}
}

func TestSetupOfferAndPersistence(t *testing.T) {
	s, _ := fixture(t)
	result, err := s.List()
	if err != nil || !s.ShouldOfferSetup(result) {
		t.Fatal("first global setup was not offered", err)
	}
	if err = s.MarkSetup("skipped"); err != nil {
		t.Fatal(err)
	}
	if s.ShouldOfferSetup(result) {
		t.Fatal("dismissed setup was offered again")
	}
	project := projectService(t, s, "project")
	projectResult, err := project.List()
	if err != nil {
		t.Fatal(err)
	}
	if project.ShouldOfferSetup(projectResult) {
		t.Fatal("project scope offered global first-run setup")
	}

	existing, _ := fixture(t)
	if err = mkdir(existing.Store); err != nil {
		t.Fatal(err)
	}
	if err = existing.save(Manifest{Version: Version}); err != nil {
		t.Fatal(err)
	}
	existingResult, err := existing.List()
	if err != nil {
		t.Fatal(err)
	}
	if existing.ShouldOfferSetup(existingResult) {
		t.Fatal("existing installation received first-run setup")
	}
}

func TestSetupOfferShowsInvalidWritableSkills(t *testing.T) {
	s, valid := fixture(t)
	if err := os.RemoveAll(valid); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(s.Config.Home, ".codex", "skills", "invalid")
	if err := os.MkdirAll(invalid, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalid, "SKILL.md"), []byte("invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := s.List()
	if err != nil || !s.ShouldOfferSetup(result) {
		t.Fatal("invalid writable skill did not trigger setup", err)
	}
}
