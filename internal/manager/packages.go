package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

const packageManifestVersion = 1

var packageNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// PackageRef pins a project installation to one package in the central library.
type PackageRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PackageManifest records user requests separately from their resolved skills.
// Requests are skill names or @group names.
type PackageManifest struct {
	Version  int          `json:"version"`
	Requests []string     `json:"requests"`
	Skills   []PackageRef `json:"skills"`
}

// InstallGroup is a reusable set of skill names and nested @group references.
type InstallGroup struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type groupManifest struct {
	Version int            `json:"version"`
	Groups  []InstallGroup `json:"groups"`
}

// PackagePlan is a deterministic project-link reconciliation.
type PackagePlan struct {
	Version     int             `json:"version"`
	Action      string          `json:"action"`
	Arguments   []string        `json:"arguments,omitempty"`
	Before      PackageManifest `json:"before"`
	After       PackageManifest `json:"after"`
	AddLinks    []LinkPlan      `json:"add_links,omitempty"`
	RemoveLinks []LinkPlan      `json:"remove_links,omitempty"`
}

func (p PackagePlan) String() string {
	lines := []string{}
	for _, link := range p.RemoveLinks {
		lines = append(lines, "Remove owned link "+link.Path)
	}
	for _, link := range p.AddLinks {
		lines = append(lines, "Create link "+link.Path)
	}
	if len(lines) == 0 {
		return "No changes."
	}
	return strings.Join(lines, "\n")
}

func (s *Service) centralStore() string { return filepath.Join(s.Config.DataHome, "skmr") }

func (s *Service) packageManifestPath() string {
	return filepath.Join(s.Store, "packages.json")
}

func (s *Service) loadPackages() (PackageManifest, error) {
	m := PackageManifest{Version: packageManifestVersion, Requests: []string{}, Skills: []PackageRef{}}
	b, err := os.ReadFile(s.packageManifestPath())
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("read package manifest: %w", err)
	}
	if m.Version != packageManifestVersion {
		return m, fmt.Errorf("unsupported package manifest version %d", m.Version)
	}
	if err = validatePackageManifest(m); err != nil {
		return m, err
	}
	return m, nil
}

func validatePackageManifest(m PackageManifest) error {
	seenRequests := map[string]bool{}
	for _, request := range m.Requests {
		if err := validatePackageRequest(request); err != nil || seenRequests[request] {
			return fmt.Errorf("invalid package request %q", request)
		}
		seenRequests[request] = true
	}
	seenNames := map[string]bool{}
	for _, skill := range m.Skills {
		if !packageNamePattern.MatchString(skill.Name) || skill.ID == "" || filepath.Base(skill.ID) != skill.ID || seenNames[skill.Name] {
			return fmt.Errorf("invalid installed package %q", skill.Name)
		}
		seenNames[skill.Name] = true
	}
	return nil
}

func validatePackageRequest(value string) error {
	name := strings.TrimPrefix(value, "@")
	if !packageNamePattern.MatchString(name) {
		return fmt.Errorf("names must contain lowercase letters, numbers, and single hyphens")
	}
	return nil
}

func (s *Service) loadGroups() (groupManifest, error) {
	m := groupManifest{Version: packageManifestVersion, Groups: []InstallGroup{}}
	b, err := os.ReadFile(filepath.Join(s.centralStore(), "groups.json"))
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("read groups: %w", err)
	}
	if m.Version != packageManifestVersion {
		return m, fmt.Errorf("unsupported group manifest version %d", m.Version)
	}
	seen := map[string]bool{}
	for _, group := range m.Groups {
		if !packageNamePattern.MatchString(group.Name) || seen[group.Name] || len(group.Members) == 0 {
			return m, fmt.Errorf("invalid group %q", group.Name)
		}
		seen[group.Name] = true
		for _, member := range group.Members {
			if err = validatePackageRequest(member); err != nil {
				return m, fmt.Errorf("invalid member %q in group %q", member, group.Name)
			}
		}
	}
	return m, nil
}

// Groups returns the centrally stored install groups.
func (s *Service) Groups() ([]InstallGroup, error) {
	m, err := s.loadGroups()
	return m.Groups, err
}

