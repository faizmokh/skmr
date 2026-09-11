package skills

import (
	"github.com/faizmokh/skmr/internal/agents"
	"path/filepath"
	"testing"
)

func TestGroupDiscoveryIdentity(t *testing.T) {
	home := t.TempDir()
	roots := agents.Global(home, filepath.Join(home, ".config"))
	roots = append(roots, agents.Project(filepath.Join(home, "project"), false)...)
	paths := []string{
		filepath.Join(roots[0].Path, "standalone"),
		filepath.Join(roots[0].Path, "nested", "folder", "alpha"),
		filepath.Join(roots[1].Path, "nested", "folder", "beta"),
		filepath.Join(roots[6].Path, "nested", "folder", "gamma"),
		filepath.Join(roots[5].Path, "vendor", "sheets", "1.0", "skills", "alpha"),
		filepath.Join(roots[5].Path, "vendor", "sheets", "1.0", "skills", "beta"),
		filepath.Join(roots[5].Path, "vendor", "sheets", "2.0", "skills", "alpha"),
		filepath.Join(roots[5].Path, "other", "sheets", "1.0", "skills", "alpha"),
	}
	for _, path := range paths {
		write(t, path, "---\nname: "+filepath.Base(path)+"\ndescription: Test\n---\n")
	}
	result := Scan(roots)
	byPath := map[string]Skill{}
	for _, s := range result.Skills {
		byPath[s.Path] = s
	}
	if byPath[paths[0]].Group != nil {
		t.Fatal("standalone grouped")
	}
	first := byPath[paths[1]].Group
	if first == nil || first.Name != "nested/folder" {
		t.Fatalf("nested group: %+v", first)
	}
	for _, i := range []int{2, 3} {
		if byPath[paths[i]].Group.ID == first.ID {
			t.Fatal("roots or scopes merged")
		}
	}
	plugin := byPath[paths[4]].Group
	if plugin.Name != "sheets" || plugin.Version != "1.0" || plugin.Marketplace != "vendor" {
		t.Fatalf("plugin: %+v", plugin)
	}
	if byPath[paths[5]].Group.ID != plugin.ID {
		t.Fatal("plugin children separated")
	}
	for _, i := range []int{6, 7} {
		if byPath[paths[i]].Group.ID == plugin.ID {
			t.Fatal("installations merged")
		}
	}
}
