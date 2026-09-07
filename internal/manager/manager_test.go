package manager

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
				p = filepath.Join(project, ".pi", "skills", "sample")
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
func TestCollisionDoesNotOverwrite(t *testing.T) {
	s, p := fixture(t)
	shared := filepath.Join(s.Shared(), "sample")
	skill(t, shared)
	if _, e := s.Preview("adopt", p); e == nil {
		t.Fatal("collision accepted")
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
			if e = os.Remove(p); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(p, []byte("user data"), 0644); e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(plan); e == nil {
				t.Fatal("replaced link accepted")
			}
			b, e := os.ReadFile(p)
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
	if e = os.WriteFile(source, []byte("keep me"), 0644); e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(); e == nil {
		t.Fatal("conflict ignored")
	}
	b, _ := os.ReadFile(source)
	if string(b) != "keep me" {
		t.Fatal("conflict overwritten")
	}
	if e = os.Remove(source); e != nil {
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
	dir := filepath.Dir(p)
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
	if err := os.WriteFile(source, []byte("unrelated content"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := s.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy || !strings.Contains(strings.Join(report.Issues, "\n"), source) {
		t.Fatalf("missed conflict: %+v", report)
	}
	data, err := os.ReadFile(source)
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
