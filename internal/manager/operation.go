package manager

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/faizmokh/skmr/internal/skills"
)

// OperationPlan is the reviewed state transition shared by the CLI, TUI and
// recovery. A single personal-library journal owns every affected scope.
type OperationPlan struct {
	Version     int              `json:"version"`
	Action      string           `json:"action"`
	Scopes      []OperationScope `json:"scopes"`
	Content     []ContentChange  `json:"content,omitempty"`
	RemoveLinks []LinkPlan       `json:"remove_links,omitempty"`
	AddLinks    []LinkPlan       `json:"add_links,omitempty"`
	Description string           `json:"description"`
	Stage       string           `json:"stage,omitempty"`
	StageID     Identity         `json:"stage_id"`
	Ready       bool             `json:"ready,omitempty"`
	Cleanup     bool             `json:"cleanup,omitempty"`
	Temporary   []string         `json:"-"`
}

type OperationScope struct {
	Project        string           `json:"project,omitempty"`
	LibraryBefore  *Manifest        `json:"library_before,omitempty"`
	LibraryAfter   *Manifest        `json:"library_after,omitempty"`
	RequestsBefore *PackageManifest `json:"requests_before,omitempty"`
	RequestsAfter  *PackageManifest `json:"requests_after,omitempty"`
	GroupsBefore   *groupManifest   `json:"groups_before,omitempty"`
	GroupsAfter    *groupManifest   `json:"groups_after,omitempty"`
	RewriteLibrary bool             `json:"rewrite_library,omitempty"`
}

// ContentChange has an exclusive destination. Copy and update payloads are
// made durable before the journal is committed; old content survives until all
// manifests have committed. Prepared and Backup are populated only by apply.
type ContentChange struct {
	Kind         string   `json:"kind"`
	From         string   `json:"from,omitempty"`
	To           string   `json:"to,omitempty"`
	Digest       string   `json:"digest"`
	OldDigest    string   `json:"old_digest,omitempty"`
	Identity     Identity `json:"identity"`
	RemoveSource bool     `json:"remove_source,omitempty"`
	Prepared     string   `json:"prepared,omitempty"`
	PreparedID   Identity `json:"prepared_id"`
	Backup       string   `json:"backup,omitempty"`
}

func (p OperationPlan) String() string { return p.Description }

// Discard releases download previews. Applied operations retain their separate
// durable payloads until commit or recovery completes.
func (p OperationPlan) Discard() {
	for _, path := range p.Temporary {
		os.RemoveAll(path)
	}
}

func (s *Service) operationPath() string {
	return filepath.Join(s.centralStore(), "operation.json")
}

func (s *Service) operationService(project string) (*Service, error) {
	return New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome, Project: project})
}

func (s *Service) lockOperation(p OperationPlan) (func(), error) {
	byStore := map[string]*Service{}
	global, err := s.operationService("")
	if err != nil {
		return nil, err
	}
	byStore[global.Store] = global
	for _, scope := range p.Scopes {
		service, err := s.operationService(scope.Project)
		if err != nil {
			return nil, err
		}
		byStore[service.Store] = service
	}
	stores := make([]string, 0, len(byStore))
	for path := range byStore {
		stores = append(stores, path)
	}
	sort.Strings(stores)
	var unlocks []func()
	unlock := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	for _, path := range stores {
		release, err := byStore[path].lock()
		if err != nil {
			unlock()
			return nil, err
		}
		unlocks = append(unlocks, release)
	}
	return unlock, nil
}

// ApplyOperation validates the complete preview before touching content or
// discovery links. Older journals are read by Recover but never written here.
func (s *Service) ApplyOperation(p OperationPlan) error {
	unlock, err := s.lockOperation(p)
	if err != nil {
		return err
	}
	defer unlock()
	for _, scope := range p.Scopes {
		service, err := s.operationService(scope.Project)
		if err != nil {
			return err
		}
		if service.hasPending() {
			return fmt.Errorf("recover the interrupted operation before applying changes")
		}
	}
	if err = s.validateOperation(p, false); err != nil {
		return err
	}
	if err = s.checkOperationState(p, false); err != nil {
		return err
	}
	if err = s.verifyNewNames(p); err != nil {
		return err
	}
	if err = s.verifyRemovals(p); err != nil {
		return err
	}
	if err = preflightOperation(p); err != nil {
		return err
	}
	if err = s.prepareOperation(&p); err != nil {
		if p.Stage != "" && !absent(s.operationPath()) {
			if recoveryErr := s.abortOperationPreparation(p); recoveryErr != nil {
				return fmt.Errorf("preparation interrupted: %w; recovery stopped: %v; run doctor --recover", err, recoveryErr)
			}
		} else if p.Stage != "" {
			if cleanupErr := s.removeOperationStage(p); cleanupErr != nil {
				return fmt.Errorf("preparation failed: %w; remove the abandoned staging conflict: %v", err, cleanupErr)
			}
		}
		return err
	}
	return s.finishOperation(p)
}

