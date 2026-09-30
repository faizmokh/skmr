package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/faizmokh/skmr/internal/skills"
)

// projectUpgrade records the complete handoff from a project-owned library to
// central storage. It remains on disk until both manifests and every link agree.
type projectUpgrade struct {
	Version        int             `json:"version"`
	ProjectBefore  Manifest        `json:"project_before"`
	GlobalBefore   Manifest        `json:"global_before"`
	PackagesBefore PackageManifest `json:"packages_before"`
	PackagesAfter  PackageManifest `json:"packages_after"`
	Entries        []upgradeEntry  `json:"entries"`
}

type upgradeEntry struct {
	Source      Record `json:"source"`
	Destination Record `json:"destination"`
	Digest      string `json:"digest"`
}

func (s *Service) upgradePath() string { return filepath.Join(s.Store, "upgrade-journal.json") }

// Upgrade converts the active project on first access. A journal from an
// interrupted upgrade is deliberately left for doctor --recover.
func (s *Service) Upgrade() error {
	if s.Config.Project == "" {
		return s.upgradePersonalLibrary()
	}
	if !absent(s.upgradePath()) {
		return fmt.Errorf("project upgrade was interrupted; run skmr doctor --recover --project %s", s.Config.Project)
	}
	if s.hasPending() {
		return nil
	}
	before, err := s.load()
	if err != nil {
		return err
	}
	if len(before.Records) == 0 {
		return s.upgradeGroupRequests()
	}
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return err
	}
	if s.hasPending() || global.hasPending() {
		return fmt.Errorf("recover interrupted operations before upgrading project %s", s.Config.Project)
	}
	plan, err := s.prepareProjectUpgrade(global)
	if err != nil {
		return err
	}
	op, err := s.operationFromUpgrade(plan)
	if err != nil {
		return err
	}
	return s.ApplyOperation(op)
}

func (s *Service) upgradePersonalLibrary() error {
	if s.hasPending() {
		return nil
	}
	groups, err := s.previewGroupUpgrade()
	if err != nil {
		return err
	}
	op := groups
	data, err := os.ReadFile(filepath.Join(s.Store, "manifest.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		var header struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(data, &header); err != nil {
			return err
		}
		m, err := s.load()
		if err != nil {
			return err
		}
		if header.Version == legacyVersion {
			library := operationPlan("upgrade-library", "Upgrade the personal library format")
			library.Scopes = []OperationScope{{LibraryBefore: &m, LibraryAfter: &m, RewriteLibrary: true}}
			if op.Version == 0 {
				op = library
			} else {
				op, err = mergeOperations(library, op)
				if err != nil {
					return err
				}
			}
		}
	}
	if op.Version == 0 {
		return nil
	}
	return s.ApplyOperation(op)
}

func (s *Service) operationFromUpgrade(p projectUpgrade) (OperationPlan, error) {
	op := operationPlan("upgrade", "Move project-owned skills into the personal library")
	projectAfter := Manifest{Version: Version, Records: []Record{}}
	globalAfter := cloneManifest(p.GlobalBefore)
	for _, entry := range p.Entries {
		globalAfter.Records = append(globalAfter.Records, entry.Destination)
		id, err := identity(filepath.Dir(entry.Source.Library))
		if err != nil {
			return op, err
		}
		op.Content = append(op.Content, ContentChange{Kind: "copy", From: filepath.Dir(entry.Source.Library), To: filepath.Dir(entry.Destination.Library), Digest: entry.Digest, Identity: id, RemoveSource: true})
		for _, path := range entry.Source.Links {
			op.RemoveLinks = append(op.RemoveLinks, LinkPlan{Path: path, Target: entry.Source.Library})
		}
	}
	op.Scopes = []OperationScope{
		{LibraryBefore: &p.GlobalBefore, LibraryAfter: &globalAfter},
		{Project: s.Config.Project, LibraryBefore: &p.ProjectBefore, LibraryAfter: &projectAfter, RequestsBefore: &p.PackagesBefore, RequestsAfter: &p.PackagesAfter},
	}
	for _, skill := range p.PackagesAfter.Skills {
		op.AddLinks = append(op.AddLinks, LinkPlan{Path: filepath.Join(s.Shared(), skill.Name), Target: s.centralLibrary(skill)})
	}
	return op, nil
}

