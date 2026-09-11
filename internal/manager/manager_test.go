package manager

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/faizmokh/skmr/internal/skills"
)

func fixture(t *testing.T) (*Service, string) {
	t.Helper()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(Config{Home: home})
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(home, ".codex", "skills", "sample")
	skill(t, p)
	return s, p
}
func skill(t *testing.T, p string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Join(p, "scripts"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("---\nname: "+filepath.Base(p)+"\ndescription: A useful skill\n---\n\nHello.\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(p, "scripts", "run.sh"), []byte("#!/bin/sh\nprintf hello\n"), 0751); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("scripts/run.sh", filepath.Join(p, "run")); e != nil {
		t.Fatal(e)
	}
}
func apply(t *testing.T, s *Service, action, arg string) Plan {
	t.Helper()
	p, e := s.Preview(action, arg)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Apply(p); e != nil {
		t.Fatal(e)
	}
	return p
}

func projectService(t *testing.T, base *Service, name string) *Service {
	t.Helper()
	project := filepath.Join(base.Config.Home, name)
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	service, err := New(Config{Home: base.Config.Home, DataHome: base.Config.DataHome, ConfigHome: base.Config.ConfigHome, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestMoveGlobalToProjectAndStopManaging(t *testing.T) {
	global, source := fixture(t)
	record := apply(t, global, "adopt", source).Record
	project := projectService(t, global, "app")
	plan, err := global.PreviewTransfer("move", record.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "Remove discovery link") || plan.DestinationRecord.ID != record.ID {
		t.Fatalf("bad move preview: %+v", plan)
	}
	if err = global.ApplyTransfer(project, plan); err != nil {
		t.Fatal(err)
	}
	globalManifest, _ := global.load()
	projectManifest, _ := project.load()
	if len(globalManifest.Records) != 0 || len(projectManifest.Records) != 1 {
		t.Fatalf("ownership did not move: %+v %+v", globalManifest, projectManifest)
	}
	moved := projectManifest.Records[0]
	if !owned(moved.Links[0], moved.Library) || !absent(record.Library) {
		t.Fatal("move did not relocate content and discovery link")
	}
	apply(t, project, "restore", moved.ID)
	if info, statErr := os.Lstat(filepath.Join(project.Config.Project, ".agents", "skills", moved.Name)); statErr != nil || !info.IsDir() {
		t.Fatal("stop managing did not restore into destination scope", statErr)
	}
}

func TestCopyBetweenProjectsIsIndependentAndPreservesDisabledState(t *testing.T) {
	global, _ := fixture(t)
	source := projectService(t, global, "source-project")
	destination := projectService(t, global, "destination-project")
	path := filepath.Join(source.Config.Project, ".agents", "skills", "project-sample")
	skill(t, path)
	record := apply(t, source, "adopt", path).Record
	apply(t, source, "disable", record.ID)
	plan, err := source.PreviewTransfer("copy", record.ID, destination)
	if err != nil {
		t.Fatal(err)
	}
	if plan.DestinationRecord.ID == record.ID || plan.DestinationRecord.Enabled {
		t.Fatalf("copy identity/state incorrect: %+v", plan.DestinationRecord)
	}
	if err = source.ApplyTransfer(destination, plan); err != nil {
		t.Fatal(err)
	}
	if !absent(plan.DestinationRecord.Links[0]) || absent(record.Library) {
		t.Fatal("disabled copy changed discovery or source content")
	}
	copiedScript, err := os.Stat(filepath.Join(plan.DestinationRecord.Library, "scripts", "run.sh"))
	if err != nil || copiedScript.Mode().Perm() != 0751 {
		t.Fatal("copy did not preserve executable permissions", err)
	}
	if target, linkErr := os.Readlink(filepath.Join(plan.DestinationRecord.Library, "run")); linkErr != nil || target != "scripts/run.sh" {
		t.Fatal("copy did not preserve the internal symlink", linkErr)
	}
	if err = os.WriteFile(filepath.Join(plan.DestinationRecord.Library, "new.txt"), []byte("copy only"), 0644); err != nil {
		t.Fatal(err)
	}
	if !absent(filepath.Join(record.Library, "new.txt")) {
		t.Fatal("copied packages are not independent")
	}
}

func TestMoveProjectToGlobalAndBetweenProjects(t *testing.T) {
	for _, destinationKind := range []string{"global", "project"} {
		t.Run(destinationKind, func(t *testing.T) {
			global, _ := fixture(t)
			source := projectService(t, global, "move-source")
			destination := global
			if destinationKind == "project" {
				destination = projectService(t, global, "move-destination")
			}
			path := filepath.Join(source.Config.Project, ".agents", "skills", "moving-skill")
			skill(t, path)
			record := apply(t, source, "adopt", path).Record
			plan, err := source.PreviewTransfer("move", record.ID, destination)
			if err != nil {
				t.Fatal(err)
			}
			if err = source.ApplyTransfer(destination, plan); err != nil {
				t.Fatal(err)
			}
			manifest, err := destination.load()
			if err != nil || len(manifest.Records) != 1 || manifest.Records[0].ID != record.ID {
				t.Fatalf("move did not preserve ownership and ID: %+v %v", manifest, err)
			}
		})
	}
}

func TestTransferValidation(t *testing.T) {
	global, sourcePath := fixture(t)
	record := apply(t, global, "adopt", sourcePath).Record
	project := projectService(t, global, "validation-project")
	if _, err := global.PreviewTransfer("copy", record.ID, project); err == nil || !strings.Contains(err.Error(), "already contains") {
		t.Fatal("copy over inherited skill was accepted", err)
	}
	if _, err := global.PreviewTransfer("move", record.ID, global); err == nil || !strings.Contains(err.Error(), "same") {
		t.Fatal("same-scope transfer was accepted", err)
	}
	overlapPath := filepath.Join(record.Library, "nested-project")
	if err := os.MkdirAll(overlapPath, 0755); err != nil {
		t.Fatal(err)
	}
	overlap, err := New(Config{Home: global.Config.Home, DataHome: global.Config.DataHome, ConfigHome: global.Config.ConfigHome, Project: overlapPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = global.PreviewTransfer("move", record.ID, overlap); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatal("overlapping destination was accepted", err)
	}
	manifest, err := global.load()
	if err != nil {
		t.Fatal(err)
	}
	manifest.Records[0].Origins = append(manifest.Records[0].Origins, Origin{Path: filepath.Join(global.Config.Home, ".pi", "agent", "skills", "backup"), Backup: filepath.Join(global.Store, "library", record.ID, ".skmr-duplicates", "backup")})
	if err = global.save(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = global.PreviewTransfer("move", record.ID, project); err == nil || !strings.Contains(err.Error(), "duplicate backups") {
		t.Fatal("record with preserved duplicates was accepted", err)
	}
}

func TestTransferRecoveryFromDestination(t *testing.T) {
	for _, action := range []string{"move", "copy"} {
		for _, stage := range []string{"journal", "content", "destination-manifest", "both-manifests"} {
			t.Run(action+"/"+stage, func(t *testing.T) {
				global, _ := fixture(t)
				source := projectService(t, global, "recover-source")
				destination := projectService(t, global, "recover-destination")
				path := filepath.Join(source.Config.Project, ".agents", "skills", "recover-skill")
				skill(t, path)
				record := apply(t, source, "adopt", path).Record
				plan, err := source.PreviewTransfer(action, record.ID, destination)
				if err != nil {
					t.Fatal(err)
				}
				if err = mkdir(source.Store); err != nil {
					t.Fatal(err)
				}
				if err = mkdir(destination.Store); err != nil {
					t.Fatal(err)
				}
				if err = atomicJSON(filepath.Join(source.Store, "transfer.json"), plan); err != nil {
					t.Fatal(err)
				}
				if err = atomicJSON(filepath.Join(destination.Store, "transfer.json"), plan); err != nil {
					t.Fatal(err)
				}
				if stage != "journal" {
					if err = executeTransfer(source, destination, plan); err != nil {
						t.Fatal(err)
					}
				}
				destinationAfter := plan.DestinationBefore
				destinationAfter.Records = append(destinationAfter.Records, plan.DestinationRecord)
				if stage == "destination-manifest" || stage == "both-manifests" {
					if err = destination.save(destinationAfter); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "both-manifests" && action == "move" {
					sourceAfter := plan.SourceBefore
					sourceAfter.Records = []Record{}
					if err = source.save(sourceAfter); err != nil {
						t.Fatal(err)
					}
				}
				if err = destination.Recover(); err != nil {
					t.Fatal(err)
				}
				if source.hasPending() || destination.hasPending() {
					t.Fatal("recovery did not clear both journals")
				}
				manifest, err := destination.load()
				if err != nil || len(manifest.Records) != 1 {
					t.Fatalf("destination manifest not recovered: %+v %v", manifest, err)
				}
			})
		}
	}
}
func TestLifecycle(t *testing.T) {
	for _, scope := range []string{"global", "project"} {
		t.Run(scope, func(t *testing.T) {
			s, p := fixture(t)
			if scope == "project" {
				project := filepath.Join(s.Config.Home, "repo")
				if e := os.MkdirAll(project, 0755); e != nil {
					t.Fatal(e)
				}
				var e error
				s, e = New(Config{Home: s.Config.Home, Project: project})
				if e != nil {
					t.Fatal(e)
				}
				p = filepath.Join(project, ".pi", "skills", "project-sample")
				skill(t, p)
			}
			plan, e := s.Preview("adopt", p)
			if e != nil {
				t.Fatal(e)
			}
			if !absent(s.Store) {
				t.Fatal("preview created state")
			}
			if e = s.Apply(plan); e != nil {
				t.Fatal(e)
			}
			r := plan.Record
			for _, link := range r.Links {
				if !owned(link, r.Library) {
					t.Fatal("missing link", link)
				}
				target, _ := os.Readlink(link)
				if scope == "project" && filepath.IsAbs(target) {
					t.Fatal("project links must be relative")
				}
			}
			st, e := os.Stat(filepath.Join(r.Library, "scripts", "run.sh"))
			if e != nil || st.Mode().Perm() != 0751 {
				t.Fatal("executable mode lost", e)
			}
			data, e := os.ReadFile(filepath.Join(r.Library, "run"))
			if e != nil || !strings.Contains(string(data), "printf hello") {
				t.Fatal("internal link broken", e)
			}
			list, e := s.List()
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, item := range list.Skills {
				if item.ID == r.ID {
					found = true
					if !item.Managed || !item.Enabled || len(item.Agents) != 3 {
						t.Fatalf("bad managed skill: %+v", item)
					}
				}
			}
			if !found {
				t.Fatal("missing managed skill")
			}
			apply(t, s, "disable", r.ID)
			for _, link := range r.Links {
				if !absent(link) {
					t.Fatal("link not removed")
				}
			}
			if absent(r.Library) {
				t.Fatal("disabled content removed")
			}
			apply(t, s, "enable", r.ID)
			apply(t, s, "restore", r.ID)
			if st, e = os.Lstat(p); e != nil || !st.IsDir() {
				t.Fatal("original not restored", e)
			}
			if !absent(r.Library) {
				t.Fatal("library not moved")
			}
			m, e := s.load()
			if e != nil || len(m.Records) != 0 {
				t.Fatal("record not removed", e)
			}
		})
	}
}
func TestAdoptSharedAndRestoreDisabled(t *testing.T) {
	s, _ := fixture(t)
	p := filepath.Join(s.Shared(), "shared")
	skill(t, p)
	r := apply(t, s, "adopt", p).Record
	if len(r.Links) != 1 {
		t.Fatal("duplicate shared link")
	}
	apply(t, s, "disable", r.ID)
	apply(t, s, "disable", r.ID)
	apply(t, s, "restore", r.ID)
	if st, e := os.Stat(p); e != nil || !st.IsDir() {
		t.Fatal("restore disabled failed", e)
	}
}
func TestAdoptKeepsSelectedCopyWhenSharedPathConflicts(t *testing.T) {
	s, p := fixture(t)
	shared := filepath.Join(s.Shared(), "sample")
	skill(t, shared)
	plan, e := s.Preview("adopt", p)
	if e != nil || plan.Action != "resolve" || len(plan.Record.Origins) != 2 {
		t.Fatalf("duplicate adoption did not create a keep-copy plan: %+v %v", plan, e)
	}
	if !absent(s.Store) {
		t.Fatal("preview wrote state")
	}
	if st, e := os.Stat(p); e != nil || !st.IsDir() {
		t.Fatal("source changed")
	}
}
func TestReplacedLinkIsNeverRemoved(t *testing.T) {
	for _, action := range []string{"enable", "disable", "restore"} {
		t.Run(action, func(t *testing.T) {
			s, p := fixture(t)
			r := apply(t, s, "adopt", p).Record
			plan, e := s.Preview(action, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			link := r.Links[0]
			if e = os.Remove(link); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(link, []byte("user data"), 0644); e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(plan); e == nil {
				t.Fatal("replaced link accepted")
			}
			b, e := os.ReadFile(link)
			if e != nil || string(b) != "user data" {
				t.Fatal("user data changed")
			}
			if absent(r.Library) {
				t.Fatal("library changed")
			}
		})
	}
}
func TestReadOnlyAndValidation(t *testing.T) {
	s, p := fixture(t)
	external := filepath.Join(s.Config.Home, "elsewhere", "sample")
	skill(t, external)
	if _, e := s.Preview("adopt", external); e == nil {
		t.Fatal("outside root accepted")
	}
	sym := filepath.Join(s.Config.Home, ".pi", "agent", "skills", "sample")
	if e := os.MkdirAll(filepath.Dir(sym), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(p, sym); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Preview("adopt", sym); e == nil {
		t.Fatal("symlink adopted")
	}
	system := filepath.Join(s.Config.Home, ".codex", "skills", ".system", "builtin")
	skill(t, system)
	if _, e := s.Preview("adopt", system); e == nil {
		t.Fatal("system skill adopted")
	}
	plugin := filepath.Join(s.Config.Home, ".codex", "plugins", "cache", "vendor", "skill")
	skill(t, plugin)
	if _, e := s.Preview("adopt", plugin); e == nil {
		t.Fatal("plugin adopted")
	}
	if e := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("bad"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Preview("adopt", p); e == nil {
		t.Fatal("malformed adopted")
	}
}
func TestRecoveryAtEveryStage(t *testing.T) {
	for _, action := range []string{"adopt", "disable", "enable", "restore"} {
		for _, stage := range []string{"journal", "partial", "executed", "committed"} {
			t.Run(action+"/"+stage, func(t *testing.T) {
				s, source := fixture(t)
				arg := source
				if action != "adopt" {
					r := apply(t, s, "adopt", source).Record
					arg = r.ID
					if action == "enable" {
						apply(t, s, "disable", arg)
					}
				}
				p, e := s.Preview(action, arg)
				if e != nil {
					t.Fatal(e)
				}
				if e = mkdir(s.Store); e != nil {
					t.Fatal(e)
				}
				if e = atomicJSON(filepath.Join(s.Store, "journal.json"), p); e != nil {
					t.Fatal(e)
				}
				if stage == "partial" {
					r := p.Record
					switch action {
					case "adopt":
						e = move(r.Original, r.Library, p.Identity)
					case "disable", "restore":
						e = unlink(r.Links[0], r.Library)
					case "enable":
						e = s.link(r.Links[0], r.Library)
					}
					if e != nil {
						t.Fatal(e)
					}
				}
				if stage == "executed" || stage == "committed" {
					if e = s.execute(p); e != nil {
						t.Fatal(e)
					}
				}
				if stage == "committed" {
					m, e := resultManifest(p)
					if e != nil {
						t.Fatal(e)
					}
					if e = s.save(m); e != nil {
						t.Fatal(e)
					}
				}
				if _, e = s.Preview("adopt", source); e == nil {
					t.Fatal("pending journal did not block mutation")
				}
				if e = s.Recover(); e != nil {
					t.Fatal(e)
				}
				if e = s.Recover(); e != nil {
					t.Fatal("recovery not idempotent", e)
				}
				report, e := s.Doctor()
				if e != nil || !report.Healthy {
					t.Fatalf("unhealthy after recovery: %+v %v", report, e)
				}
			})
		}
	}
}
func TestRecoveryConflict(t *testing.T) {
	s, source := fixture(t)
	p, e := s.Preview("adopt", source)
	if e != nil {
		t.Fatal(e)
	}
	if e = mkdir(s.Store); e != nil {
		t.Fatal(e)
	}
	if e = atomicJSON(filepath.Join(s.Store, "journal.json"), p); e != nil {
		t.Fatal(e)
	}
	if e = move(source, p.Record.Library, p.Identity); e != nil {
		t.Fatal(e)
	}
	shared := p.Record.Links[0]
	if e = os.MkdirAll(filepath.Dir(shared), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(shared, []byte("keep me"), 0644); e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(); e == nil {
		t.Fatal("conflict ignored")
	}
	b, _ := os.ReadFile(shared)
	if string(b) != "keep me" {
		t.Fatal("conflict overwritten")
	}
	if e = os.Remove(shared); e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(); e != nil {
		t.Fatal(e)
	}
}
func TestLockAndStalePreview(t *testing.T) {
	s, p := fixture(t)
	plan, e := s.Preview("adopt", p)
	if e != nil {
		t.Fatal(e)
	}
	unlock, e := s.lock()
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var applyErr error
	go func() { defer wg.Done(); applyErr = s.Apply(plan) }()
	wg.Wait()
	unlock()
	if applyErr == nil || !strings.Contains(applyErr.Error(), "another skmr") {
		t.Fatal("lock failed", applyErr)
	}
	if e = s.Apply(plan); e != nil {
		t.Fatal(e)
	}
	stale, e := s.Preview("disable", plan.Record.ID)
	if e != nil {
		t.Fatal(e)
	}
	apply(t, s, "disable", plan.Record.ID)
	if e = s.Apply(stale); e == nil {
		t.Fatal("stale preview applied")
	}
}
func TestPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	s, p := fixture(t)
	plan, e := s.Preview("adopt", p)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(filepath.Dir(p), 0555); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(filepath.Dir(p), 0755)
	if e = s.Apply(plan); e == nil {
		t.Fatal("permission failure expected")
	}
	if st, e := os.Stat(p); e != nil || !st.IsDir() {
		t.Fatal("source lost on permission failure")
	}
	if e = os.Chmod(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(); e != nil {
		t.Fatal(e)
	}
}
func TestProjectInheritanceAndRelocation(t *testing.T) {
	global, _ := fixture(t)
	root := filepath.Join(global.Config.Home, "repo")
	child := filepath.Join(root, "child")
	if e := os.MkdirAll(filepath.Join(root, ".git"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(child, 0755); e != nil {
		t.Fatal(e)
	}
	ancestor := filepath.Join(root, ".agents", "skills", "ancestor")
	skill(t, ancestor)
	s, e := New(Config{Home: global.Config.Home, Project: child})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Preview("adopt", ancestor); e == nil {
		t.Fatal("inherited adopted")
	}
	list, e := s.List()
	if e != nil {
		t.Fatal(e)
	}
	inherited := 0
	for _, item := range list.Skills {
		if item.Inherited {
			inherited++
		}
	}
	if inherited != 2 {
		t.Fatalf("expected global and ancestor, got %d", inherited)
	}
	local := filepath.Join(child, ".agents", "skills", "local")
	skill(t, local)
	r := apply(t, s, "adopt", local).Record
	manifest, e := os.ReadFile(filepath.Join(s.Store, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(manifest), child) {
		t.Fatal("project manifest has absolute paths")
	}
	moved := filepath.Join(global.Config.Home, "moved")
	if e = os.Rename(root, moved); e != nil {
		t.Fatal(e)
	}
	s, e = New(Config{Home: global.Config.Home, Project: filepath.Join(moved, "child")})
	if e != nil {
		t.Fatal(e)
	}
	apply(t, s, "disable", r.ID)
	apply(t, s, "enable", r.ID)
	apply(t, s, "restore", r.ID)
}
func TestManifestValidationAndSymlinkParents(t *testing.T) {
	s, p := fixture(t)
	r := apply(t, s, "adopt", p).Record
	m, e := s.load()
	if e != nil {
		t.Fatal(e)
	}
	m.Records[0].Links = append(m.Records[0].Links, filepath.Join(s.Config.Home, "unrelated"))
	if e = atomicJSON(filepath.Join(s.Store, "manifest.json"), m); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Preview("disable", r.ID); e == nil {
		t.Fatal("bad manifest accepted")
	}
	m.Records[0] = r
	m.Records[0].Origins = nil
	if e = atomicJSON(filepath.Join(s.Store, "manifest.json"), m); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(); e == nil {
		t.Fatal("version 2 record without origins accepted")
	}
	m.Records[0] = r
	m.Version = 99
	if e = atomicJSON(filepath.Join(s.Store, "manifest.json"), m); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(); e == nil {
		t.Fatal("unknown version accepted")
	}
}
func TestParentReplacement(t *testing.T) {
	s, p := fixture(t)
	r := apply(t, s, "adopt", p).Record
	plan, e := s.Preview("disable", r.ID)
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Dir(r.Links[0])
	moved := dir + "-old"
	if e = os.Rename(dir, moved); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(moved, dir); e != nil {
		t.Fatal(e)
	}
	if e = s.Apply(plan); e == nil {
		t.Fatal("symlink parent accepted")
	}
	if !owned(filepath.Join(moved, "sample"), r.Library) {
		t.Fatal("redirected link removed")
	}
}

func TestExclusiveRename(t *testing.T) {
	base := t.TempDir()
	from, to := filepath.Join(base, "from"), filepath.Join(base, "to")
	if e := os.Mkdir(from, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(to, 0755); e != nil {
		t.Fatal(e)
	}
	before, e := identity(to)
	if e != nil {
		t.Fatal(e)
	}
	if e = renameExclusive(from, to); e == nil {
		t.Fatal("exclusive rename overwrote existing directory")
	}
	after, e := identity(to)
	if e != nil || before != after {
		t.Fatal("destination replaced")
	}
	if absent(from) {
		t.Fatal("source moved")
	}
}

func TestInheritedManagedSkillsRemainVisibleWhenDisabled(t *testing.T) {
	global, _ := fixture(t)
	repo := filepath.Join(global.Config.Home, "repo")
	child := filepath.Join(repo, "child")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	parent, err := New(Config{Home: global.Config.Home, Project: repo})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repo, ".agents", "skills", "parent-skill")
	skill(t, source)
	record := apply(t, parent, "adopt", source).Record
	nested, err := New(Config{Home: global.Config.Home, Project: child})
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		if !enabled {
			apply(t, parent, "disable", record.ID)
		}
		item, err := nested.Show(record.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !item.Managed || !item.Inherited || !item.ReadOnly || item.Enabled != enabled {
			t.Fatalf("wrong inherited state: %+v", item)
		}
		if _, err := nested.Preview("enable", record.ID); err == nil {
			t.Fatal("child can change parent skill")
		}
	}
}

func TestDoctorReportsOccupiedDisabledPaths(t *testing.T) {
	s, source := fixture(t)
	record := apply(t, s, "adopt", source).Record
	apply(t, s, "disable", record.ID)
	link := record.Links[0]
	if err := os.WriteFile(link, []byte("unrelated content"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := s.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy || !strings.Contains(strings.Join(report.Issues, "\n"), link) {
		t.Fatalf("missed conflict: %+v", report)
	}
	data, err := os.ReadFile(link)
	if err != nil || string(data) != "unrelated content" {
		t.Fatal("diagnosis changed user content")
	}
}

func TestRestoreRecoveryChecksSourceBeforeRemovingLinks(t *testing.T) {
	s, source := fixture(t)
	record := apply(t, s, "adopt", source).Record
	plan, err := s.Preview("restore", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(s.Store, "journal.json"), plan); err != nil {
		t.Fatal(err)
	}
	saved := record.Library + "-saved"
	if err := os.Rename(record.Library, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(record.Library, 0755); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err == nil {
		t.Fatal("replaced source was accepted")
	}
	for _, path := range record.Links {
		if !owned(path, record.Library) {
			t.Fatalf("link removed before source validation: %s", path)
		}
	}
	if err := os.Remove(record.Library); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, record.Library); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
}

func TestInheritedJournalIsReportedAndGitBoundaryRespected(t *testing.T) {
	global, _ := fixture(t)
	repo := filepath.Join(global.Config.Home, "repo")
	child := filepath.Join(repo, "child")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	parent, err := New(Config{Home: global.Config.Home, Project: repo})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repo, ".agents", "skills", "parent-skill")
	skill(t, source)
	plan, err := parent.Preview("adopt", source)
	if err != nil {
		t.Fatal(err)
	}
	if err = mkdir(parent.Store); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(parent.Store, "journal.json"), plan); err != nil {
		t.Fatal(err)
	}
	// A corrupt manifest above the repository must never be loaded.
	outside := filepath.Join(global.Config.Home, ".skmr")
	if err = os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(outside, "manifest.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	nested, err := New(Config{Home: global.Config.Home, Project: child})
	if err != nil {
		t.Fatal(err)
	}
	report, err := nested.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	issues := strings.Join(report.Issues, "\n")
	if report.Healthy || !strings.Contains(issues, parent.Store) {
		t.Fatalf("missing inherited journal: %+v", report)
	}
	if strings.Contains(issues, outside) {
		t.Fatalf("crossed Git boundary: %+v", report)
	}
	if err = parent.Recover(); err != nil {
		t.Fatal(err)
	}
	report, err = nested.Doctor()
	if err != nil || !report.Healthy {
		t.Fatalf("unexpected report after recovery: %+v %v", report, err)
	}
}

func TestConflictClassificationAndResolutionLifecycle(t *testing.T) {
	s, canonicalPath := fixture(t)
	duplicatePath := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, duplicatePath)
	if err := os.Chmod(duplicatePath, 0700); err != nil {
		t.Fatal(err)
	}

	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 2 {
		t.Fatalf("expected two copies, got %+v", result.Skills)
	}
	for _, item := range result.Skills {
		if item.ConflictKind != "identical" || item.ConflictCount != 2 || item.ConflictID == "" {
			t.Fatalf("missing identical conflict metadata: %+v", item)
		}
	}
	adoptPlan, err := s.Preview("adopt", canonicalPath)
	if err != nil || adoptPlan.Action != "resolve" || len(adoptPlan.Record.Origins) != 2 {
		t.Fatalf("duplicate adoption did not create a keep-copy plan: %+v %v", adoptPlan, err)
	}

	if err = os.WriteFile(filepath.Join(duplicatePath, "different.txt"), []byte("keep this variant"), 0640); err != nil {
		t.Fatal(err)
	}
	result, err = s.List()
	if err != nil {
		t.Fatal(err)
	}
	var canonicalID string
	for _, item := range result.Skills {
		if item.ConflictKind != "divergent" {
			t.Fatalf("expected divergent conflict: %+v", item)
		}
		if item.Path == canonicalPath {
			canonicalID = item.ID
		}
	}

	plan, err := s.Preview("resolve", canonicalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 2 || len(plan.Record.Origins) != 2 || len(plan.Comparisons) != 1 || len(plan.Comparisons[0].Differences) == 0 {
		t.Fatalf("incomplete resolution plan: %+v", plan)
	}
	if absent(canonicalPath) || absent(duplicatePath) {
		t.Fatal("preview changed source folders")
	}
	if err = s.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if !owned(filepath.Join(s.Shared(), "sample"), plan.Record.Library) || !absent(canonicalPath) || !absent(duplicatePath) {
		t.Fatal("resolution did not leave one shared discovery link")
	}
	result, err = s.List()
	if err != nil || len(result.Skills) != 1 || !result.Skills[0].Managed || result.Skills[0].ConflictID != "" {
		t.Fatalf("conflict remained after resolution: %+v %v", result, err)
	}

	apply(t, s, "disable", plan.Record.ID)
	if !absent(filepath.Join(s.Shared(), "sample")) {
		t.Fatal("disable retained shared link")
	}
	apply(t, s, "enable", plan.Record.ID)
	apply(t, s, "restore", plan.Record.ID)
	if stat, statErr := os.Stat(canonicalPath); statErr != nil || !stat.IsDir() {
		t.Fatal("canonical origin was not restored", statErr)
	}
	data, err := os.ReadFile(filepath.Join(duplicatePath, "different.txt"))
	if err != nil || string(data) != "keep this variant" {
		t.Fatal("duplicate variant was not restored", err)
	}
	if stat, err := os.Stat(filepath.Join(duplicatePath, "different.txt")); err != nil || stat.Mode().Perm() != 0640 {
		t.Fatal("duplicate permissions were not preserved", err)
	}
}

func TestResolveLeavesExternalConflictVisible(t *testing.T) {
	s, canonicalPath := fixture(t)
	externalTarget := filepath.Join(s.Config.Home, "external", "sample")
	skill(t, externalTarget)
	externalLink := filepath.Join(s.Config.Home, ".pi", "agent", "skills", "sample")
	if err := os.MkdirAll(filepath.Dir(externalLink), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalTarget, externalLink); err != nil {
		t.Fatal(err)
	}
	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var canonicalID string
	for _, item := range result.Skills {
		if item.Path == canonicalPath {
			canonicalID = item.ID
		}
		if item.ConflictKind != "external" || len(item.Unresolved) == 0 {
			t.Fatalf("external conflict not classified: %+v", item)
		}
	}
	plan := apply(t, s, "resolve", canonicalID)
	if _, err := os.Lstat(externalLink); err != nil {
		t.Fatal("external link was modified", err)
	}
	result, err = s.List()
	if err != nil {
		t.Fatal(err)
	}
	foundManaged, foundExternal := false, false
	for _, item := range result.Skills {
		foundManaged = foundManaged || item.ID == plan.Record.ID && item.Managed
		foundExternal = foundExternal || item.Path == externalLink && item.ReadOnly
	}
	if !foundManaged || !foundExternal {
		t.Fatalf("remaining external conflict hidden: %+v", result.Skills)
	}
}

func TestVersionOneManifestLoadsAsLegacyConflict(t *testing.T) {
	s, source := fixture(t)
	item := skills.Parse(source)
	library := filepath.Join(s.Store, "library", item.ID, item.Name)
	shared := filepath.Join(s.Shared(), item.Name)
	legacy := Manifest{Version: legacyVersion, Records: []Record{{
		ID: item.ID, Name: item.Name, Original: source, Library: library,
		Links: []string{source, shared}, Enabled: true,
	}}}
	if err := mkdir(s.Store); err != nil {
		t.Fatal(err)
	}
	identity, err := identity(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = move(source, library, identity); err != nil {
		t.Fatal(err)
	}
	for _, link := range legacy.Records[0].Links {
		if err = s.link(link, library); err != nil {
			t.Fatal(err)
		}
	}
	if err = atomicJSON(filepath.Join(s.Store, "manifest.json"), legacy); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load()
	if err != nil || loaded.Version != Version || len(loaded.Records[0].Origins) != 1 {
		t.Fatalf("legacy manifest did not migrate in memory: %+v %v", loaded, err)
	}
	result, err := s.List()
	if err != nil || len(result.Skills) != 1 || result.Skills[0].ConflictKind != "identical" {
		t.Fatalf("legacy discovery conflict not reported: %+v %v", result, err)
	}
}

func TestInterruptedResolutionRecovery(t *testing.T) {
	s, canonical := fixture(t)
	duplicate := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, duplicate)
	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var canonicalID string
	for _, item := range result.Skills {
		if item.Path == canonical {
			canonicalID = item.ID
		}
	}
	plan, err := s.Preview("resolve", canonicalID)
	if err != nil {
		t.Fatal(err)
	}
	if err = mkdir(s.Store); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(s.Store, "journal.json"), plan); err != nil {
		t.Fatal(err)
	}
	if err = move(plan.Moves[0].From, plan.Moves[0].To, plan.Moves[0].Identity); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal("resolution recovery is not idempotent", err)
	}
	result, err = s.List()
	if err != nil || len(result.Skills) != 1 || !result.Skills[0].Managed {
		t.Fatalf("resolution recovery incomplete: %+v %v", result, err)
	}
}

func TestResolveCanReplaceManagedCanonical(t *testing.T) {
	s, oldPath := fixture(t)
	oldRecord := apply(t, s, "adopt", oldPath).Record
	newPath := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, newPath)
	if err := os.WriteFile(filepath.Join(newPath, "new.txt"), []byte("new canonical"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var newID string
	for _, item := range result.Skills {
		if item.Path == newPath {
			newID = item.ID
		}
	}
	plan := apply(t, s, "resolve", newID)
	if plan.Record.ID == oldRecord.ID || len(plan.Record.Origins) != 2 {
		t.Fatalf("canonical was not replaced: %+v", plan.Record)
	}
	if !absent(oldRecord.Library) || !owned(filepath.Join(s.Shared(), "sample"), plan.Record.Library) {
		t.Fatal("old canonical remained active")
	}
	apply(t, s, "restore", plan.Record.ID)
	if _, err := os.Stat(filepath.Join(oldPath, "scripts", "run.sh")); err != nil {
		t.Fatal("old canonical was not restored", err)
	}
	if data, err := os.ReadFile(filepath.Join(newPath, "new.txt")); err != nil || string(data) != "new canonical" {
		t.Fatal("new canonical was not restored", err)
	}
}

func TestCanonicalSwitchRecoveryAfterExecution(t *testing.T) {
	s, oldPath := fixture(t)
	oldRecord := apply(t, s, "adopt", oldPath).Record
	newPath := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, newPath)
	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var newID string
	for _, item := range result.Skills {
		if item.Path == newPath {
			newID = item.ID
		}
	}
	plan, err := s.Preview("resolve", newID)
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(s.Store, "journal.json"), plan); err != nil {
		t.Fatal(err)
	}
	if err = s.execute(plan); err != nil {
		t.Fatal(err)
	}
	if owned(oldRecord.Links[0], oldRecord.Library) || !owned(plan.Record.Links[0], plan.Record.Library) {
		t.Fatal("canonical link was not switched")
	}
	if err = s.Recover(); err != nil {
		t.Fatal("executed canonical switch did not recover", err)
	}
	manifest, err := s.load()
	if err != nil || len(manifest.Records) != 1 || manifest.Records[0].ID != newID {
		t.Fatalf("canonical switch manifest was not committed: %+v %v", manifest, err)
	}
}

func TestResolveRejectsOccupiedReservedOrigin(t *testing.T) {
	s, original := fixture(t)
	record := apply(t, s, "adopt", original).Record
	skill(t, original)
	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 2 || result.Skills[0].ID == result.Skills[1].ID {
		t.Fatalf("reserved origin did not receive a distinct conflict ID: %+v", result.Skills)
	}
	if _, err = s.Preview("resolve", record.ID); err == nil || !strings.Contains(err.Error(), "reserved restore path") {
		t.Fatal("occupied reserved origin was accepted", err)
	}
	report, err := s.Doctor()
	if err != nil || report.Healthy || !strings.Contains(strings.Join(report.Issues, "\n"), "Restore path is occupied") {
		t.Fatalf("doctor missed occupied restore path: %+v %v", report, err)
	}
}

func TestVersionOneJournalRecovery(t *testing.T) {
	s, source := fixture(t)
	item := skills.Parse(source)
	library := filepath.Join(s.Store, "library", item.ID, item.Name)
	shared := filepath.Join(s.Shared(), item.Name)
	id, err := identity(source)
	if err != nil {
		t.Fatal(err)
	}
	legacy := Plan{
		Version: legacyVersion,
		Action:  "adopt",
		Record: Record{
			ID: item.ID, Name: item.Name, Original: source, Library: library,
			Links: []string{source, shared}, Enabled: true,
		},
		Identity: id,
		Before:   Manifest{Version: legacyVersion, Records: []Record{}},
	}
	if err = mkdir(s.Store); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(s.Store, "journal.json"), legacy); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	manifest, err := s.load()
	if err != nil || manifest.Version != Version || len(manifest.Records) != 1 || len(manifest.Records[0].Origins) != 1 {
		t.Fatalf("legacy journal was not upgraded: %+v %v", manifest, err)
	}
}

func TestResolutionRejectsContentChangedAfterPreview(t *testing.T) {
	s, canonical := fixture(t)
	duplicate := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, duplicate)
	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var canonicalID string
	for _, item := range result.Skills {
		if item.Path == canonical {
			canonicalID = item.ID
		}
	}
	plan, err := s.Preview("resolve", canonicalID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(duplicate, "changed.txt"), []byte("changed after review"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(plan); err == nil || !strings.Contains(err.Error(), "state changed") {
		t.Fatal("changed package was accepted", err)
	}
	if absent(canonical) || absent(duplicate) {
		t.Fatal("stale resolution changed source folders")
	}
}

func TestProjectResolutionRemainsRelocatable(t *testing.T) {
	global, _ := fixture(t)
	repo := filepath.Join(global.Config.Home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Home: global.Config.Home, Project: repo})
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(repo, ".pi", "skills", "sample")
	duplicate := filepath.Join(repo, ".opencode", "skills", "sample")
	skill(t, canonical)
	skill(t, duplicate)
	result, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var canonicalID string
	for _, item := range result.Skills {
		if item.Path == canonical {
			canonicalID = item.ID
		}
	}
	plan := apply(t, s, "resolve", canonicalID)
	manifestData, err := os.ReadFile(filepath.Join(s.Store, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifestData), repo) {
		t.Fatal("resolved project manifest contains absolute project paths")
	}
	moved := filepath.Join(global.Config.Home, "moved-repo")
	if err = os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}
	s, err = New(Config{Home: global.Config.Home, Project: moved})
	if err != nil {
		t.Fatal(err)
	}
	apply(t, s, "disable", plan.Record.ID)
	apply(t, s, "enable", plan.Record.ID)
	apply(t, s, "restore", plan.Record.ID)
	for _, path := range []string{filepath.Join(moved, ".pi", "skills", "sample"), filepath.Join(moved, ".opencode", "skills", "sample")} {
		if stat, statErr := os.Stat(path); statErr != nil || !stat.IsDir() {
			t.Fatal("project duplicate was not restored after relocation", path, statErr)
		}
	}
}

func TestResolutionCollisionDoesNotMoveSources(t *testing.T) {
	s, canonical := fixture(t)
	duplicate := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, duplicate)
	canonicalID := skills.ID(canonical)
	backup := filepath.Join(s.Store, "library", canonicalID, ".skmr-duplicates", skills.ID(duplicate), "sample")
	if err := os.MkdirAll(backup, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preview("resolve", canonicalID); err == nil || !strings.Contains(err.Error(), "destination already exists") {
		t.Fatal("backup collision was accepted", err)
	}
	for _, path := range []string{canonical, duplicate} {
		if stat, err := os.Stat(path); err != nil || !stat.IsDir() {
			t.Fatal("collision changed source", path, err)
		}
	}
}

func TestResolutionPermissionFailureRecovers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	s, canonical := fixture(t)
	duplicate := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, duplicate)
	plan, err := s.Preview("resolve", skills.ID(canonical))
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(duplicate)
	if err = os.Chmod(parent, 0555); err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(plan); err == nil {
		t.Fatal("permission failure expected")
	}
	if err = os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	result, err := s.List()
	if err != nil || len(result.Skills) != 1 || !result.Skills[0].Managed {
		t.Fatalf("permission recovery incomplete: %+v %v", result, err)
	}
}

func TestResolutionRejectsReplacedManagedLink(t *testing.T) {
	s, source := fixture(t)
	record := apply(t, s, "adopt", source).Record
	duplicate := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, duplicate)
	plan, err := s.Preview("resolve", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	shared := record.Links[0]
	if err = os.Remove(shared); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(shared, []byte("keep me"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(plan); err == nil {
		t.Fatal("replaced shared link was accepted")
	}
	data, err := os.ReadFile(shared)
	if err != nil || string(data) != "keep me" {
		t.Fatal("replaced shared link was modified", err)
	}
}
