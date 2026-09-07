// Package manager provides the shared, transactional skill-management service.
package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/faizmokh/skmr/internal/agents"
	"github.com/faizmokh/skmr/internal/skills"
)

func (s *Service) List() (skills.Result, error) {
	m, err := s.load()
	if err != nil {
		return skills.Result{}, err
	}
	out := skills.Scan(s.Roots)
	type scoped struct {
		m         Manifest
		inherited bool
		scope     string
	}
	sets := []scoped{{m, false, s.Scope()}}

	if s.Config.Project != "" {
		configs := []Config{{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome}}
		for _, ancestor := range s.ancestors {
			c := s.Config
			c.Project = ancestor
			configs = append(configs, c)
		}
		for _, config := range configs {
			inherited, err := New(config)
			if err != nil {
				return out, err
			}
			im, err := inherited.load()
			if err != nil {
				out.Issues = append(out.Issues, inherited.Store+": "+err.Error())
				continue
			}
			sets = append(sets, scoped{im, true, inherited.Scope()})
			if !absent(filepath.Join(inherited.Store, "journal.json")) {
				out.Issues = append(out.Issues, "Interrupted operation in inherited library: "+inherited.Store+"; run doctor --recover in its owning scope")
			}
		}
	}

	for _, set := range sets {
		for _, r := range set.m.Records {
			item := skills.Parse(r.Library)
			item.ID = r.ID
			item.Name = r.Name
			item.Managed = true
			item.Enabled = r.Enabled
			item.Scope = set.scope
			item.Inherited = set.inherited
			item.ReadOnly = set.inherited
			for _, p := range r.Links {
				if owned(p, r.Library) {
					for _, root := range s.Roots {
						if within(root.Path, p) {
							item.Agents = unique(append(item.Agents, root.Agents...))
						}
					}
				} else if !absent(p) {
					item.Issues = append(item.Issues, "Managed link path is occupied by unrelated content: "+p)
				} else if r.Enabled {
					item.Issues = append(item.Issues, "Missing managed link: "+p)
				}
			}
			if !r.Enabled && len(item.Agents) > 0 {
				item.Issues = append(item.Issues, "Disabled skill still has discovery links")
			}
			filtered := out.Skills[:0]
			for _, found := range out.Skills {
				managedLink := false
				for _, p := range r.Links {
					if found.Path == p && owned(p, r.Library) {
						managedLink = true
					}
				}
				if !managedLink {
					filtered = append(filtered, found)
				}
			}
			out.Skills = filtered
			out.Skills = append(out.Skills, item)
		}
	}
	counts := map[string]int{}
	for _, item := range out.Skills {
		counts[item.Name]++
	}
	for i := range out.Skills {
		if counts[out.Skills[i].Name] > 1 {
			out.Skills[i].Issues = append(out.Skills[i].Issues, "Duplicate name; another copy may remain discoverable")
		}
	}
	if !absent(filepath.Join(s.Store, "journal.json")) {
		out.Issues = append(out.Issues, "Interrupted operation: run doctor --recover before making changes")
	}
	sort.Slice(out.Skills, func(i, j int) bool {
		if out.Skills[i].Name == out.Skills[j].Name {
			return out.Skills[i].ID < out.Skills[j].ID
		}
		return out.Skills[i].Name < out.Skills[j].Name
	})
	return out, nil
}
func unique(xs []string) []string {
	sort.Strings(xs)
	out := []string{}
	for _, x := range xs {
		if len(out) == 0 || out[len(out)-1] != x {
			out = append(out, x)
		}
	}
	return out
}
func (s *Service) Show(id string) (skills.Skill, error) {
	result, err := s.List()
	if err != nil {
		return skills.Skill{}, err
	}
	for _, item := range result.Skills {
		if item.ID == id {
			return item, nil
		}
	}
	var matches []skills.Skill
	for _, item := range result.Skills {
		if item.Name == id {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return skills.Skill{}, fmt.Errorf("ambiguous skill name %q; use its ID", id)
	}
	return skills.Skill{}, fmt.Errorf("skill %q was not found", id)
}

func (s *Service) adoption(path string, m Manifest) (Record, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return Record{}, err
	}
	p = filepath.Clean(p)
	if err = realParents(p); err != nil {
		return Record{}, err
	}
	st, err := os.Lstat(p)
	if err != nil {
		return Record{}, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return Record{}, fmt.Errorf("adopt a real skill folder; existing links are read-only")
	}
	var match *agents.Root
	for i, root := range s.Roots {
		if within(root.Path, p) && p != root.Path {
			if match == nil || len(root.Path) > len(match.Path) {
				match = &s.Roots[i]
			}
		}
	}
	if match == nil || match.ReadOnly || match.Inherited {
		return Record{}, fmt.Errorf("skill is outside this scope's writable discovery directories")
	}
	rel, _ := filepath.Rel(match.Path, p)
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".system" || part == ".git" {
			return Record{}, fmt.Errorf("externally managed skills are read-only")
		}
	}
	for parent := filepath.Dir(p); parent != match.Path && within(match.Path, parent); parent = filepath.Dir(parent) {
		if !absent(filepath.Join(parent, "SKILL.md")) {
			return Record{}, fmt.Errorf("cannot adopt a folder inside another skill")
		}
	}
	item := skills.Parse(p)
	if len(item.Issues) > 0 {
		return Record{}, fmt.Errorf("cannot adopt: %s", strings.Join(item.Issues, "; "))
	}
	for _, r := range m.Records {
		if r.Name == item.Name || r.Original == p {
			return Record{}, fmt.Errorf("skill already managed: %s", r.ID)
		}
	}
	r := Record{ID: item.ID, Name: item.Name, Original: p, Library: filepath.Join(s.Store, "library", item.ID, item.Name), Enabled: true, Links: []string{p}}
	shared := filepath.Join(s.Shared(), item.Name)
	if p != shared {
		r.Links = append(r.Links, shared)
		if err = available(shared); err != nil {
			return Record{}, err
		}
	}
	if err = available(r.Library); err != nil {
		return Record{}, err
	}
	if err = sameDevice(p, r.Library); err != nil {
		return Record{}, err
	}
	// Rename preserves bytes and modes. Refuse relocation when relative links would
	// escape the package, or absolute internal links would break while disabled.
	err = filepath.WalkDir(p, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, e := os.Readlink(path)
		if e != nil {
			return e
		}
		if filepath.IsAbs(target) {
			if within(p, target) {
				return fmt.Errorf("use a relative internal link before adoption: %s", path)
			}
			return nil
		}
		if !within(p, filepath.Clean(filepath.Join(filepath.Dir(path), target))) {
			return fmt.Errorf("relative link escapes skill folder: %s", path)
		}
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return r, s.validate(r)
}
