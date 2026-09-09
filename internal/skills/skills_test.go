package skills

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/faizmokh/skmr/internal/agents"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if e := os.MkdirAll(path, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0644); e != nil {
		t.Fatal(e)
	}
}
func TestDiscovery(t *testing.T) {
	home := t.TempDir()
	roots := agents.Global(home, filepath.Join(home, ".config"))
	for i, root := range roots {
		if root.ReadOnly {
			continue
		}
		name := []string{"shared", "codex", "opencode", "pi", "claude"}[i]
		write(t, filepath.Join(root.Path, name), "---\nname: "+name+"\ndescription: Test skill\n---\nHello")
	}
	write(t, filepath.Join(roots[0].Path, "group", "invalid"), "broken")
	if e := os.Symlink(roots[0].Path, filepath.Join(roots[0].Path, "cycle")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(roots[0].Path, "shared"), filepath.Join(roots[1].Path, "shared")); e != nil {
		t.Fatal(e)
	}
	out := Scan(roots)
	if len(out.Skills) != 7 {
		t.Fatalf("unexpected skills: %+v", out)
	}
	if len(out.Issues) == 0 {
		t.Fatal("cycle not reported")
	}
	linked, invalid, shared := false, false, false
	for _, s := range out.Skills {
		if s.Path == filepath.Join(roots[1].Path, "shared") {
			linked = s.ReadOnly
		}
		if s.Name == "invalid" {
			invalid = len(s.Issues) > 0
		}
		if s.Path == filepath.Join(roots[0].Path, "shared") {
			shared = len(s.Agents) == 3
		}
	}
	if !linked || !invalid || !shared {
		t.Fatal("missing metadata", out)
	}
}
func TestMalformedFrontmatter(t *testing.T) {
	for _, body := range []string{"plain text", "---\nname: test", "---\nname: [\n---", "---\nname: ../escape\ndescription: Test\n---", "---\nname: test\n---", "---\nname: test\nname: duplicate\ndescription: Hello\n---"} {
		p := filepath.Join(t.TempDir(), "test")
		write(t, p, body)
		if len(Parse(p).Issues) == 0 {
			t.Fatalf("invalid skill accepted: %s", body)
		}
	}
}
func TestCRLFAndSizeLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test")
	write(t, p, "---\r\nname: test\r\ndescription: Hello\r\n---\r\nBody")
	if len(Parse(p).Issues) != 0 {
		t.Fatal(Parse(p).Issues)
	}
	write(t, p, strings.Repeat("x", MaxDocument+1))
	if _, e := Read(p); e == nil {
		t.Fatal("oversized skill read")
	}
}

func TestUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := filepath.Join(t.TempDir(), "skills")
	if e := os.Mkdir(root, 0000); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(root, 0755)
	result := Scan([]agents.Root{{Path: root, Scope: "global"}})
	if len(result.Issues) == 0 {
		t.Fatal("permission problem not reported")
	}
}

func TestNonRegularDocument(t *testing.T) {
	path := t.TempDir()
	if e := syscall.Mkfifo(filepath.Join(path, "SKILL.md"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Read(path); e == nil {
		t.Fatal("FIFO accepted as skill content")
	}
}

func TestDigestIncludesContentModesAndSymlinkTargets(t *testing.T) {
	one := filepath.Join(t.TempDir(), "skill")
	two := filepath.Join(t.TempDir(), "skill")
	for _, path := range []string{one, two} {
		write(t, path, "---\nname: skill\ndescription: Test\n---\nBody")
		if err := os.WriteFile(filepath.Join(path, "run.sh"), []byte("echo ok\n"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("run.sh", filepath.Join(path, "run")); err != nil {
			t.Fatal(err)
		}
	}
	oneDigest, err := Digest(one)
	if err != nil {
		t.Fatal(err)
	}
	twoDigest, err := Digest(two)
	if err != nil || oneDigest != twoDigest {
		t.Fatal("identical package digests differ", err)
	}
	differences, err := Differences(one, two)
	if err != nil || len(differences) != 0 {
		t.Fatal("identical packages reported differences", differences, err)
	}
	if err = os.Chmod(filepath.Join(two, "run.sh"), 0644); err != nil {
		t.Fatal(err)
	}
	modeDigest, err := Digest(two)
	if err != nil || modeDigest == oneDigest {
		t.Fatal("permission change missing from digest", err)
	}
	differences, err = Differences(one, two)
	if err != nil || !strings.Contains(strings.Join(differences, "\n"), "changed: run.sh") {
		t.Fatal("permission change missing from comparison", differences, err)
	}
	if err = os.Remove(filepath.Join(two, "run")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("SKILL.md", filepath.Join(two, "run")); err != nil {
		t.Fatal(err)
	}
	linkDigest, err := Digest(two)
	if err != nil || linkDigest == modeDigest {
		t.Fatal("symlink target change missing from digest", err)
	}
}
