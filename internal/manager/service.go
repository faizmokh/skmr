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
	if s.Config.Project != "" {
		projects := append([]string{s.Config.Project}, s.ancestors...)
		for i := range out.Skills {
			if out.Skills[i].Scope != "project" {
				continue
			}
			for _, project := range projects {
				if within(project, out.Skills[i].Path) {
					out.Skills[i].OwnerProject = project
					break
				}
			}
		}
	}
	type scoped struct {
		m         Manifest
		inherited bool
		scope     string
		project   string
	}
	sets := []scoped{{m, false, s.Scope(), s.Config.Project}}

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
			sets = append(sets, scoped{im, true, inherited.Scope(), inherited.Config.Project})
			if inherited.hasPending() {
				out.Issues = append(out.Issues, "Interrupted operation in inherited library: "+inherited.Store+"; run doctor --recover in its owning scope")
			}
		}
	}

	for _, set := range sets {
		for _, r := range set.m.Records {
			item := skills.Parse(r.Library)
			item.Group = skills.GroupFor(r.Original, s.Roots)
			item.ID = r.ID
			item.Name = r.Name
			item.Managed = true
			item.Enabled = r.Enabled
			item.Scope = set.scope
			item.OwnerProject = set.project
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
			for _, origin := range r.Origins {
				isLegacyLink := false
				for _, link := range r.Links {
					isLegacyLink = isLegacyLink || origin.Path == link && owned(link, r.Library)
				}
				if !isLegacyLink && !absent(origin.Path) {
					item.Issues = append(item.Issues, "Restore path is occupied by unrelated content: "+origin.Path)
				}
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
					if found.Path == r.Original && found.ID == r.ID {
						found.ID = skills.ID(found.Path + "#unmanaged")
					}
					filtered = append(filtered, found)
				}
			}
			out.Skills = filtered
			out.Skills = append(out.Skills, item)
		}
	}
	annotateConflicts(&out, sets[0].m)
	if s.hasPending() {
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

func annotateConflicts(out *skills.Result, current Manifest) {
	out.Snapshots = map[string]*skills.PackageSnapshot{}
	groups := map[string][]int{}
	for i := range out.Skills {
		groups[out.Skills[i].Name] = append(groups[out.Skills[i].Name], i)
	}
	for name, indexes := range groups {
		legacyLinks := 0
		for _, record := range current.Records {
			if record.Name != name {
				continue
			}
			for _, link := range record.Links {
				if owned(link, record.Library) {
					legacyLinks++
				}
			}
		}
		count := len(indexes)
		if count < 2 && legacyLinks < 2 {
			continue
		}
		kind := "identical"
		metadataDigests := map[string]bool{}
		unresolved := []string{}
		for _, index := range indexes {
			item := &out.Skills[index]
			if item.ReadOnly || item.Inherited {
				kind = "external"
				unresolved = append(unresolved, item.Path)
			}
			snapshot, err := skills.Snapshot(item.Path)
			if err != nil {
				kind = "external"
				unresolved = append(unresolved, item.Path)
				continue
			}
			out.Snapshots[item.Path] = snapshot
			metadataDigests[snapshot.MetadataDigest()] = true
		}
		if kind != "external" {
			if len(metadataDigests) > 1 {
				kind = "divergent"
			} else {
				digests := map[string]bool{}
				for _, index := range indexes {
					digest, err := out.Snapshots[out.Skills[index].Path].Digest()
					if err != nil {
						kind = "external"
						unresolved = append(unresolved, out.Skills[index].Path)
						break
					}
					digests[digest] = true
				}
				if kind != "external" && len(digests) > 1 {
					kind = "divergent"
				}
			}
		}
		if kind == "divergent" {
			contentDigests := map[string]bool{}
			for _, index := range indexes {
				path := out.Skills[index].Path
				digest, err := out.Snapshots[path].ContentDigest()
				if err != nil {
					kind = "external"
					unresolved = append(unresolved, path)
					break
				}
				contentDigests[digest] = true
			}
			if kind != "external" && len(contentDigests) == 1 {
				kind = "agent_config"
			}
		}
		visibleCount := max(count, legacyLinks)
		groupID := skills.ID("conflict:" + name)
		for _, index := range indexes {
			item := &out.Skills[index]
			item.ConflictID = groupID
			item.ConflictKind = kind
			item.ConflictCount = visibleCount
			item.Canonical = item.Managed
			item.Unresolved = unique(unresolved)
			label := skills.ConflictLabel(kind)
			message := fmt.Sprintf("%s%s found in %d locations", strings.ToUpper(label[:1]), label[1:], visibleCount)
			if legacyLinks >= 2 && count == 1 {
				message = "Same copies found; adopt the copy you want to keep"
			}
			item.Issues = append(item.Issues, message)
		}
	}
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
		return Record{}, fmt.Errorf("adopt a real skill folder; linked skills are view only")
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
			return Record{}, fmt.Errorf("externally managed skills are view only")
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
	for _, discovered := range skills.Scan(s.Roots).Skills {
		if discovered.Name == item.Name && discovered.Path != p {
			return Record{}, fmt.Errorf("duplicate skill name; adopt the copy you want to keep")
		}
	}
	for _, r := range m.Records {
		if r.Name == item.Name || r.Original == p {
			return Record{}, fmt.Errorf("skill already managed: %s", r.ID)
		}
	}
	r := Record{ID: item.ID, Name: item.Name, Original: p, Library: filepath.Join(s.Store, "library", item.ID, item.Name), Enabled: true, Origins: []Origin{{Path: p, Canonical: true}}}
	shared := filepath.Join(s.Shared(), item.Name)
	r.Links = []string{shared}
	if err = available(shared); err != nil && shared != p {
		return Record{}, err
	}
	if err = available(r.Library); err != nil {
		return Record{}, err
	}
	if err = sameDevice(p, r.Library); err != nil {
		return Record{}, err
	}
	if err = validateRelocation(p); err != nil {
		return Record{}, err
	}
	return r, s.validate(r)
}

func validateRelocation(path string) error {
	// Rename preserves bytes and modes. Refuse relocation when relative links would
	// escape the package, or absolute internal links would break while disabled.
	return filepath.WalkDir(path, func(current string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, e := os.Readlink(current)
		if e != nil {
			return e
		}
		if filepath.IsAbs(target) {
			if within(path, target) {
				return fmt.Errorf("use a relative internal link before relocation: %s", current)
			}
			return nil
		}
		if !within(path, filepath.Clean(filepath.Join(filepath.Dir(current), target))) {
			return fmt.Errorf("relative link escapes skill folder: %s", current)
		}
		return nil
	})
}

func (s *Service) resolution(id string, manifest Manifest) (Plan, error) {
	result, err := s.List()
	if err != nil {
		return Plan{}, err
	}
	return s.resolutionFromResult(id, manifest, result)
}

func (s *Service) resolutionFromResult(id string, manifest Manifest, result skills.Result) (Plan, error) {
	var err error
	var selected *skills.Skill
	for i := range result.Skills {
		if result.Skills[i].ID == id {
			selected = &result.Skills[i]
			break
		}
	}
	if selected == nil {
		return Plan{}, fmt.Errorf("skill %q was not found; resolve requires an ID", id)
	}
	if selected.ReadOnly || selected.Inherited {
		return Plan{}, fmt.Errorf("the selected copy is view only in this scope")
	}
	if selected.ConflictID == "" {
		return Plan{}, fmt.Errorf("skill %q has no duplicate copies", selected.Name)
	}
	if !selected.Managed {
		parsed := skills.Parse(selected.Path)
		if len(parsed.Issues) > 0 {
			return Plan{}, fmt.Errorf("the selected copy is invalid: %s", strings.Join(parsed.Issues, "; "))
		}
		if err = validateRelocation(selected.Path); err != nil {
			return Plan{}, err
		}
	}

	plan := Plan{Version: Version, Action: "resolve", Before: manifest}
	for _, item := range result.Skills {
		if item.Name != selected.Name || item.ID == selected.ID || item.ReadOnly || item.Inherited {
			continue
		}
		var differences []string
		left, leftOK := result.Snapshots[selected.Path]
		right, rightOK := result.Snapshots[item.Path]
		var compareErr error
		if leftOK && rightOK {
			differences, compareErr = skills.SnapshotDifferences(left, right)
		} else {
			differences, compareErr = skills.Differences(selected.Path, item.Path)
		}
		if compareErr != nil {
			return Plan{}, compareErr
		}
		const comparisonLimit = 200
		if len(differences) > comparisonLimit {
			remainder := len(differences) - comparisonLimit
			differences = append(differences[:comparisonLimit], fmt.Sprintf("... %d more differences", remainder))
		}
		plan.Comparisons = append(plan.Comparisons, Comparison{Path: item.Path, Differences: differences})
	}
	var existing *Record
	for i := range manifest.Records {
		if manifest.Records[i].Name == selected.Name {
			copy := manifest.Records[i]
			existing = &copy
			break
		}
	}

	shared := filepath.Join(s.Shared(), selected.Name)
	if selected.Managed {
		if existing == nil || existing.ID != selected.ID {
			return Plan{}, fmt.Errorf("managed copy record is missing")
		}
		plan.Record = *existing
		plan.Record.Origins = append([]Origin{}, existing.Origins...)
	} else {
		plan.Record = Record{
			ID:       selected.ID,
			Name:     selected.Name,
			Original: selected.Path,
			Library:  filepath.Join(s.Store, "library", selected.ID, selected.Name),
			Enabled:  true,
			Origins:  []Origin{{Path: selected.Path, Canonical: true}},
		}
		if err = available(plan.Record.Library); err != nil {
			return Plan{}, err
		}
		if err = sameDevice(selected.Path, plan.Record.Library); err != nil {
			return Plan{}, err
		}
		selectedMove, moveErr := newMovePlan(selected.Path, plan.Record.Library)
		if moveErr != nil {
			return Plan{}, moveErr
		}
		plan.Identity = selectedMove.Identity
		plan.Moves = append(plan.Moves, selectedMove)
		if existing != nil {
			for _, link := range existing.Links {
				plan.RemoveLinks = append(plan.RemoveLinks, LinkPlan{Path: link, Target: existing.Library})
			}
			for _, origin := range existing.Origins {
				if origin.Path == selected.Path {
					return Plan{}, fmt.Errorf("duplicate occupies a reserved restore path; move it before resolving: %s", selected.Path)
				}
				source := origin.Backup
				if origin.Canonical {
					source = existing.Library
				}
				backup := filepath.Join(s.Store, "library", plan.Record.ID, ".skmr-duplicates", skills.ID(origin.Path), existing.Name)
				if err = available(backup); err != nil {
					return Plan{}, err
				}
				if err = sameDevice(source, backup); err != nil {
					return Plan{}, err
				}
				oldMove, moveErr := newMovePlan(source, backup)
				if moveErr != nil {
					return Plan{}, moveErr
				}
				plan.Moves = append(plan.Moves, oldMove)
				origin.Canonical = false
				origin.Backup = backup
				plan.Record.Origins = append(plan.Record.Origins, origin)
			}
		}
	}

	knownOrigins := map[string]bool{}
	for _, origin := range plan.Record.Origins {
		knownOrigins[origin.Path] = true
	}
	for _, item := range result.Skills {
		if item.Name != selected.Name || item.ID == selected.ID || item.Managed || item.ReadOnly || item.Inherited {
			continue
		}
		if knownOrigins[item.Path] {
			return Plan{}, fmt.Errorf("duplicate occupies a reserved restore path; move it before resolving: %s", item.Path)
		}
		stat, statErr := os.Lstat(item.Path)
		if statErr != nil {
			return Plan{}, statErr
		}
		if !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
			continue
		}
		backup := filepath.Join(s.Store, "library", plan.Record.ID, ".skmr-duplicates", item.ID, item.Name)
		if err = available(backup); err != nil {
			return Plan{}, err
		}
		if err = sameDevice(item.Path, backup); err != nil {
			return Plan{}, err
		}
		duplicateMove, moveErr := newMovePlan(item.Path, backup)
		if moveErr != nil {
			return Plan{}, moveErr
		}
		plan.Record.Origins = append(plan.Record.Origins, Origin{Path: item.Path, Backup: backup})
		plan.Moves = append(plan.Moves, duplicateMove)
		knownOrigins[item.Path] = true
	}

	if selected.Managed && existing != nil {
		for _, link := range existing.Links {
			if link != shared {
				plan.RemoveLinks = append(plan.RemoveLinks, LinkPlan{Path: link, Target: existing.Library})
			}
		}
	}
	plan.Record.Links = []string{shared}
	plan.Record.Enabled = true
	if selected.Managed && len(plan.Moves) == 0 && len(plan.RemoveLinks) == 0 {
		return Plan{}, fmt.Errorf("only view-only copies or copies from other scopes remain; skmr cannot move them")
	}

	sharedWillMove := false
	for _, item := range plan.Moves {
		sharedWillMove = sharedWillMove || item.From == shared
	}
	sharedOwned := existing != nil && owned(shared, existing.Library)
	if !absent(shared) && !sharedWillMove && !sharedOwned && !owned(shared, plan.Record.Library) {
		return Plan{}, fmt.Errorf("destination already exists: %s", shared)
	}
	if err = s.validate(plan.Record); err != nil {
		return Plan{}, err
	}
	return plan, nil
}