// CreateGroup creates a named dependency bundle in the central store.
func (s *Service) CreateGroup(name string, members []string) (InstallGroup, error) {
	if !packageNamePattern.MatchString(name) {
		return InstallGroup{}, fmt.Errorf("invalid group name %q", name)
	}
	if len(members) == 0 {
		return InstallGroup{}, fmt.Errorf("provide at least one skill or @group member")
	}
	for _, member := range members {
		if err := validatePackageRequest(member); err != nil {
			return InstallGroup{}, fmt.Errorf("invalid group member %q: %w", member, err)
		}
	}
	unlock, err := s.globalLock()
	if err != nil {
		return InstallGroup{}, err
	}
	defer unlock()
	m, err := s.loadGroups()
	if err != nil {
		return InstallGroup{}, err
	}
	for _, group := range m.Groups {
		if group.Name == name {
			return InstallGroup{}, fmt.Errorf("group %q already exists", name)
		}
	}
	knownGroups := map[string]bool{}
	for _, group := range m.Groups {
		knownGroups[group.Name] = true
	}
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return InstallGroup{}, err
	}
	library, err := global.load()
	if err != nil {
		return InstallGroup{}, err
	}
	knownSkills := map[string]bool{}
	for _, record := range library.Records {
		knownSkills[record.Name] = true
	}
	for _, member := range members {
		if strings.HasPrefix(member, "@") {
			dependency := strings.TrimPrefix(member, "@")
			if dependency == name {
				return InstallGroup{}, fmt.Errorf("group %q cannot contain itself", name)
			}
			if !knownGroups[dependency] {
				return InstallGroup{}, fmt.Errorf("group %q was not found", dependency)
			}
		} else if !knownSkills[member] {
			return InstallGroup{}, fmt.Errorf("skill %q is not in the central library; adopt it first", member)
		}
	}
	group := InstallGroup{Name: name, Members: uniqueStrings(members)}
	m.Groups = append(m.Groups, group)
	sort.Slice(m.Groups, func(i, j int) bool { return m.Groups[i].Name < m.Groups[j].Name })
	if err = atomicJSON(filepath.Join(s.centralStore(), "groups.json"), m); err != nil {
		return InstallGroup{}, err
	}
	return group, nil
}

// DeleteGroup removes a group definition. Existing project manifests retain
// their resolved skill pins until their next sync.
func (s *Service) DeleteGroup(name string) error {
	unlock, err := s.globalLock()
	if err != nil {
		return err
	}
	defer unlock()
	m, err := s.loadGroups()
	if err != nil {
		return err
	}
	found := false
	groups := m.Groups[:0]
	for _, group := range m.Groups {
		if group.Name == name {
			found = true
			continue
		}
		groups = append(groups, group)
	}
	if !found {
		return fmt.Errorf("group %q was not found", name)
	}
	m.Groups = groups
	return atomicJSON(filepath.Join(s.centralStore(), "groups.json"), m)
}

func (s *Service) globalLock() (func(), error) {
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return nil, err
	}
	return global.lock()
}

