// Package agents describes the standard skill discovery directories.
package agents

import "path/filepath"

type Root struct {
	Path      string
	Agents    []string
	Scope     string
	Inherited bool
	ReadOnly  bool
}

var All = []string{"codex", "opencode", "pi"}

func Global(home, config string) []Root {
	return []Root{
		{filepath.Join(home, ".agents", "skills"), All, "global", false, false},
		{filepath.Join(home, ".codex", "skills"), []string{"codex"}, "global", false, false},
		{filepath.Join(config, "opencode", "skills"), []string{"opencode"}, "global", false, false},
		{filepath.Join(home, ".pi", "agent", "skills"), []string{"pi"}, "global", false, false},
		{filepath.Join(home, ".claude", "skills"), []string{"opencode"}, "global", false, false},
		{filepath.Join(home, ".codex", "plugins", "cache"), []string{"codex"}, "global", false, true},
	}
}

func Project(path string, inherited bool) []Root {
	return []Root{
		{filepath.Join(path, ".agents", "skills"), All, "project", inherited, false},
		{filepath.Join(path, ".opencode", "skills"), []string{"opencode"}, "project", inherited, false},
		{filepath.Join(path, ".pi", "skills"), []string{"pi"}, "project", inherited, false},
		{filepath.Join(path, ".codex", "skills"), []string{"codex"}, "project", inherited, false},
		{filepath.Join(path, ".claude", "skills"), []string{"opencode"}, "project", inherited, false},
	}
}
