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

func TestResolveJSONDryRunAndNoninteractiveUse(t *testing.T) {
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
	var canonicalID string
	for _, item := range result.Skills {
		if item.Path == shared {
			canonicalID = item.ID
		}
		if item.ConflictKind != "divergent" || item.ConflictCount != 2 {
			t.Fatalf("missing conflict JSON: %+v", item)
		}
	}
	out, err = run("resolve", canonicalID, "--dry-run")
	if err != nil || !strings.Contains(out, "Move ") || !strings.Contains(out, "Create link ") {
		t.Fatal(out, err)
	}
	if _, err = os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "skmr")); !os.IsNotExist(err) {
		t.Fatal("resolve dry run wrote state")
	}
	if _, err = run("resolve", canonicalID); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("noninteractive resolution must require --yes", err)
	}
	if _, err = run("resolve", canonicalID, "--yes"); err != nil {
		t.Fatal(err)
	}
	out, err = run("list", "--json")
	if err != nil || !strings.Contains(out, `"managed": true`) || strings.Contains(out, `"conflict_id"`) {
		t.Fatalf("resolution did not clear writable conflict: %s %v", out, err)
	}
}