// PreviewPackages resolves add, remove, or sync against the central library.
func (s *Service) PreviewPackages(action string, arguments []string) (PackagePlan, error) {
	if s.Config.Project == "" {
		return PackagePlan{}, fmt.Errorf("package installation requires a project")
	}
	if s.hasPending() {
		return PackagePlan{}, fmt.Errorf("an interrupted operation needs recovery; run doctor --recover")
	}
	before, err := s.loadPackages()
	if err != nil {
		return PackagePlan{}, err
	}
	requests := append([]string{}, before.Requests...)
	switch action {
	case "add":
		if len(arguments) == 0 {
			return PackagePlan{}, fmt.Errorf("provide at least one skill or @group")
		}
		for _, argument := range arguments {
			if err = validatePackageRequest(argument); err != nil {
				return PackagePlan{}, fmt.Errorf("invalid package request %q: %w", argument, err)
			}
			if !contains(requests, argument) {
				requests = append(requests, argument)
			}
		}
	case "remove":
		if len(arguments) == 0 {
			return PackagePlan{}, fmt.Errorf("provide at least one installed skill or @group")
		}
		for _, argument := range arguments {
			if !contains(requests, argument) {
				return PackagePlan{}, fmt.Errorf("%q is not a direct project request", argument)
			}
			requests = removeString(requests, argument)
		}
	case "sync":
		if len(arguments) != 0 {
			return PackagePlan{}, fmt.Errorf("sync takes no package arguments")
		}
	default:
		return PackagePlan{}, fmt.Errorf("unknown package action %q", action)
	}
	after, err := s.resolvePackages(requests)
	if err != nil {
		return PackagePlan{}, err
	}
	plan := PackagePlan{Version: packageManifestVersion, Action: action, Arguments: append([]string{}, arguments...), Before: before, After: after}
	old := map[string]PackageRef{}
	for _, skill := range before.Skills {
		old[skill.Name] = skill
	}
	newSkills := map[string]PackageRef{}
	for _, skill := range after.Skills {
		newSkills[skill.Name] = skill
	}
	for name, skill := range old {
		if next, ok := newSkills[name]; !ok || next.ID != skill.ID {
			plan.RemoveLinks = append(plan.RemoveLinks, LinkPlan{Path: filepath.Join(s.Shared(), name), Target: s.centralLibrary(skill)})
		}
	}
	for name, skill := range newSkills {
		path := filepath.Join(s.Shared(), name)
		target := s.centralLibrary(skill)
		previous, existed := old[name]
		if !existed || previous.ID != skill.ID || !owned(path, target) {
			plan.AddLinks = append(plan.AddLinks, LinkPlan{Path: path, Target: target})
		}
	}
	sort.Slice(plan.RemoveLinks, func(i, j int) bool { return plan.RemoveLinks[i].Path < plan.RemoveLinks[j].Path })
	sort.Slice(plan.AddLinks, func(i, j int) bool { return plan.AddLinks[i].Path < plan.AddLinks[j].Path })
	if err = validatePackageLinks(plan); err != nil {
		return PackagePlan{}, err
	}
	return plan, nil
}

func (s *Service) resolvePackages(requests []string) (PackageManifest, error) {
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return PackageManifest{}, err
	}
	library, err := global.load()
	if err != nil {
		return PackageManifest{}, err
	}
	byName := map[string]Record{}
	for _, record := range library.Records {
		byName[record.Name] = record
	}
	groupsManifest, err := s.loadGroups()
	if err != nil {
		return PackageManifest{}, err
	}
	groups := map[string]InstallGroup{}
	for _, group := range groupsManifest.Groups {
		groups[group.Name] = group
	}
	resolved := map[string]PackageRef{}
	visiting := map[string]bool{}
	var resolve func(string) error
	resolve = func(request string) error {
		if strings.HasPrefix(request, "@") {
			name := strings.TrimPrefix(request, "@")
			group, ok := groups[name]
			if !ok {
				return fmt.Errorf("group %q was not found", name)
			}
			if visiting[name] {
				return fmt.Errorf("group dependency cycle at %q", name)
			}
			visiting[name] = true
			for _, member := range group.Members {
				if err := resolve(member); err != nil {
					return fmt.Errorf("group %q: %w", name, err)
				}
			}
			delete(visiting, name)
			return nil
		}
		record, ok := byName[request]
		if !ok {
			return fmt.Errorf("skill %q is not in the central library; adopt it first", request)
		}
		info, statErr := os.Lstat(record.Library)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("central package is unavailable: %s", record.Library)
		}
		resolved[record.Name] = PackageRef{ID: record.ID, Name: record.Name}
		return nil
	}
	for _, request := range requests {
		if err = resolve(request); err != nil {
			return PackageManifest{}, err
		}
	}
	result := PackageManifest{Version: packageManifestVersion, Requests: uniqueStrings(requests), Skills: make([]PackageRef, 0, len(resolved))}
	for _, skill := range resolved {
		result.Skills = append(result.Skills, skill)
	}
	sort.Slice(result.Skills, func(i, j int) bool { return result.Skills[i].Name < result.Skills[j].Name })
	return result, nil
}

func (s *Service) centralLibrary(skill PackageRef) string {
	return filepath.Join(s.centralStore(), "library", skill.ID, skill.Name)
}

