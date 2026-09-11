package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faizmokh/skmr/internal/skills"
)

func setup(t *testing.T) string {
	t.Helper()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	p := filepath.Join(home, ".agents", "skills", "sample")
	if e = os.MkdirAll(p, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("---\nname: sample\ndescription: Example skill\n---\nHello \x1b[31mworld\x1b[0m\n"), 0644); e != nil {
		t.Fatal(e)
	}
	return p
}
func run(args ...string) (string, error) {
	root := New(BuildInfo{Version: "test", Commit: "abc123", Date: "2026-09-07T00:00:00Z"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	e := root.Execute()
	return out.String(), e
}

func TestHumanStatusLabelsPreserveJSONValues(t *testing.T) {
	tests := []struct {
		skill skills.Skill
		want  string
	}{
		{skills.Skill{}, "available"},
		{skills.Skill{ReadOnly: true}, "available / view only"},
		{skills.Skill{Managed: true, Enabled: true}, "enabled"},
		{skills.Skill{Managed: true}, "disabled"},
		{skills.Skill{Installed: true, ReadOnly: true}, "installed"},
		{skills.Skill{ConflictKind: "identical"}, "available / same copies"},
		{skills.Skill{ConflictKind: "agent_config"}, "available / same content, different agent config"},
		{skills.Skill{ConflictKind: "divergent"}, "available / different copies"},
		{skills.Skill{ConflictKind: "external"}, "available / view-only copies"},
		{skills.Skill{ConflictKind: "future"}, "available / copy conflict"},
	}
	for _, test := range tests {
		if got := status(test.skill); got != test.want {
			t.Errorf("status(%+v) = %q, want %q", test.skill, got, test.want)
		}
	}

	data, err := json.Marshal(skills.Skill{ReadOnly: true, ConflictKind: "divergent"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"read_only":true`) || !strings.Contains(text, `"conflict_kind":"divergent"`) {
		t.Fatalf("JSON compatibility changed: %s", text)
	}
}

func TestListShowAndDryRun(t *testing.T) {
	p := setup(t)
	out, e := run("list", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var result skills.Result
	if e = json.Unmarshal([]byte(out), &result); e != nil || len(result.Skills) != 1 {
		t.Fatalf("bad JSON: %s %v", out, e)
	}
	id := result.Skills[0].ID
	out, e = run("show", id)
	if e != nil || strings.ContainsRune(out, '\x1b') || !strings.Contains(out, "Hello world") {
		t.Fatalf("unsafe output %q %v", out, e)
	}
	out, e = run("adopt", p, "--dry-run")
	if e != nil || !strings.Contains(out, "Move ") {
		t.Fatal(out, e)
	}
	if _, e = os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "skmr")); !os.IsNotExist(e) {
		t.Fatal("dry run wrote state")
	}
	if _, e = run("adopt", p); e == nil || !strings.Contains(e.Error(), "--yes") {
		t.Fatal("noninteractive adoption must require --yes", e)
	}
	if _, e = run("adopt", p, "--yes"); e != nil {
		t.Fatal(e)
	}
	if _, e = run("disable", id); e != nil {
		t.Fatal(e)
	}
	if _, e = run("enable", id); e != nil {
		t.Fatal(e)
	}
	if _, e = run("restore", id, "--yes"); e != nil {
		t.Fatal(e)
	}
}

func TestMoveAndCopyCommands(t *testing.T) {
	path := setup(t)
	if _, err := run("adopt", path, "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err := run("list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var result skills.Result
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	id := result.Skills[0].ID
	project := filepath.Join(os.Getenv("HOME"), "app")
	other := filepath.Join(os.Getenv("HOME"), "other")
	if err = os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	out, err = run("move", id, "--to-project", project, "--dry-run")
	if err != nil || !strings.Contains(out, "Destination scope: project "+project) {
		t.Fatalf("bad move dry run: %s %v", out, err)
	}
	if _, statErr := os.Stat(filepath.Join(project, ".skmr")); !os.IsNotExist(statErr) {
		t.Fatal("move dry run created destination state")
	}
	if _, err = run("move", id, "--to-project", project, "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err = run("list", "--project", project, "--json")
	if err != nil || !strings.Contains(out, `"owner_project": "`+project+`"`) {
		t.Fatalf("owner project missing: %s %v", out, err)
	}
	if _, err = run("copy", id, "--project", project, "--to-project", other, "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err = run("list", "--project", other, "--json")
	if err != nil || !strings.Contains(out, `"managed": true`) {
		t.Fatalf("copied skill missing: %s %v", out, err)
	}
}

func TestTransferRequiresOneDestination(t *testing.T) {
	setup(t)
	for _, args := range [][]string{{"move", "id"}, {"copy", "id", "--to-global", "--to-project", "auto"}} {
		if _, err := run(args...); err == nil {
			t.Fatal("expected destination error", args)
		}
	}
}
func TestHelpAndErrors(t *testing.T) {
	setup(t)
	if out, e := run(); e != nil || !strings.Contains(out, "Available Commands") {
		t.Fatal(out, e)
	}
	for _, args := range [][]string{{"tui"}, {"show"}, {"show", "missing"}, {"enable", "missing"}, {"list", "--global", "--project", "auto"}, {"list", "--project", "/does/not/exist"}} {
		if _, e := run(args...); e == nil {
			t.Fatal("expected error", args)
		}
	}
}

func TestVersion(t *testing.T) {
	setup(t)
	out, err := run("version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "skmr test") || !strings.Contains(out, "commit: abc123") || !strings.Contains(out, "built: 2026-09-07T00:00:00Z") {
		t.Fatalf("unexpected version output: %q", out)
	}

	out, err = run("--version")
	if err != nil {
		t.Fatal(err)
	}
	if out != "skmr test\n" {
		t.Fatalf("unexpected short version output: %q", out)
	}
}

func TestAddGroupUsesCurrentGitProject(t *testing.T) {
	source := setup(t)
	if _, err := run("adopt", source, "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("disable", "sample"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("group", "create", "backend", "sample"); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(os.Getenv("HOME"), "repo")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	out, err := run("add", "@backend")
	if err != nil || !strings.Contains(out, "project now has 1 skills") {
		t.Fatalf("group add failed: %s %v", out, err)
	}
	link := filepath.Join(project, ".agents", "skills", "sample")
	if _, err = os.Readlink(link); err != nil {
		t.Fatal("project skill link missing", err)
	}
	out, err = run("group", "list", "--json")
	if err != nil || !strings.Contains(out, `"name": "backend"`) {
		t.Fatalf("group list failed: %s %v", out, err)
	}
	if _, err = run("remove", "@backend"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("project skill link was not removed")
	}
}

func TestAddRequiresGitOrExplicitProject(t *testing.T) {
	source := setup(t)
	if _, err := run("adopt", source, "--yes"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if _, err = run("add", "sample"); err == nil || !strings.Contains(err.Error(), "outside Git") {
		t.Fatal("add outside Git did not require --project", err)
	}
}

func TestDoctorExitStatus(t *testing.T) {
	p := setup(t)
	out, e := run("doctor", "--json")
	if e != nil || !strings.Contains(out, `"healthy": true`) {
		t.Fatal(out, e)
	}
	if e = os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("bad"), 0644); e != nil {
		t.Fatal(e)
	}
	out, e = run("doctor", "--json")
	if e == nil {
		t.Fatal("doctor returned success for malformed skill")
	}
	if !json.Valid([]byte(out)) || !strings.Contains(out, `"healthy": false`) {
		t.Fatal(out)
	}
}

func TestAdoptDuplicateDryRunAndNoninteractiveUse(t *testing.T) {
	shared := setup(t)
	home := os.Getenv("HOME")
	duplicate := filepath.Join(home, ".codex", "skills", "sample")
	if err := os.MkdirAll(duplicate, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(duplicate, "SKILL.md"), []byte("---\nname: sample\ndescription: Different copy\n---\nDifferent\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := run("list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var result skills.Result
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Skills {
		if item.ConflictKind != "divergent" || item.ConflictCount != 2 {
			t.Fatalf("missing conflict JSON: %+v", item)
		}
	}
	out, err = run("adopt", shared, "--dry-run")
	if err != nil || !strings.Contains(out, "Keep:    "+shared) || !strings.Contains(out, "Back up: "+duplicate) || !strings.Contains(out, "Link:    "+shared) {
		t.Fatal(out, err)
	}
	if _, err = os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "skmr")); !os.IsNotExist(err) {
		t.Fatal("duplicate adoption dry run wrote state")
	}
	if _, err = run("adopt", shared); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("noninteractive duplicate adoption must require --yes", err)
	}
	if _, err = run("adopt", shared, "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err = run("list", "--json")
	if err != nil || !strings.Contains(out, `"managed": true`) || strings.Contains(out, `"conflict_id"`) {
		t.Fatalf("adoption did not clear writable conflict: %s %v", out, err)
	}
}

func TestAdoptMultiplePathsAndAll(t *testing.T) {
	first := setup(t)
	home := os.Getenv("HOME")
	second := filepath.Join(home, ".pi", "agent", "skills", "second")
	if err := os.MkdirAll(second, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "SKILL.md"), []byte("---\nname: second\ndescription: Second skill\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := run("adopt", first, second, "--dry-run")
	if err != nil || !strings.Contains(out, "Add 2 skills") || !strings.Contains(out, "sample") || !strings.Contains(out, "second") {
		t.Fatalf("bad multi-path preview: %s %v", out, err)
	}
	if _, statErr := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "skmr")); !os.IsNotExist(statErr) {
		t.Fatal("multi-path dry run wrote state")
	}
	if _, err = run("adopt", first, second); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("multi-path noninteractive use did not require --yes", err)
	}
	if _, err = run("adopt", first, second, "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err = run("list", "--json")
	if err != nil || strings.Count(out, `"managed": true`) != 2 {
		t.Fatalf("batch adoption missing: %s %v", out, err)
	}
}

func TestAdoptAllSkipsDuplicateNames(t *testing.T) {
	shared := setup(t)
	home := os.Getenv("HOME")
	duplicate := filepath.Join(home, ".codex", "skills", "sample")
	unique := filepath.Join(home, ".codex", "skills", "unique")
	for path, content := range map[string]string{
		duplicate: "---\nname: sample\ndescription: Another copy\n---\n",
		unique:    "---\nname: unique\ndescription: Unique skill\n---\n",
	} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run("adopt", "--all", "--dry-run")
	if err != nil || !strings.Contains(out, "Skipped duplicate-name skills: sample") || !strings.Contains(out, "unique") || strings.Contains(out, "Move "+shared) {
		t.Fatalf("bad --all preview: %s %v", out, err)
	}
	if _, err = run("adopt", "--all", shared); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatal("--all accepted an explicit path", err)
	}
}

func TestAdoptAllWithNoSafeSkills(t *testing.T) {
	path := setup(t)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	out, err := run("adopt", "--all")
	if err != nil || !strings.Contains(out, "No safe unmanaged skills to add.") {
		t.Fatalf("unexpected empty --all result: %s %v", out, err)
	}
}
