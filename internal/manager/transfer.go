package manager

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/faizmokh/skmr/internal/skills"
)

// TransferPlan decodes historical transfers for recovery. New operations keep
// all content in the personal library and change placements instead.
type TransferPlan struct {
	Version            int      `json:"version"`
	Action             string   `json:"action"`
	SourceProject      string   `json:"source_project,omitempty"`
	DestinationProject string   `json:"destination_project,omitempty"`
	SourceStore        string   `json:"source_store"`
	DestinationStore   string   `json:"destination_store"`
	SourceBefore       Manifest `json:"source_before"`
	DestinationBefore  Manifest `json:"destination_before"`
	SourceRecord       Record   `json:"source_record"`
	DestinationRecord  Record   `json:"destination_record"`
	Identity           Identity `json:"identity"`
	Digest             string   `json:"digest"`
}

func lockServices(a, b *Service) (func(), error) {
	services := []*Service{a, b}
	sort.Slice(services, func(i, j int) bool { return services[i].Store < services[j].Store })
	first, err := services[0].lock()
	if err != nil {
		return nil, err
	}
	second, err := services[1].lock()
	if err != nil {
		first()
		return nil, err
	}
	return func() { second(); first() }, nil
}

func (s *Service) recoverTransfer() error {
	b, err := os.ReadFile(filepath.Join(s.Store, "transfer.json"))
	if err != nil {
		return err
	}
	var plan TransferPlan
	if err = json.Unmarshal(b, &plan); err != nil {
		return fmt.Errorf("read transfer recovery journal: %w", err)
	}
	source, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome, Project: plan.SourceProject})
	if err != nil {
		return err
	}
	destination, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome, Project: plan.DestinationProject})
	if err != nil {
		return err
	}
	if plan.Version != Version || plan.SourceBefore.Version != Version || plan.DestinationBefore.Version != Version {
		return fmt.Errorf("unsupported transfer journal version")
	}
	if plan.Action != "move" && plan.Action != "copy" {
		return fmt.Errorf("unknown transfer journal action %q", plan.Action)
	}
	if source.Store != plan.SourceStore || destination.Store != plan.DestinationStore {
		return fmt.Errorf("transfer journal scope paths no longer match")
	}
	if err = source.validate(plan.SourceRecord); err != nil {
		return err
	}
	if err = destination.validate(plan.DestinationRecord); err != nil {
		return err
	}
	for _, record := range plan.SourceBefore.Records {
		if err = source.validate(record); err != nil {
			return err
		}
	}
	for _, record := range plan.DestinationBefore.Records {
		if err = destination.validate(record); err != nil {
			return err
		}
	}
	unlock, err := lockServices(source, destination)
	if err != nil {
		return err
	}
	defer unlock()
	return source.finishTransfer(destination, plan)
}

func (s *Service) finishTransfer(destination *Service, plan TransferPlan) error {
	if err := executeTransfer(s, destination, plan); err != nil {
		return fmt.Errorf("operation paused: %w; resolve the conflict, then run doctor --recover", err)
	}
	sourceAfter := plan.SourceBefore
	if plan.Action == "move" {
		records := make([]Record, 0, len(sourceAfter.Records)-1)
		for _, record := range sourceAfter.Records {
			if record.ID != plan.SourceRecord.ID {
				records = append(records, record)
			}
		}
		sourceAfter.Records = records
	}
	destinationAfter := plan.DestinationBefore
	destinationAfter.Records = append(destinationAfter.Records, plan.DestinationRecord)
	if err := acceptManifest(destination, plan.DestinationBefore, destinationAfter); err != nil {
		return err
	}
	if err := acceptManifest(s, plan.SourceBefore, sourceAfter); err != nil {
		return err
	}
	for _, service := range []*Service{s, destination} {
		if err := os.Remove(filepath.Join(service.Store, "transfer.json")); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := syncDir(service.Store); err != nil {
			return err
		}
	}
	return nil
}

func acceptManifest(service *Service, before, after Manifest) error {
	current, err := service.load()
	if err != nil {
		return err
	}
	if reflect.DeepEqual(current, after) {
		return nil
	}
	if !reflect.DeepEqual(current, before) {
		return fmt.Errorf("manifest changed outside the interrupted transfer; recovery stopped")
	}
	if err = service.save(after); err != nil {
		return fmt.Errorf("save interrupted: %w; run doctor --recover", err)
	}
	return nil
}

func executeTransfer(source, destination *Service, plan TransferPlan) error {
	src, dst := plan.SourceRecord, plan.DestinationRecord
	for _, path := range append(append([]string{}, src.Links...), dst.Links...) {
		if err := realParents(path); err != nil {
			return err
		}
	}
	for _, path := range src.Links {
		if !absent(path) && !owned(path, src.Library) {
			return fmt.Errorf("source link was replaced: %s", path)
		}
	}
	destinationReady := false
	if !absent(dst.Library) {
		digest, err := skills.Digest(dst.Library)
		if err != nil || digest != plan.Digest {
			return fmt.Errorf("destination already exists with different content: %s", dst.Library)
		}
		destinationReady = true
	}
	if !destinationReady {
		if current, err := identity(src.Library); err != nil || current != plan.Identity {
			return fmt.Errorf("source folder changed or disappeared: %s", src.Library)
		}
		if err := verifyDigest(src.Library, plan.Digest); err != nil {
			return err
		}
	}
	for _, path := range src.Links {
		if err := unlink(path, src.Library); err != nil {
			return err
		}
	}
	if !destinationReady {
		if plan.Action == "move" {
			if err := move(src.Library, dst.Library, plan.Identity); err != nil {
				return err
			}
		} else if err := copyPackage(src.Library, dst.Library, plan.Digest); err != nil {
			return err
		}
	}
	if plan.Action == "copy" && src.Enabled {
		for _, path := range src.Links {
			if err := source.link(path, src.Library); err != nil {
				return err
			}
		}
	}
	if dst.Enabled {
		for _, path := range dst.Links {
			if err := destination.link(path, dst.Library); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyPackage(from, to, expectedDigest string) error {
	if err := mkdir(filepath.Dir(to)); err != nil {
		return err
	}
	store := filepath.Dir(filepath.Dir(filepath.Dir(to)))
	temp, err := os.MkdirTemp(store, ".skmr-copy-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err = copyTree(from, temp); err != nil {
		return err
	}
	if err = verifyDigest(temp, expectedDigest); err != nil {
		return err
	}
	if err = renameExclusive(temp, to); err != nil {
		return err
	}
	return syncDir(filepath.Dir(to))
}

func copyTree(from, to string) error {
	root, err := os.Lstat(from)
	if err != nil {
		return err
	}
	type directoryMode struct {
		path string
		mode os.FileMode
	}
	directories := []directoryMode{{to, root.Mode().Perm()}}
	err = filepath.WalkDir(from, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == from {
			return nil
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			if err := os.Mkdir(target, 0700); err != nil {
				return err
			}
			directories = append(directories, directoryMode{target, info.Mode().Perm()})
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported package entry: %s", path)
		}
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err = os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(to, mode)
}
