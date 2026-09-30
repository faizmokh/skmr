package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// RemoteAddPlan stores selected remote skills centrally and requests them in a project.
type RemoteAddPlan struct {
	Remote  RemotePlan
	Package PackagePlan
	Reused  []string
}

type remoteAddJournal struct {
	Version int           `json:"version"`
	Entries []RemoteEntry `json:"entries"`
	Package PackagePlan   `json:"package"`
}

func (p RemoteAddPlan) String() string {
	result := p.Remote.String()
	if len(p.Remote.Entries) == 0 {
		result = ""
	}
	for _, name := range p.Reused {
		if result != "" {
			result += "\n"
		}
		result += "Reuse " + name + " from personal library"
	}
	if result != "" {
		result += "\n"
	}
	return result + p.Package.String()
}

func (p RemoteAddPlan) Cleanup() { p.Remote.Cleanup() }

func (s *Service) remoteAddJournalPath() string {
	return filepath.Join(s.Store, "remote-add-journal.json")
}

func (s *Service) remoteAddOnlyPending() bool {
	return !absent(s.remoteAddJournalPath()) &&
		absent(filepath.Join(s.Store, "journal.json")) &&
		absent(s.deletionJournalPath()) &&
		absent(filepath.Join(s.Store, "batch.json")) &&
		absent(filepath.Join(s.Store, "transfer.json")) &&
		absent(filepath.Join(s.Store, "packages-journal.json")) &&
		absent(filepath.Join(s.Store, "remote-journal.json"))
}

// PrepareRemoteAdd previews both the central import and project installation.
func (s *Service) prepareRemoteLibrary(raw string, names []string) (RemoteAddPlan, error) {
	if s.hasPending() {
		return RemoteAddPlan{}, fmt.Errorf("recover the interrupted operation before adding skills")
	}
	source, _, err := normalizeRemote(raw)
	if err != nil {
		return RemoteAddPlan{}, err
	}
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return RemoteAddPlan{}, err
	}
	if global.hasPending() {
		return RemoteAddPlan{}, fmt.Errorf("recover the interrupted personal library operation before adding skills")
	}
	library, err := global.load()
	if err != nil {
		return RemoteAddPlan{}, err
	}
	p := RemoteAddPlan{Remote: RemotePlan{Action: "import", Source: raw, Before: &library}}
	var fresh []string
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return RemoteAddPlan{}, fmt.Errorf("skill %q was selected more than once", name)
		}
		seen[name] = true
		var found *Record
		for i := range library.Records {
			if library.Records[i].Name == name {
				found = &library.Records[i]
				break
			}
		}
		if found == nil {
			fresh = append(fresh, name)
			continue
		}
		if found.Remote == nil || found.Remote.Skill != name {
			return RemoteAddPlan{}, fmt.Errorf("skill %q already exists from another source", name)
		}
		oldSource, _, e := normalizeRemote(found.Remote.URL)
		if e != nil || oldSource != source {
			return RemoteAddPlan{}, fmt.Errorf("skill %q already exists from another source", name)
		}
		p.Reused = append(p.Reused, name)
	}
	if len(fresh) > 0 {
		p.Remote, err = global.PrepareImport(raw, fresh)
		if err != nil {
			return RemoteAddPlan{}, err
		}
		if p.Remote.Before == nil || !reflect.DeepEqual(*p.Remote.Before, library) {
			p.Cleanup()
			return RemoteAddPlan{}, fmt.Errorf("personal library changed during discovery; review a fresh plan")
		}
	}
	return p, nil
}

func (s *Service) PrepareRemoteAdd(raw string, names []string) (RemoteAddPlan, error) {
	if s.Config.Project == "" {
		return RemoteAddPlan{}, fmt.Errorf("remote add requires a project")
	}
	p, err := s.prepareRemoteLibrary(raw, names)
	if err != nil {
		return p, err
	}
	additions := make([]Record, 0, len(p.Remote.Entries))
	for _, entry := range p.Remote.Entries {
		additions = append(additions, entry.Record)
	}
	p.Package, err = s.previewPackages("add", names, additions, false)
	if err != nil {
		p.Cleanup()
		return RemoteAddPlan{}, err
	}
	return p, nil
}

// ApplyRemoteAdd applies the import and project placement in one operation.
func (s *Service) ApplyRemoteAdd(p RemoteAddPlan) error {
	defer p.Cleanup()
	op, err := s.operationFromRemoteAdd(p)
	if err != nil {
		return err
	}
	return s.ApplyOperation(op)
}

func (s *Service) operationFromRemoteAdd(p RemoteAddPlan) (OperationPlan, error) {
	global, err := s.operationService("")
	if err != nil {
		return OperationPlan{}, err
	}
	remote, err := global.operationFromRemote(p.Remote)
	if err != nil {
		return remote, err
	}
	placements, err := s.operationFromPackages(p.Package)
	if err != nil {
		return remote, err
	}
	op, err := mergeOperations(remote, placements)
	op.Action, op.Description = "add", p.String()
	return op, err
}

func (s *Service) clearRemoteAddJournal() error {
	if err := os.Remove(s.remoteAddJournalPath()); err != nil {
		return err
	}
	return syncDir(s.Store)
}

func (s *Service) recoverRemoteAdd() error {
	b, err := os.ReadFile(s.remoteAddJournalPath())
	if err != nil {
		return err
	}
	var j remoteAddJournal
	if err = json.Unmarshal(b, &j); err != nil {
		return err
	}
	if j.Version != Version || len(j.Entries) == 0 || j.Package.Action != "add" {
		return fmt.Errorf("invalid remote add journal")
	}
	global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
	if err != nil {
		return err
	}
	if !absent(global.remoteJournalPath()) {
		if err = global.recoverRemote(); err != nil {
			return err
		}
	}
	library, err := global.load()
	if err != nil {
		return err
	}
	present := 0
	for _, entry := range j.Entries {
		for _, record := range library.Records {
			if reflect.DeepEqual(record, entry.Record) {
				present++
				break
			}
		}
	}
	if present == 0 {
		return s.clearRemoteAddJournal()
	}
	if present != len(j.Entries) {
		return fmt.Errorf("personal library changed during remote add; recovery stopped")
	}
	if !absent(filepath.Join(s.Store, "packages-journal.json")) {
		if err = s.recoverPackages(); err != nil {
			return err
		}
	}
	current, err := s.loadPackages()
	if err != nil {
		return err
	}
	if reflect.DeepEqual(current, j.Package.Before) {
		if err = s.applyPackages(j.Package, true); err != nil {
			return err
		}
	} else if !reflect.DeepEqual(current, j.Package.After) {
		return fmt.Errorf("project packages changed during remote add; recovery stopped")
	}
	return s.clearRemoteAddJournal()
}