func (s *Service) recoverOperation() error {
	data, err := os.ReadFile(s.operationPath())
	if err != nil {
		return err
	}
	var p OperationPlan
	if err = json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("read operation journal: %w", err)
	}
	unlock, err := s.lockOperation(p)
	if err != nil {
		return err
	}
	defer unlock()
	if err = s.validateOperation(p, true); err != nil {
		return err
	}
	if !p.Ready {
		return s.abortOperationPreparation(p)
	}
	return s.finishOperation(p)
}

func (s *Service) abortOperationPreparation(p OperationPlan) error {
	if err := s.checkOperationState(p, false); err != nil {
		return err
	}
	if err := s.removeOperationStage(p); err != nil {
		return err
	}
	if err := os.Remove(s.operationPath()); err != nil {
		return err
	}
	return syncDir(s.centralStore())
}

func (s *Service) removeOperationStage(p OperationPlan) error {
	for _, c := range p.Content {
		if c.Prepared == "" || absent(c.Prepared) {
			continue
		}
		if err := realParents(c.Prepared); err != nil {
			return err
		}
		id, err := identity(c.Prepared)
		if err != nil || id != c.PreparedID {
			return fmt.Errorf("prepared payload was replaced: %s", c.Prepared)
		}
		if err = os.RemoveAll(c.Prepared); err != nil {
			return err
		}
	}
	if absent(p.Stage) {
		return nil
	}
	if err := realParents(p.Stage); err != nil {
		return err
	}
	id, err := identity(p.Stage)
	if err != nil || id != p.StageID {
		return fmt.Errorf("operation staging directory was replaced: %s", p.Stage)
	}
	return os.RemoveAll(p.Stage)
}

func (s *Service) prepareOperation(p *OperationPlan) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	p.Stage = filepath.Join(s.centralStore(), "operations", hex.EncodeToString(nonce[:]))
	if err := mkdir(p.Stage); err != nil {
		return err
	}
	stageID, err := identity(p.Stage)
	if err != nil {
		return err
	}
	p.StageID = stageID
	for i := range p.Content {
		c := &p.Content[i]
		if c.Kind == "move" && sameDevice(c.From, c.To) != nil {
			c.Kind, c.OldDigest = "relocate", c.Digest
		}
		if c.Kind == "copy" || c.Kind == "update" {
			c.Prepared = filepath.Join(p.Stage, fmt.Sprintf("payload-%d", i))
		}
		if c.Kind == "relocate" {
			c.Prepared = filepath.Join(filepath.Dir(c.To), ".skmr-payload-"+filepath.Base(p.Stage)+fmt.Sprintf("-%d", i))
		}
		if c.Prepared != "" {
			if err := mkdir(filepath.Dir(c.Prepared)); err != nil {
				return err
			}
			if err := os.Mkdir(c.Prepared, 0700); err != nil {
				return err
			}
			id, err := identity(c.Prepared)
			if err != nil {
				return err
			}
			c.PreparedID = id
			if err := syncDir(filepath.Dir(c.Prepared)); err != nil {
				return err
			}
		}
		if c.Kind == "delete" || c.Kind == "update" || c.Kind == "relocate" {
			path := c.From
			if c.Kind == "update" {
				path = c.To
			}
			c.Backup = filepath.Join(filepath.Dir(path), ".skmr-old-"+filepath.Base(p.Stage)+fmt.Sprintf("-%d", i))
			if err := available(c.Backup); err != nil {
				return err
			}
		}
	}
	if err := atomicJSON(s.operationPath(), p); err != nil {
		s.removeOperationStage(*p)
		return err
	}
	for i := range p.Content {
		c := &p.Content[i]
		if c.Prepared == "" {
			continue
		}
		if err := verifyDigest(c.From, c.Digest); err != nil {
			return err
		}
		if err := copyTree(c.From, c.Prepared); err != nil {
			return err
		}
		if err := verifyDigest(c.Prepared, c.Digest); err != nil {
			return err
		}
		id, err := identity(c.Prepared)
		if err != nil {
			return err
		}
		c.PreparedID = id
		if err := syncDirectoryTree(c.Prepared); err != nil {
			return err
		}
	}
	if err := syncDir(p.Stage); err != nil {
		return err
	}
	p.Ready = true
	return atomicJSON(s.operationPath(), p)
}

func syncDirectoryTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return syncDir(path)
		}
		return nil
	})
}

func (s *Service) checkOperationState(p OperationPlan, replay bool) error {
	for _, scope := range p.Scopes {
		service, err := s.operationService(scope.Project)
		if err != nil {
			return err
		}
		check := func(current, before, after any) error {
			if !p.Cleanup && reflect.DeepEqual(current, before) || replay && reflect.DeepEqual(current, after) {
				return nil
			}
			return fmt.Errorf("%s state changed since preview; recovery stopped; review a fresh plan", service.Scope())
		}
		if scope.LibraryBefore != nil {
			m, err := service.load()
			if err != nil {
				return err
			}
			if err = check(m, *scope.LibraryBefore, *scope.LibraryAfter); err != nil {
				return err
			}
		}
		if scope.RequestsBefore != nil {
			m, err := service.loadPackages()
			if err != nil {
				return err
			}
			if err = check(m, *scope.RequestsBefore, *scope.RequestsAfter); err != nil {
				return err
			}
		}
		if scope.GroupsBefore != nil {
			m, err := service.loadGroups()
			if err != nil {
				return err
			}
			if err = check(m, *scope.GroupsBefore, *scope.GroupsAfter); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) finishOperation(p OperationPlan) error {
	if !p.Ready {
		return fmt.Errorf("operation payloads are not ready")
	}
	if err := s.checkOperationState(p, true); err != nil {
		return err
	}
	if !p.Cleanup {
		if err := s.verifyRemovals(p); err != nil {
			return err
		}
		if err := s.verifyNewNames(p); err != nil {
			return err
		}
		if err := preflightOperation(p); err != nil {
			return fmt.Errorf("operation paused: %w; run doctor --recover after repairing the conflict", err)
		}
		for _, link := range p.RemoveLinks {
			if containsLink(p.AddLinks, link.Path) && desiredLinkOwned(p.AddLinks, link.Path) {
				continue
			}
			restored := false
			for _, c := range p.Content {
				restored = restored || (c.Kind == "move" || c.Kind == "relocate") && c.To == link.Path && contentDone(c)
			}
			if restored {
				continue
			}
			if err := unlink(link.Path, link.Target); err != nil {
				return err
			}
		}
		for _, c := range p.Content {
			if err := executeContent(c); err != nil {
				return fmt.Errorf("operation paused: %w; run doctor --recover", err)
			}
		}
		for _, link := range p.AddLinks {
			if err := verifyDirectory(link.Target); err != nil {
				return err
			}
			service := s
			for _, scope := range p.Scopes {
				if scope.Project != "" && within(scope.Project, link.Path) {
					var err error
					service, err = s.operationService(scope.Project)
					if err != nil {
						return err
					}
					break
				}
			}
			if err := service.link(link.Path, link.Target); err != nil {
				return err
			}
		}
		if err := preflightOperation(p); err != nil {
			return err
		}
		for _, scope := range p.Scopes {
			service, err := s.operationService(scope.Project)
			if err != nil {
				return err
			}
			if scope.LibraryAfter != nil && (scope.RewriteLibrary || !reflect.DeepEqual(scope.LibraryBefore, scope.LibraryAfter)) {
				if err = service.save(*scope.LibraryAfter); err != nil {
					return err
				}
			}
			if scope.RequestsAfter != nil {
				if err = atomicJSON(service.packageManifestPath(), *scope.RequestsAfter); err != nil {
					return err
				}
				if err = service.registerPackages(*scope.RequestsAfter); err != nil {
					return err
				}
			}
			if scope.GroupsAfter != nil && !reflect.DeepEqual(scope.GroupsBefore, scope.GroupsAfter) {
				if err = atomicJSON(filepath.Join(service.Store, "groups.json"), *scope.GroupsAfter); err != nil {
					return err
				}
			}
		}
		// Mark cleanup before removing old data: an interrupted recursive removal
		// must never be treated as an intact old package on replay.
		p.Cleanup = true
		if err := atomicJSON(s.operationPath(), p); err != nil {
			return err
		}
	}
	for _, c := range p.Content {
		if c.Backup != "" && !absent(c.Backup) {
			if err := realParents(c.Backup); err != nil {
				return err
			}
			id, err := identity(c.Backup)
			if err != nil || id != c.Identity {
				return fmt.Errorf("old content was replaced: %s", c.Backup)
			}
			if err = os.RemoveAll(c.Backup); err != nil {
				return err
			}
		}
		if c.RemoveSource && !absent(c.From) {
			if err := realParents(c.From); err != nil {
				return err
			}
			id, err := identity(c.From)
			if err != nil || id != c.Identity {
				return fmt.Errorf("old source was replaced: %s", c.From)
			}
			if err = os.RemoveAll(c.From); err != nil {
				return err
			}
		}
	}
	if err := s.removeOperationStage(p); err != nil {
		return err
	}
	if err := os.Remove(s.operationPath()); err != nil {
		return err
	}
	return syncDir(s.centralStore())
}

func (s *Service) verifyRemovals(p OperationPlan) error {
	global, err := s.operationService("")
	if err != nil {
		return err
	}
	for _, scope := range p.Scopes {
		if scope.Project != "" || scope.LibraryBefore == nil {
			continue
		}
		for _, record := range scope.LibraryBefore.Records {
			kept := false
			for _, after := range scope.LibraryAfter.Records {
				kept = kept || after.ID == record.ID
			}
			if kept {
				continue
			}
			dependencies, err := global.deletionDependencies(record)
			if err != nil {
				return err
			}
			if len(dependencies) > 0 {
				return fmt.Errorf("remove placements before removing %s from the library: %s", record.Name, strings.Join(dependencies, ", "))
			}
		}
	}
	return nil
}

func (s *Service) verifyNewNames(p OperationPlan) error {
	for _, scope := range p.Scopes {
		if scope.Project != "" || scope.LibraryBefore == nil {
			continue
		}
		global, err := s.operationService("")
		if err != nil {
			return err
		}
		before := map[string]Record{}
		for _, record := range scope.LibraryBefore.Records {
			before[record.ID] = record
		}
		for _, record := range scope.LibraryAfter.Records {
			if old, ok := before[record.ID]; ok && old.Name == record.Name {
				continue
			}
			for _, item := range skills.Scan(global.Roots).Skills {
				if item.Name != record.Name || owned(item.Path, record.Library) {
					continue
				}
				if item.ReadOnly && (p.Action == "resolve" || len(record.Origins) > 1) {
					continue
				}
				planned := false
				for _, c := range p.Content {
					planned = planned || (c.Kind == "move" || c.Kind == "relocate") && c.From == item.Path
					if c.Backup == item.Path {
						id, err := identity(item.Path)
						planned = planned || err == nil && id == c.Identity
					}
				}
				for _, link := range p.RemoveLinks {
					planned = planned || link.Path == item.Path && owned(link.Path, link.Target)
				}
				if !planned {
					return fmt.Errorf("name conflict for %s at %s; resolve it before applying", record.Name, item.Path)
				}
			}
		}
	}
	return nil
}

func verifyDirectory(path string) error {
	if err := realParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("content directory was replaced: %s", path)
	}
	return nil
}

func contentDone(c ContentChange) bool {
	switch c.Kind {
	case "move":
		id, err := identity(c.To)
		return err == nil && id == c.Identity
	case "copy", "update", "relocate":
		id, err := identity(c.To)
		return c.Prepared != "" && err == nil && id == c.PreparedID
	case "delete":
		id, err := identity(c.Backup)
		return c.Backup != "" && err == nil && id == c.Identity
	}
	return false
}

func preflightOperation(p OperationPlan) error {
	for _, c := range p.Content {
		for _, path := range []string{c.From, c.To, c.Prepared, c.Backup} {
			if path != "" {
				if err := realParents(path); err != nil {
					return err
				}
			}
		}
		if contentDone(c) {
			path := c.To
			digest := c.Digest
			if c.Kind == "delete" {
				path = c.Backup
			}
			if err := verifyDigest(path, digest); err != nil {
				return err
			}
			if c.Kind == "update" || c.Kind == "relocate" {
				id, err := identity(c.Backup)
				if err != nil || id != c.Identity {
					return fmt.Errorf("old content changed: %s", c.Backup)
				}
				if err := verifyDigest(c.Backup, c.OldDigest); err != nil {
					return err
				}
			}
			if c.RemoveSource {
				id, err := identity(c.From)
				if err != nil || id != c.Identity {
					return fmt.Errorf("old source changed: %s", c.From)
				}
				if err := verifyDigest(c.From, c.Digest); err != nil {
					return err
				}
			}
			continue
		}
		switch c.Kind {
		case "move", "delete":
			id, err := identity(c.From)
			if err != nil {
				return err
			}
			if id != c.Identity {
				return fmt.Errorf("source folder changed: %s", c.From)
			}
			if err := verifyDigest(c.From, c.Digest); err != nil {
				return err
			}
		case "copy", "update", "relocate":
			path := c.From
			if c.Prepared != "" {
				path = c.Prepared
			}
			if err := verifyDigest(path, c.Digest); err != nil {
				return err
			}
			if c.RemoveSource {
				id, err := identity(c.From)
				if err != nil || id != c.Identity {
					return fmt.Errorf("old source changed: %s", c.From)
				}
				if err := verifyDigest(c.From, c.Digest); err != nil {
					return err
				}
			}
			if c.Kind == "update" || c.Kind == "relocate" {
				old := c.To
				if c.Kind == "relocate" {
					old = c.From
				}
				if c.Backup != "" && !absent(c.Backup) {
					old = c.Backup
				}
				id, err := identity(old)
				if err != nil || id != c.Identity {
					return fmt.Errorf("update source changed: %s", old)
				}
				if err := verifyDigest(old, c.OldDigest); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown content change %q", c.Kind)
		}
		if c.Kind != "update" && c.Kind != "delete" && !absent(c.To) {
			allowed := false
			for _, link := range p.RemoveLinks {
				allowed = allowed || link.Path == c.To && owned(link.Path, link.Target)
			}
			if !allowed {
				return fmt.Errorf("destination is occupied: %s", c.To)
			}
		}
		if c.Kind == "update" && c.Backup != "" && !absent(c.Backup) && !absent(c.To) {
			return fmt.Errorf("update destination is occupied: %s", c.To)
		}
	}
	for _, link := range p.RemoveLinks {
		if err := realParents(link.Path); err != nil {
			return err
		}
		if absent(link.Path) || owned(link.Path, link.Target) || desiredLinkOwned(p.AddLinks, link.Path) {
			continue
		}
		// A restore may already have put the reviewed directory back here.
		restored := false
		for _, c := range p.Content {
			restored = restored || (c.Kind == "move" || c.Kind == "relocate") && c.To == link.Path && contentDone(c)
		}
		if !restored {
			return fmt.Errorf("owned link was replaced: %s", link.Path)
		}
	}
	for _, link := range p.AddLinks {
		if err := realParents(link.Path); err != nil {
			return err
		}
		if absent(link.Path) || owned(link.Path, link.Target) {
			continue
		}
		allowed := false
		for _, old := range p.RemoveLinks {
			allowed = allowed || old.Path == link.Path && owned(old.Path, old.Target)
		}
		for _, c := range p.Content {
			id, err := identity(link.Path)
			allowed = allowed || (c.Kind == "move" || c.Kind == "relocate") && c.From == link.Path && err == nil && id == c.Identity
		}
		if !allowed {
			return fmt.Errorf("discovery path is occupied: %s", link.Path)
		}
	}
	return nil
}

func executeContent(c ContentChange) error {
	if contentDone(c) {
		return nil
	}
	switch c.Kind {
	case "move":
		return move(c.From, c.To, c.Identity)
	case "copy":
		return move(c.Prepared, c.To, c.PreparedID)
	case "delete":
		return move(c.From, c.Backup, c.Identity)
	case "relocate":
		if absent(c.Backup) {
			if err := move(c.From, c.Backup, c.Identity); err != nil {
				return err
			}
		}
		return move(c.Prepared, c.To, c.PreparedID)
	case "update":
		if absent(c.Backup) {
			if err := move(c.To, c.Backup, c.Identity); err != nil {
				return err
			}
		}
		return move(c.Prepared, c.To, c.PreparedID)
	}
	return fmt.Errorf("unknown content change")
}

func containsLink(links []LinkPlan, path string) bool {
	for _, link := range links {
		if link.Path == path {
			return true
		}
	}
	return false
}
func desiredLinkOwned(links []LinkPlan, path string) bool {
	for _, link := range links {
		if link.Path == path && owned(path, link.Target) {
			return true
		}
	}
	return false
}

func (s *Service) validateOperation(p OperationPlan, replay bool) error {
	if p.Version != Version || p.Action == "" || len(p.Scopes) == 0 {
		return fmt.Errorf("invalid operation plan")
	}
	if replay {
		if filepath.Dir(p.Stage) != filepath.Join(s.centralStore(), "operations") || len(filepath.Base(p.Stage)) != 32 {
			return fmt.Errorf("invalid operation staging path")
		}
		if _, err := hex.DecodeString(filepath.Base(p.Stage)); err != nil {
			return err
		}
		if p.StageID == (Identity{}) || p.Cleanup && !p.Ready {
			return fmt.Errorf("invalid operation phase")
		}
	} else if p.Stage != "" || p.Cleanup || p.Ready || p.StageID != (Identity{}) {
		return fmt.Errorf("operation was already applied")
	}
	allowed := map[string]bool{}
	links := map[LinkPlan]bool{}
	seen := map[string]bool{}
	for _, scope := range p.Scopes {
		if seen[scope.Project] {
			return fmt.Errorf("duplicate operation scope")
		}
		seen[scope.Project] = true
		service, err := s.operationService(scope.Project)
		if err != nil {
			return err
		}
		if service.Config.Project != scope.Project {
			return fmt.Errorf("project path changed")
		}
		if (scope.LibraryBefore == nil) != (scope.LibraryAfter == nil) || (scope.RequestsBefore == nil) != (scope.RequestsAfter == nil) || (scope.GroupsBefore == nil) != (scope.GroupsAfter == nil) {
			return fmt.Errorf("incomplete operation state")
		}
		for _, m := range []*Manifest{scope.LibraryBefore, scope.LibraryAfter} {
			if m == nil {
				continue
			}
			if m.Version != Version {
				return fmt.Errorf("invalid library version")
			}
			if err := validateReturned(m.Returned); err != nil {
				return err
			}
			ids := map[string]bool{}
			names := map[string]bool{}
			for _, record := range m.Records {
				if err := service.validate(record); err != nil {
					return err
				}
				if ids[record.ID] || names[record.Name] {
					return fmt.Errorf("conflicting library name or ID: %s", record.Name)
				}
				ids[record.ID], names[record.Name] = true, true
				allowed[record.Library], allowed[filepath.Dir(record.Library)] = true, true
				if record.Original != "" {
					allowed[record.Original] = true
				}
				if record.MigratedFrom != nil {
					allowed[record.MigratedFrom.Path] = true
					for _, origin := range record.MigratedFrom.Origins {
						allowed[origin.Path] = true
						if origin.Backup != "" {
							allowed[origin.Backup] = true
						}
					}
				}
				for _, origin := range record.Origins {
					allowed[origin.Path] = true
					if origin.Backup != "" {
						allowed[origin.Backup] = true
					}
				}
				for _, path := range record.Links {
					links[LinkPlan{Path: path, Target: record.Library}] = true
				}
			}
		}
		for _, m := range []*PackageManifest{scope.RequestsBefore, scope.RequestsAfter} {
			if m == nil {
				continue
			}
			if scope.Project == "" {
				return fmt.Errorf("global scope cannot have project requests")
			}
			if m.Version != packageManifestVersion {
				return fmt.Errorf("unsupported project request version")
			}
			if err := validatePackageManifest(*m); err != nil {
				return err
			}
			for _, skill := range m.Skills {
				links[LinkPlan{Path: filepath.Join(service.Shared(), skill.Name), Target: service.centralLibrary(skill)}] = true
			}
		}
		if scope.GroupsBefore != nil && scope.Project != "" {
			return fmt.Errorf("groups belong to the personal library")
		}
		for _, m := range []*groupManifest{scope.GroupsBefore, scope.GroupsAfter} {
			if m == nil {
				continue
			}
			if m.Version != packageManifestVersion {
				return fmt.Errorf("unsupported preset version")
			}
			names := map[string]bool{}
			for _, group := range m.Groups {
				if !packageNamePattern.MatchString(group.Name) || names[group.Name] || len(group.Members) == 0 {
					return fmt.Errorf("invalid preset %q", group.Name)
				}
				names[group.Name] = true
				for _, member := range group.Members {
					if err := validatePackageRequest(member); err != nil {
						return err
					}
				}
			}
		}
	}
	for i, c := range p.Content {
		if c.Kind != "move" && c.Kind != "copy" && c.Kind != "delete" && c.Kind != "update" && c.Kind != "relocate" {
			return fmt.Errorf("unknown content change")
		}
		if _, err := hex.DecodeString(c.Digest); err != nil || len(c.Digest) != 64 {
			return fmt.Errorf("content digest missing")
		}
		if c.Kind != "copy" && c.Identity == (Identity{}) {
			return fmt.Errorf("content identity missing")
		}
		if c.RemoveSource && c.Kind != "copy" {
			return fmt.Errorf("invalid source cleanup")
		}
		if (c.Kind == "move" || c.Kind == "copy") && c.Backup != "" {
			return fmt.Errorf("unexpected content backup")
		}
		if c.Kind == "move" || c.Kind == "delete" {
			if c.Prepared != "" || c.PreparedID != (Identity{}) {
				return fmt.Errorf("unexpected content payload")
			}
		}
		if c.Kind == "update" || c.Kind == "relocate" {
			if _, err := hex.DecodeString(c.OldDigest); err != nil || len(c.OldDigest) != 64 {
				return fmt.Errorf("old content digest missing")
			}
		}
		if c.Kind == "move" || c.Kind == "relocate" || c.Kind == "delete" || c.RemoveSource {
			if !allowed[c.From] {
				return fmt.Errorf("unauthorized source: %s", c.From)
			}
		}
		if c.Kind != "delete" && !allowed[c.To] {
			return fmt.Errorf("unauthorized destination: %s", c.To)
		}
		if replay {
			if c.Kind == "copy" || c.Kind == "update" {
				if c.Prepared != filepath.Join(p.Stage, fmt.Sprintf("payload-%d", i)) {
					return fmt.Errorf("invalid prepared payload")
				}
			}
			if c.Kind == "relocate" && c.Prepared != filepath.Join(filepath.Dir(c.To), ".skmr-payload-"+filepath.Base(p.Stage)+fmt.Sprintf("-%d", i)) {
				return fmt.Errorf("invalid relocation payload")
			}
			if c.Kind == "delete" || c.Kind == "update" || c.Kind == "relocate" {
				path := c.From
				if c.Kind == "update" {
					path = c.To
				}
				if c.Backup != filepath.Join(filepath.Dir(path), ".skmr-old-"+filepath.Base(p.Stage)+fmt.Sprintf("-%d", i)) {
					return fmt.Errorf("invalid old content path")
				}
			}
		} else if c.Prepared != "" || c.Backup != "" {
			return fmt.Errorf("invalid content preview")
		}
	}
	for _, link := range append(append([]LinkPlan{}, p.RemoveLinks...), p.AddLinks...) {
		if !links[link] {
			return fmt.Errorf("unauthorized discovery link: %s", link.Path)
		}
	}
	return nil
}

func operationPlan(action, description string) OperationPlan {
	return OperationPlan{Version: Version, Action: action, Description: description}
}

func (s *Service) operationFromPlan(p Plan) (OperationPlan, error) {
	after, err := resultManifest(p)
	if err != nil {
		return OperationPlan{}, err
	}
	op := operationPlan(p.Action, p.String())
	op.Scopes = []OperationScope{{Project: s.Config.Project, LibraryBefore: &p.Before, LibraryAfter: &after}}
	for _, move := range p.Moves {
		op.Content = append(op.Content, ContentChange{Kind: "move", From: move.From, To: move.To, Identity: move.Identity, Digest: move.Digest})
	}
	op.RemoveLinks = append(op.RemoveLinks, p.RemoveLinks...)
	for _, path := range p.Record.Links {
		link := LinkPlan{Path: path, Target: p.Record.Library}
		if p.Action == "disable" || p.Action == "restore" {
			op.RemoveLinks = append(op.RemoveLinks, link)
		} else {
			op.AddLinks = append(op.AddLinks, link)
		}
	}
	if p.Action == "restore" && p.Record.Remote != nil {
		id, err := identity(p.Record.Library)
		if err != nil {
			return op, err
		}
		digest, err := skills.Digest(p.Record.Library)
		if err != nil {
			return op, err
		}
		op.Content = append(op.Content, ContentChange{Kind: "delete", From: p.Record.Library, Identity: id, Digest: digest})
	}
	return op, nil
}

func (s *Service) operationFromPackages(p PackagePlan) (OperationPlan, error) {
	op := operationPlan(p.Action, p.String())
	op.Scopes = []OperationScope{{Project: s.Config.Project, RequestsBefore: &p.Before, RequestsAfter: &p.After}}
	global, err := s.operationService("")
	if err != nil {
		return op, err
	}
	m, err := global.load()
	if err != nil {
		return op, err
	}
	op.Scopes = append(op.Scopes, OperationScope{LibraryBefore: &m, LibraryAfter: &m})
	groups, err := global.loadGroups()
	if err != nil {
		return op, err
	}
	op.Scopes[len(op.Scopes)-1].GroupsBefore, op.Scopes[len(op.Scopes)-1].GroupsAfter = &groups, &groups
	for _, old := range p.Before.Skills {
		kept := false
		for _, desired := range p.After.Skills {
			kept = kept || desired.Name == old.Name && desired.ID == old.ID
		}
		if !kept {
			op.RemoveLinks = append(op.RemoveLinks, LinkPlan{Path: filepath.Join(s.Shared(), old.Name), Target: s.centralLibrary(old)})
		}
	}
	for _, skill := range p.After.Skills {
		op.AddLinks = append(op.AddLinks, LinkPlan{Path: filepath.Join(s.Shared(), skill.Name), Target: s.centralLibrary(skill)})
	}
	return op, nil
}

func (s *Service) operationFromRemote(p RemotePlan) (OperationPlan, error) {
	op := operationPlan(p.Action, p.String())
	before, err := s.load()
	if err != nil {
		return op, err
	}
	if p.Before != nil {
		before = *p.Before
	}
	after := cloneManifest(before)
	for _, entry := range p.Entries {
		c := ContentChange{Kind: "copy", From: entry.StagedPath, To: entry.Record.Library, Digest: entry.Record.Remote.Digest}
		if p.Action == "update" {
			c.Kind, c.OldDigest = "update", entry.OldDigest
			c.Identity, err = identity(c.To)
			if err != nil {
				return op, err
			}
			found := false
			for i := range after.Records {
				if after.Records[i].ID == entry.Record.ID {
					after.Records[i] = entry.Record
					found = true
					break
				}
			}
			if !found {
				return op, fmt.Errorf("remote skill changed since preview")
			}
		} else {
			after.Records = append(after.Records, entry.Record)
		}
		op.Content = append(op.Content, c)
	}
	op.Scopes = []OperationScope{{Project: s.Config.Project, LibraryBefore: &before, LibraryAfter: &after}}
	return op, nil
}

func (s *Service) operationFromDelete(p DeletePlan) OperationPlan {
	after := cloneManifest(p.Before)
	after.Records = []Record{}
	for _, record := range p.Before.Records {
		if record.ID != p.SkillID {
			after.Records = append(after.Records, record)
		}
	}
	op := operationPlan("delete", p.String())
	op.Scopes = []OperationScope{{Project: s.Config.Project, LibraryBefore: &p.Before, LibraryAfter: &after}}
	op.Content = []ContentChange{{Kind: "delete", From: p.Source, Identity: p.Identity, Digest: p.Digest}}
	for _, path := range p.Record.Links {
		op.RemoveLinks = append(op.RemoveLinks, LinkPlan{Path: path, Target: p.Record.Library})
	}
	return op
}

func (s *Service) operationFromBatch(p BatchPlan) (OperationPlan, error) {
	after, err := batchResult(p)
	if err != nil {
		return OperationPlan{}, err
	}
	op := operationPlan(p.Action, p.String())
	op.Scopes = []OperationScope{{Project: s.Config.Project, LibraryBefore: &p.Before, LibraryAfter: &after}}
	for _, plan := range p.Plans {
		item, err := s.operationFromPlan(plan)
		if err != nil {
			return op, err
		}
		op.Content = append(op.Content, item.Content...)
		op.AddLinks = append(op.AddLinks, item.AddLinks...)
		op.RemoveLinks = append(op.RemoveLinks, item.RemoveLinks...)
	}
	return op, nil
}

// Merge descriptions are kept in the review; scopes are coalesced only when
// their expected state agrees, so compound operations cannot hide stale plans.
func mergeOperations(a, b OperationPlan) (OperationPlan, error) {
	for _, incoming := range b.Scopes {
		found := false
		for i := range a.Scopes {
			current := &a.Scopes[i]
			if current.Project != incoming.Project {
				continue
			}
			found = true
			if incoming.LibraryBefore != nil {
				if current.LibraryBefore != nil && !reflect.DeepEqual(current.LibraryBefore, incoming.LibraryBefore) {
					return a, fmt.Errorf("library previews disagree")
				}
				if current.LibraryBefore == nil {
					current.LibraryBefore, current.LibraryAfter = incoming.LibraryBefore, incoming.LibraryAfter
				}
				current.RewriteLibrary = current.RewriteLibrary || incoming.RewriteLibrary
			}
			if incoming.RequestsBefore != nil {
				current.RequestsBefore, current.RequestsAfter = incoming.RequestsBefore, incoming.RequestsAfter
			}
			if incoming.GroupsBefore != nil {
				current.GroupsBefore, current.GroupsAfter = incoming.GroupsBefore, incoming.GroupsAfter
			}
		}
		if !found {
			a.Scopes = append(a.Scopes, incoming)
		}
	}
	a.Content = append(a.Content, b.Content...)
	a.RemoveLinks = append(a.RemoveLinks, b.RemoveLinks...)
	a.AddLinks = append(a.AddLinks, b.AddLinks...)
	a.Description = strings.TrimSpace(a.Description + "\n" + b.Description)
	return a, nil
}