func (s *Service) prepareProjectUpgrade(global *Service) (projectUpgrade, error) {
	var p projectUpgrade
	p.Version = Version
	var err error
	if p.ProjectBefore, err = s.load(); err != nil {
		return p, err
	}
	if p.GlobalBefore, err = global.load(); err != nil {
		return p, err
	}
	if p.PackagesBefore, err = s.loadPackages(); err != nil {
		return p, err
	}
	requests := legacyDirectRequests(p.PackagesBefore)
	byName := map[string]bool{}
	byID := map[string]bool{}
	for _, record := range p.GlobalBefore.Records {
		byName[record.Name] = true
		byID[record.ID] = true
	}
	for _, source := range p.ProjectBefore.Records {
		if byName[source.Name] || byID[source.ID] {
			return p, fmt.Errorf("cannot migrate %s: personal library already has that name or ID", source.Name)
		}
		byName[source.Name], byID[source.ID] = true, true
		if source.Remote != nil {
			return p, fmt.Errorf("cannot migrate %s: resolve duplicate copies or unsupported source first", source.Name)
		}
		for _, link := range source.Links {
			if !absent(link) && !owned(link, source.Library) {
				return p, fmt.Errorf("cannot migrate %s: discovery link was replaced: %s", source.Name, link)
			}
		}
		idDir := filepath.Join(global.Store, "library", source.ID)
		if !absent(idDir) {
			return p, fmt.Errorf("cannot migrate %s: destination is occupied: %s", source.Name, idDir)
		}
		if err = realParents(idDir); err != nil {
			return p, err
		}
		sourceDir := filepath.Dir(source.Library)
		if err = realParents(sourceDir); err != nil {
			return p, err
		}
		digest, digestErr := skills.Digest(sourceDir)
		if digestErr != nil {
			return p, digestErr
		}
		original := filepath.Join(global.Shared(), source.Name)
		destination := Record{ID: source.ID, Name: source.Name, Original: original,
			Library: filepath.Join(idDir, source.Name), Links: []string{original},
			Origins:      []Origin{{Path: original, Canonical: true}},
			MigratedFrom: &MigrationOrigin{Project: s.Config.Project, Path: source.Original}}
		for _, origin := range source.Origins {
			if origin.Backup != "" {
				rel, err := filepath.Rel(sourceDir, origin.Backup)
				if err != nil {
					return p, err
				}
				origin.Backup = filepath.Join(idDir, rel)
			}
			destination.MigratedFrom.Origins = append(destination.MigratedFrom.Origins, origin)
		}
		if err = global.validate(destination); err != nil {
			return p, err
		}
		p.Entries = append(p.Entries, upgradeEntry{Source: source, Destination: destination, Digest: digest})
		if source.Enabled {
			requests = append(requests, source.Name)
		}
	}
	additions := make([]Record, 0, len(p.Entries))
	for _, entry := range p.Entries {
		additions = append(additions, entry.Destination)
	}
	p.PackagesAfter, err = s.resolvePackages(uniqueStrings(requests), additions)
	if err != nil {
		return p, err
	}
	for _, installed := range p.PackagesAfter.Skills {
		path := filepath.Join(s.Shared(), installed.Name)
		if absent(path) || owned(path, s.centralLibrary(installed)) {
			continue
		}
		legacy := false
		for _, entry := range p.Entries {
			legacy = legacy || entry.Source.Name == installed.Name && owned(path, entry.Source.Library)
		}
		if !legacy {
			return p, fmt.Errorf("cannot migrate: project skill path is occupied: %s", path)
		}
	}
	return p, nil
}

