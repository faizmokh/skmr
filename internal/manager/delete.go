package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/faizmokh/skmr/internal/skills"
)

// DeletePlan describes one permanent deletion, including content hidden in the library.
type DeletePlan struct {
	Version  int      `json:"version"`
	SkillID  string   `json:"skill_id"`
	Name     string   `json:"name"`
	Source   string   `json:"source"`
	Stage    string   `json:"stage"`
	Identity Identity `json:"identity"`
	Digest   string   `json:"digest"`
	Managed  bool     `json:"managed"`
	Cleanup  bool     `json:"cleanup,omitempty"`
	Record   Record   `json:"record,omitempty"`
	Before   Manifest `json:"before"`
}

func (p DeletePlan) String() string {
	lines := []string{"Permanently delete " + p.Source}
	if p.Managed {
		for _, link := range p.Record.Links {
			lines = append(lines, "Remove owned link "+link)
		}
		for _, origin := range p.Record.Origins {
			if origin.Backup != "" {
				lines = append(lines, "Delete stored duplicate "+origin.Backup)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (s *Service) deletionJournalPath() string { return filepath.Join(s.Store, "delete-journal.json") }

func (s *Service) PreviewDelete(id string) (DeletePlan, error) {
	if s.hasPending() {
		return DeletePlan{}, fmt.Errorf("an interrupted operation needs recovery; run doctor --recover")
	}
	manifest, err := s.load()
	if err != nil {
		return DeletePlan{}, err
	}
	result, err := s.List()
	if err != nil {
		return DeletePlan{}, err
	}
	var selected *skills.Skill
	for i := range result.Skills {
		if result.Skills[i].ID == id || result.Skills[i].Name == id && result.Skills[i].Managed {
			selected = &result.Skills[i]
			break
		}
	}
	if selected == nil {
		return DeletePlan{}, fmt.Errorf("skill %q was not found in this scope", id)
	}
	if selected.ReadOnly || selected.Inherited || selected.Installed {
		return DeletePlan{}, fmt.Errorf("skill %q is view only here; open its owning scope", selected.Name)
	}
	if !selected.Managed {
		return DeletePlan{}, fmt.Errorf("skill %q is outside the library; add it or resolve its copies first", selected.Name)
	}
	p := DeletePlan{Version: Version, SkillID: selected.ID, Name: selected.Name, Managed: selected.Managed, Before: manifest}
	if p.Managed {
		found := false
		for _, record := range manifest.Records {
			if record.ID == selected.ID {
				p.Record = record
				found = true
				break
			}
		}
		if !found {
			return DeletePlan{}, fmt.Errorf("managed skill record is missing")
		}
		dependencies, err := s.deletionDependencies(p.Record)
		if err != nil {
			return DeletePlan{}, err
		}
		if len(dependencies) > 0 {
			return DeletePlan{}, fmt.Errorf("remove dependencies before deleting %s: %s", p.Name, strings.Join(dependencies, ", "))
		}
		for _, link := range p.Record.Links {
			if err := realParents(link); err != nil {
				return DeletePlan{}, err
			}
			if !absent(link) && !owned(link, p.Record.Library) {
				return DeletePlan{}, fmt.Errorf("managed link was replaced: %s", link)
			}
		}
		if s.Config.Project == "" && p.Record.Enabled {
			return DeletePlan{}, fmt.Errorf("disable the global placement before deleting %s", p.Name)
		}
		p.Source = filepath.Dir(p.Record.Library)
	} else {
		p.Source = selected.Path
		allowed := false
		for _, root := range s.Roots {
			if !root.ReadOnly && !root.Inherited && p.Source != root.Path && within(root.Path, p.Source) {
				allowed = true
			}
		}
		if !allowed {
			return DeletePlan{}, fmt.Errorf("skill is outside a writable discovery root: %s", p.Source)
		}
	}
	if err := realParents(p.Source); err != nil {
		return DeletePlan{}, err
	}
	info, err := os.Lstat(p.Source)
	if err != nil {
		return DeletePlan{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return DeletePlan{}, fmt.Errorf("skill folder was replaced: %s", p.Source)
	}
	p.Identity, err = identity(p.Source)
	if err != nil {
		return DeletePlan{}, err
	}
	p.Digest, err = skills.Digest(p.Source)
	if err != nil {
		return DeletePlan{}, err
	}
	p.Stage = filepath.Join(filepath.Dir(p.Source), "."+filepath.Base(p.Source)+".skmr-delete-"+p.SkillID)
	if err := available(p.Stage); err != nil {
		return DeletePlan{}, err
	}
	return p, nil
}

func (s *Service) ApplyDelete(plan DeletePlan) error {
	fresh, err := s.PreviewDelete(plan.SkillID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh, plan) {
		return fmt.Errorf("skill state changed since preview; review a fresh plan")
	}
	return s.ApplyOperation(s.operationFromDelete(plan))
}

func (s *Service) recoverDelete() error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	b, err := os.ReadFile(s.deletionJournalPath())
	if err != nil {
		return err
	}
	var plan DeletePlan
	if err := json.Unmarshal(b, &plan); err != nil {
		return err
	}
	return s.finishDelete(plan)
}

func (s *Service) finishDelete(plan DeletePlan) error {
	if plan.Version != Version || plan.SkillID == "" || plan.Before.Version != Version || plan.Stage != filepath.Join(filepath.Dir(plan.Source), "."+filepath.Base(plan.Source)+".skmr-delete-"+plan.SkillID) {
		return fmt.Errorf("invalid deletion journal")
	}
	if plan.Managed {
		if err := s.validate(plan.Record); err != nil {
			return err
		}
		if plan.Record.ID != plan.SkillID || plan.Source != filepath.Dir(plan.Record.Library) {
			return fmt.Errorf("invalid managed deletion journal")
		}
	} else {
		allowed := false
		for _, root := range s.Roots {
			allowed = allowed || !root.ReadOnly && !root.Inherited && plan.Source != root.Path && within(root.Path, plan.Source)
		}
		if !allowed {
			return fmt.Errorf("invalid unmanaged deletion journal")
		}
	}
	for _, path := range []string{plan.Source, plan.Stage} {
		if err := realParents(path); err != nil {
			return err
		}
	}
	current, err := s.load()
	if err != nil {
		return err
	}
	after := cloneManifest(plan.Before)
	if plan.Managed {
		found := false
		for i, record := range after.Records {
			if record.ID == plan.SkillID {
				after.Records = append(after.Records[:i], after.Records[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("deletion journal refers to an unknown skill")
		}
	}
	committed := plan.Managed && reflect.DeepEqual(current, after)
	if !committed && !reflect.DeepEqual(current, plan.Before) {
		return fmt.Errorf("manifest changed outside the interrupted deletion; recovery stopped")
	}
	if !committed && !plan.Cleanup {
		path := plan.Source
		if !absent(plan.Stage) {
			path = plan.Stage
		}
		if err := verifyDeleteSource(path, plan); err != nil {
			return fmt.Errorf("deletion paused: %w; resolve the conflict, then run doctor --recover", err)
		}
		if plan.Managed {
			for _, link := range plan.Record.Links {
				if !absent(link) && !owned(link, plan.Record.Library) {
					return fmt.Errorf("managed link was replaced: %s", link)
				}
			}
			for _, link := range plan.Record.Links {
				if err := unlink(link, plan.Record.Library); err != nil {
					return err
				}
			}
		}
		if path == plan.Source {
			if err := os.Rename(plan.Source, plan.Stage); err != nil {
				return err
			}
		}
		if plan.Managed {
			if err := s.save(after); err != nil {
				return fmt.Errorf("save interrupted: %w; run doctor --recover", err)
			}
		}
	}
	if !absent(plan.Stage) {
		id, err := identity(plan.Stage)
		if err != nil || id != plan.Identity {
			return fmt.Errorf("staged skill folder changed: %s", plan.Stage)
		}
		if !plan.Cleanup {
			if err := verifyDigest(plan.Stage, plan.Digest); err != nil {
				return err
			}
			plan.Cleanup = true
			if err := atomicJSON(s.deletionJournalPath(), plan); err != nil {
				return err
			}
		}
		if err := os.RemoveAll(plan.Stage); err != nil {
			return err
		}
	}
	if err := os.Remove(s.deletionJournalPath()); err != nil {
		return err
	}
	return syncDir(s.Store)
}

func verifyDeleteSource(path string, plan DeletePlan) error {
	id, err := identity(path)
	if err != nil {
		return err
	}
	if id != plan.Identity {
		return fmt.Errorf("skill folder changed: %s", path)
	}
	return verifyDigest(path, plan.Digest)
}
