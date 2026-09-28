package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeSkillsCLI(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *" find "*)
    printf 'Install with npx skills add <owner/repo@skill>\n└ https://skills.sh/example/skills/alpha\n'
    exit 0 ;;
esac
case "$*" in
  *" --list"*)
    printf '◇  Available Skills\n│\n│    alpha\n│      First skill\n│    beta\n│      Second skill\n└  Use --skill <name> to install specific skills\n'
    exit 0 ;;
esac
name=''
previous=''
for arg in "$@"; do
  if [ "$previous" = '--skill' ]; then name="$arg"; fi
  previous="$arg"
done
if [ -z "$name" ]; then exit 2; fi
path="$(pwd -P)/.agents/skills/$name"
mkdir -p "$path"
printf '%s\n' '---' "name: $name" 'description: Example skill' '---' "version ${SKMR_TEST_VERSION:-1}" > "$path/SKILL.md"
printf 'Progress before JSON\n[\n  {"name":"%s","status":"installed","path":"%s","ref":"main"}\n]\n' "$name" "$path"
`
	path := filepath.Join(bin, "npx")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func remoteService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Home: filepath.Join(root, "home"), DataHome: filepath.Join(root, "data"), ConfigHome: filepath.Join(root, "config")})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(s.Config.Home, 0755); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRemoteImportUpdateAndProjectLink(t *testing.T) {
	fakeSkillsCLI(t)
	s := remoteService(t)
	url := "https://github.com/example/skills"
	candidates, err := s.DiscoverRemote(url)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("discover: %+v, %v", candidates, err)
	}
	plan, err := s.PrepareImport(url, []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyRemote(plan); err != nil {
		t.Fatal(err)
	}
	if !absent(filepath.Join(s.Shared(), "alpha")) {
		t.Fatal("import unexpectedly enabled skill")
	}
	result, err := s.List()
	if err != nil || len(result.Skills) != 1 || !result.Skills[0].Remote || result.Skills[0].Enabled {
		t.Fatalf("list: %+v, %v", result, err)
	}
	if _, err = s.PrepareImport(url, []string{"alpha"}); err == nil {
		t.Fatal("duplicate import allowed")
	}
	enable, err := s.Preview("enable", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(enable); err != nil {
		t.Fatal(err)
	}
	if !owned(filepath.Join(s.Shared(), "alpha"), result.Skills[0].Path) {
		t.Fatal("global link missing")
	}
	project := filepath.Join(t.TempDir(), "project")
	project, err = filepath.EvalSymlinks(filepath.Dir(project))
	if err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(project, "project")
	if err = os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	ps, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	add, err := ps.PreviewPackages("add", []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if err = ps.ApplyPackages(add); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKMR_TEST_VERSION", "2")
	update, err := s.PrepareUpdate("alpha", false)
	if err != nil || len(update.Entries) != 1 || len(update.Entries[0].Differences) == 0 {
		t.Fatalf("update preview: %+v, %v", update, err)
	}
	if err = s.ApplyRemote(update); err != nil {
		t.Fatal(err)
	}
	if !owned(filepath.Join(ps.Shared(), "alpha"), result.Skills[0].Path) {
		t.Fatal("project link changed after update")
	}
	if err = os.WriteFile(filepath.Join(result.Skills[0].Path, "SKILL.md"), []byte("local edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareUpdate("alpha", false); err == nil || !strings.Contains(err.Error(), "local edits") {
		t.Fatalf("local edit protection: %v", err)
	}
	replace, err := s.PrepareUpdate("alpha", true)
	if err != nil {
		t.Fatal(err)
	}
	replace.Cleanup()
}

func TestRemoteSearchAndInvalidURL(t *testing.T) {
	fakeSkillsCLI(t)
	results, err := SearchRemote("example")
	if err != nil || len(results) != 1 || results[0].Name != "alpha" {
		t.Fatalf("search: %+v, %v", results, err)
	}
	if _, _, err = normalizeRemote("https://github.com/example/skills?token=secret"); err == nil {
		t.Fatal("accepted URL secret")
	}
	if source, skill, err := normalizeRemote("https://skills.sh/example/skills/alpha"); err != nil || source != "https://github.com/example/skills" || skill != "alpha" {
		t.Fatalf("page URL: %s, %s, %v", source, skill, err)
	}
	if source, skill, err := normalizeRemote("https://skills.sh/p/pack123"); err != nil || source != "https://skills.sh/p/pack123" || skill != "" {
		t.Fatalf("pack URL: %s, %s, %v", source, skill, err)
	}
	if source, skill, err := normalizeRemote("https://skills.sh/mintlify.com/alpha"); err != nil || source != "https://mintlify.com" || skill != "alpha" {
		t.Fatalf("well-known URL: %s, %s, %v", source, skill, err)
	}
	s := remoteService(t)
	pack, err := s.PrepareImport("https://skills.sh/p/pack123", []string{"beta"})
	if err != nil || len(pack.Entries) != 1 {
		t.Fatalf("pack import: %+v, %v", pack, err)
	}
	pack.Cleanup()
	known, err := s.PrepareImport("https://skills.sh/mintlify.com/alpha", []string{"alpha"})
	if err != nil || len(known.Entries) != 1 {
		t.Fatalf("well-known import: %+v, %v", known, err)
	}
	known.Cleanup()
}

func TestFailedRemoteStageLeavesLibraryUntouched(t *testing.T) {
	bin := t.TempDir()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "npx"), []byte("#!/bin/sh\nmkdir -p .agents/skills/alpha\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", tmp)
	s := remoteService(t)
	if _, err := s.PrepareImport("https://github.com/example/skills", []string{"alpha"}); err == nil {
		t.Fatal("failed download accepted")
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging was left behind: %+v, %v", entries, err)
	}
	if !absent(filepath.Join(s.Store, "manifest.json")) {
		t.Fatal("failed staging wrote manifest")
	}
}

func TestRecoverInterruptedRemoteWrites(t *testing.T) {
	fakeSkillsCLI(t)
	s := remoteService(t)
	plan, err := s.PrepareImport("https://github.com/example/skills", []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(s.Store, 0755); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(s.remoteJournalPath(), remoteJournal{Version: Version, Action: "import", Before: before, Entries: plan.Entries}); err != nil {
		t.Fatal(err)
	}
	e := plan.Entries[0]
	if err = copyPackage(e.StagedPath, e.Record.Library, e.Record.Remote.Digest); err != nil {
		t.Fatal(err)
	}
	plan.Cleanup()
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	if !absent(e.Record.Library) || !absent(s.remoteJournalPath()) {
		t.Fatal("interrupted import was not rolled back")
	}
	plan, err = s.PrepareImport("https://github.com/example/skills", []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyRemote(plan); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKMR_TEST_VERSION", "2")
	update, err := s.PrepareUpdate("alpha", false)
	if err != nil {
		t.Fatal(err)
	}
	before, err = s.load()
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(s.remoteJournalPath(), remoteJournal{Version: Version, Action: "update", Before: before, Entries: update.Entries}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(filepath.Dir(e.Record.Library), ".skmr-backup")
	if err = os.Rename(e.Record.Library, backup); err != nil {
		t.Fatal(err)
	}
	update.Cleanup()
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	if absent(e.Record.Library) || !absent(backup) || !absent(s.remoteJournalPath()) {
		t.Fatal("interrupted update was not rolled back")
	}
}