func validatePackageLinks(plan PackagePlan) error {
	removing := map[string]string{}
	for _, link := range plan.RemoveLinks {
		removing[link.Path] = link.Target
		if !absent(link.Path) && !owned(link.Path, link.Target) {
			return fmt.Errorf("project skill path is occupied by unrelated content: %s", link.Path)
		}
	}
	for _, link := range plan.AddLinks {
		if absent(link.Path) || owned(link.Path, link.Target) {
			continue
		}
		if target, ok := removing[link.Path]; !ok || !owned(link.Path, target) {
			return fmt.Errorf("project skill path is occupied by unrelated content: %s", link.Path)
		}
	}
	return nil
}

// ApplyPackages journals the complete project reconciliation before changing links.
func (s *Service) ApplyPackages(plan PackagePlan) error {
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return err
	}
	unlock, err := lockServices(s, global)
	if err != nil {
		return err
	}
	defer unlock()
	fresh, err := s.PreviewPackages(plan.Action, plan.Arguments)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh, plan) {
		return fmt.Errorf("package state changed since preview; review a fresh plan")
	}
	if err = atomicJSON(filepath.Join(s.Store, "packages-journal.json"), plan); err != nil {
		return err
	}
	return s.finishPackages(plan)
}

func (s *Service) finishPackages(plan PackagePlan) error {
	if plan.Version != packageManifestVersion || plan.Before.Version != packageManifestVersion || plan.After.Version != packageManifestVersion {
		return fmt.Errorf("unsupported package recovery journal")
	}
	if plan.Action != "add" && plan.Action != "remove" && plan.Action != "sync" {
		return fmt.Errorf("unknown package recovery action %q", plan.Action)
	}
	if err := validatePackageManifest(plan.Before); err != nil {
		return err
	}
	if err := validatePackageManifest(plan.After); err != nil {
		return err
	}
	current, err := s.loadPackages()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, plan.Before) && !reflect.DeepEqual(current, plan.After) {
		return fmt.Errorf("package manifest changed outside the interrupted operation; recovery stopped")
	}
	before := map[string]PackageRef{}
	for _, skill := range plan.Before.Skills {
		before[skill.Name] = skill
	}
	after := map[string]PackageRef{}
	for _, skill := range plan.After.Skills {
		after[skill.Name] = skill
	}
	for name, skill := range before {
		desired, retained := after[name]
		if retained && desired.ID == skill.ID {
			continue
		}
		path := filepath.Join(s.Shared(), name)
		target := s.centralLibrary(skill)
		if absent(path) || retained && owned(path, s.centralLibrary(desired)) {
			continue
		}
		if err = unlink(path, target); err != nil {
			return fmt.Errorf("package operation paused: %w; resolve the conflict, then run doctor --recover", err)
		}
	}
	for name, skill := range after {
		path := filepath.Join(s.Shared(), name)
		target := s.centralLibrary(skill)
		if owned(path, target) {
			continue
		}
		info, statErr := os.Lstat(target)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("package operation paused: central package is unavailable: %s; restore it, then run doctor --recover", target)
		}
		if err = s.link(path, target); err != nil {
			return fmt.Errorf("package operation paused: %w; resolve the conflict, then run doctor --recover", err)
		}
	}
	if reflect.DeepEqual(current, plan.Before) {
		if err = atomicJSON(s.packageManifestPath(), plan.After); err != nil {
			return fmt.Errorf("package manifest save interrupted: %w; run doctor --recover", err)
		}
	}
	if err = os.Remove(filepath.Join(s.Store, "packages-journal.json")); err != nil {
		return err
	}
	return syncDir(s.Store)
}

func (s *Service) recoverPackages() error {
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return err
	}
	unlock, err := lockServices(s, global)
	if err != nil {
		return err
	}
	defer unlock()
	b, err := os.ReadFile(filepath.Join(s.Store, "packages-journal.json"))
	if err != nil {
		return err
	}
	var plan PackagePlan
	if err = json.Unmarshal(b, &plan); err != nil {
		return fmt.Errorf("read package recovery journal: %w", err)
	}
	return s.finishPackages(plan)
}

func contains(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func removeString(values []string, value string) []string {
	out := make([]string, 0, len(values))
	for _, current := range values {
		if current != value {
			out = append(out, current)
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