func (s *Service) recoverProjectUpgrade() error {
	b, err := os.ReadFile(s.upgradePath())
	if err != nil {
		return err
	}
	var p projectUpgrade
	if err = json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("read upgrade journal: %w", err)
	}
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return err
	}
	unlock, err := lockServices(s, global)
	if err != nil {
		return err
	}
	defer unlock()
	return s.finishProjectUpgrade(global, p)
}

func (s *Service) finishProjectUpgrade(global *Service, p projectUpgrade) error {
	if p.Version != Version || p.ProjectBefore.Version != Version || p.GlobalBefore.Version != Version || len(p.Entries) == 0 {
		return fmt.Errorf("invalid project upgrade journal")
	}
	projectNow, err := s.load()
	if err != nil {
		return err
	}
	globalNow, err := global.load()
	if err != nil {
		return err
	}
	packagesNow, err := s.loadPackages()
	if err != nil {
		return err
	}
	projectAfter := Manifest{Version: Version, Records: []Record{}}
	globalAfter := cloneManifest(p.GlobalBefore)
	for _, entry := range p.Entries {
		if err = s.validate(entry.Source); err != nil {
			return err
		}
		if err = global.validate(entry.Destination); err != nil {
			return err
		}
		globalAfter.Records = append(globalAfter.Records, entry.Destination)
	}
	if !reflect.DeepEqual(projectNow, p.ProjectBefore) && !reflect.DeepEqual(projectNow, projectAfter) {
		return fmt.Errorf("project manifest changed during upgrade; recovery stopped")
	}
	if !reflect.DeepEqual(globalNow, p.GlobalBefore) && !reflect.DeepEqual(globalNow, globalAfter) {
		return fmt.Errorf("personal library changed during upgrade; recovery stopped")
	}
	if !reflect.DeepEqual(packagesNow, p.PackagesBefore) && !reflect.DeepEqual(packagesNow, p.PackagesAfter) {
		return fmt.Errorf("project placements changed during upgrade; recovery stopped")
	}
	for _, entry := range p.Entries {
		sourceDir := filepath.Dir(entry.Source.Library)
		destDir := filepath.Dir(entry.Destination.Library)
		if absent(destDir) {
			if err = verifyDigest(sourceDir, entry.Digest); err != nil {
				return fmt.Errorf("upgrade paused: %w", err)
			}
			if err = copyPackage(sourceDir, destDir, entry.Digest); err != nil {
				return err
			}
		} else if err = verifyDigest(destDir, entry.Digest); err != nil {
			return fmt.Errorf("upgrade destination changed: %w", err)
		}
	}
	for _, entry := range p.Entries {
		for _, link := range entry.Source.Links {
			if err = unlink(link, entry.Source.Library); err != nil {
				return err
			}
		}
	}
	for _, installed := range p.PackagesAfter.Skills {
		path := filepath.Join(s.Shared(), installed.Name)
		target := s.centralLibrary(installed)
		if !owned(path, target) {
			if err = s.link(path, target); err != nil {
				return err
			}
		}
	}
	if reflect.DeepEqual(globalNow, p.GlobalBefore) {
		if err = global.save(globalAfter); err != nil {
			return err
		}
	}
	if reflect.DeepEqual(packagesNow, p.PackagesBefore) {
		if err = atomicJSON(s.packageManifestPath(), p.PackagesAfter); err != nil {
			return err
		}
	}
	if reflect.DeepEqual(projectNow, p.ProjectBefore) {
		if err = s.save(projectAfter); err != nil {
			return err
		}
	}
	if err = s.registerPackages(p.PackagesAfter); err != nil {
		return err
	}
	for _, entry := range p.Entries {
		sourceDir := filepath.Dir(entry.Source.Library)
		if absent(sourceDir) {
			continue
		}
		if err = verifyDigest(sourceDir, entry.Digest); err != nil {
			return fmt.Errorf("old project copy changed; cleanup paused: %w", err)
		}
		if err = os.RemoveAll(sourceDir); err != nil {
			return err
		}
	}
	if err = os.Remove(s.upgradePath()); err != nil {
		return err
	}
	return syncDir(s.Store)
}
