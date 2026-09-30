package manager

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/faizmokh/skmr/internal/agents"
)

const (
	legacyVersion = 1
	Version       = 2
)

type Origin struct {
	Path      string `json:"path"`
	Backup    string `json:"backup,omitempty"`
	Canonical bool   `json:"canonical"`
}

type Record struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Original     string           `json:"original"`
	Library      string           `json:"library"`
	Links        []string         `json:"links"`
	Enabled      bool             `json:"enabled"`
	Origins      []Origin         `json:"origins,omitempty"`
	Remote       *RemoteSource    `json:"remote,omitempty"`
	MigratedFrom *MigrationOrigin `json:"migrated_from,omitempty"`
	ImportedFrom string           `json:"imported_from,omitempty"`
}

// MigrationOrigin preserves the restore destination of a project-owned skill
// after its content moves into the personal library.
type MigrationOrigin struct {
	Project string   `json:"project"`
	Path    string   `json:"path"`
	Origins []Origin `json:"origins,omitempty"`
}

// RemoteSource identifies content imported from an internet source.
type RemoteSource struct {
	URL      string `json:"url"`
	Skill    string `json:"skill"`
	Digest   string `json:"digest"`
	Revision string `json:"revision,omitempty"`
}
type Manifest struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
	// Returned origins remain outside automatic ownership until explicitly added.
	Returned []string `json:"returned,omitempty"`
}

func cloneManifest(m Manifest) Manifest {
	m.Version = Version
	m.Records = append([]Record{}, m.Records...)
	m.Returned = append([]string(nil), m.Returned...)
	return m
}

func forgetReturned(m *Manifest, paths ...string) {
	var kept []string
	for _, path := range m.Returned {
		forget := false
		for _, adopted := range paths {
			forget = forget || path == adopted
		}
		if !forget {
			kept = append(kept, path)
		}
	}
	m.Returned = kept
}

func validateReturned(paths []string) error {
	seen := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || seen[path] {
			return fmt.Errorf("invalid returned origin %q", path)
		}
		seen[path] = true
	}
	return nil
}

type Identity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func identity(path string) (Identity, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return Identity{}, e
	}
	v, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, fmt.Errorf("unsupported filesystem")
	}
	return Identity{uint64(v.Dev), v.Ino}, nil
}

func (s *Service) load() (Manifest, error) {
	m := Manifest{Version: Version, Records: []Record{}}
	b, err := os.ReadFile(filepath.Join(s.Store, "manifest.json"))
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("read manifest: %w", err)
	}
	if m.Version != legacyVersion && m.Version != Version {
		return m, fmt.Errorf("unsupported manifest version %d", m.Version)
	}
	legacy := m.Version == legacyVersion
	m.Version = Version
	if err := validateReturned(m.Returned); err != nil {
		return m, err
	}
	ids := map[string]bool{}
	for i := range m.Records {
		r := &m.Records[i]
		if s.Config.Project != "" {
			r.Original = s.absolute(r.Original)
			r.Library = s.absolute(r.Library)
			for j := range r.Links {
				r.Links[j] = s.absolute(r.Links[j])
			}
			for j := range r.Origins {
				r.Origins[j].Path = s.absolute(r.Origins[j].Path)
				if r.Origins[j].Backup != "" {
					r.Origins[j].Backup = s.absolute(r.Origins[j].Backup)
				}
			}
		}
		if legacy {
			r.Origins = []Origin{{Path: r.Original, Canonical: true}}
		}
		if err := s.validate(*r); err != nil {
			return m, err
		}
		if ids[r.ID] {
			return m, fmt.Errorf("duplicate manifest ID %s", r.ID)
		}
		ids[r.ID] = true
	}
	return m, nil
}
func (s *Service) absolute(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.Config.Project, p)
}
func (s *Service) validate(r Record) error {
	if r.ID == "" || filepath.Base(r.ID) != r.ID || r.ID == "." || r.ID == ".." || r.Name == "" || filepath.Base(r.Name) != r.Name || r.Name == "." || r.Name == ".." {
		return fmt.Errorf("invalid manifest record")
	}
	if r.Library != filepath.Join(s.Store, "library", r.ID, r.Name) {
		return fmt.Errorf("invalid library path for %s", r.ID)
	}
	if r.ImportedFrom != "" && (s.Config.Project != "" || !filepath.IsAbs(r.ImportedFrom) || filepath.Clean(r.ImportedFrom) != r.ImportedFrom || r.Original != filepath.Join(s.Shared(), r.Name) || r.MigratedFrom != nil || r.Remote != nil) {
		return fmt.Errorf("invalid local import source for %s", r.ID)
	}
	if r.MigratedFrom != nil {
		origin := r.MigratedFrom
		if s.Config.Project != "" || !filepath.IsAbs(origin.Project) || !filepath.IsAbs(origin.Path) || filepath.Clean(origin.Project) != origin.Project || filepath.Clean(origin.Path) != origin.Path || !within(origin.Project, origin.Path) {
			return fmt.Errorf("invalid migration origin for %s", r.ID)
		}
		allowed := false
		for _, root := range agents.Project(origin.Project, false) {
			allowed = allowed || within(root.Path, origin.Path) && origin.Path != root.Path
		}
		if !allowed {
			return fmt.Errorf("invalid migration origin for %s", r.ID)
		}
		canonical := 0
		seen := map[string]bool{}
		for _, old := range origin.Origins {
			permitted := false
			for _, root := range agents.Project(origin.Project, false) {
				permitted = permitted || old.Path != root.Path && within(root.Path, old.Path)
			}
			if !permitted || seen[old.Path] || !filepath.IsAbs(old.Path) {
				return fmt.Errorf("invalid migrated copy origin for %s", r.ID)
			}
			seen[old.Path] = true
			if old.Canonical {
				canonical++
				if old.Path != origin.Path || old.Backup != "" {
					return fmt.Errorf("invalid migrated canonical copy")
				}
			} else if old.Backup == "" || !within(filepath.Join(s.Store, "library", r.ID, ".skmr-duplicates"), old.Backup) {
				return fmt.Errorf("invalid migrated duplicate backup")
			}
		}
		if len(origin.Origins) > 0 && canonical != 1 {
			return fmt.Errorf("missing migrated canonical copy")
		}
	}
	if r.Remote != nil {
		_, _, sourceErr := normalizeRemote(r.Remote.URL)
		_, digestErr := hex.DecodeString(r.Remote.Digest)
		if s.Config.Project != "" || sourceErr != nil || r.Remote.Skill != r.Name || len(r.Remote.Digest) != 64 || digestErr != nil || r.Original != "" || len(r.Origins) != 0 || len(r.Links) != 1 || r.Links[0] != filepath.Join(s.Shared(), r.Name) {
			return fmt.Errorf("invalid remote record for %s", r.ID)
		}
		return nil
	}
	allowed := false
	for _, root := range s.Roots {
		if !root.ReadOnly && !root.Inherited && within(root.Path, r.Original) && r.Original != root.Path {
			allowed = true
		}
	}
	if !allowed || !filepath.IsAbs(r.Original) {
		return fmt.Errorf("original path is outside writable discovery roots: %s", r.Original)
	}
	shared := filepath.Join(s.Shared(), r.Name)
	if len(r.Links) == 0 || len(r.Links) > 2 {
		return fmt.Errorf("invalid owned links for %s", r.ID)
	}
	foundOriginal, foundShared := false, false
	seen := map[string]bool{}
	for _, p := range r.Links {
		if p != r.Original && p != shared {
			return fmt.Errorf("invalid owned link: %s", p)
		}
		if seen[p] {
			return fmt.Errorf("duplicate owned link: %s", p)
		}
		seen[p] = true
		foundOriginal = foundOriginal || p == r.Original
		foundShared = foundShared || p == shared
	}
	if !foundShared || len(r.Links) == 2 && !foundOriginal {
		return fmt.Errorf("missing owned link for %s", r.ID)
	}
	canonical := 0
	origins := map[string]bool{}
	for _, origin := range r.Origins {
		if origin.Path == "" || origins[origin.Path] {
			return fmt.Errorf("invalid origin for %s", r.ID)
		}
		origins[origin.Path] = true
		originAllowed := false
		for _, root := range s.Roots {
			if !root.ReadOnly && !root.Inherited && within(root.Path, origin.Path) && origin.Path != root.Path {
				originAllowed = true
			}
		}
		if !originAllowed || !filepath.IsAbs(origin.Path) {
			return fmt.Errorf("origin path is outside writable discovery roots: %s", origin.Path)
		}
		if origin.Canonical {
			canonical++
			if origin.Path != r.Original || origin.Backup != "" {
				return fmt.Errorf("invalid kept copy for %s", r.ID)
			}
		} else if origin.Backup == "" || !within(filepath.Join(s.Store, "library", r.ID, ".skmr-duplicates"), origin.Backup) {
			return fmt.Errorf("invalid duplicate backup for %s", r.ID)
		}
	}
	if canonical != 1 {
		return fmt.Errorf("missing kept copy for %s", r.ID)
	}
	return nil
}
func (s *Service) save(m Manifest) error {
	// Clone before converting paths: callers retain absolute runtime paths.
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var disk Manifest
	if err = json.Unmarshal(b, &disk); err != nil {
		return err
	}
	if s.Config.Project != "" {
		for i := range disk.Records {
			r := &disk.Records[i]
			r.Original, _ = filepath.Rel(s.Config.Project, r.Original)
			r.Library, _ = filepath.Rel(s.Config.Project, r.Library)
			for j := range r.Links {
				r.Links[j], _ = filepath.Rel(s.Config.Project, r.Links[j])
			}
			for j := range r.Origins {
				r.Origins[j].Path, _ = filepath.Rel(s.Config.Project, r.Origins[j].Path)
				if r.Origins[j].Backup != "" {
					r.Origins[j].Backup, _ = filepath.Rel(s.Config.Project, r.Origins[j].Backup)
				}
			}
		}
	}
	return atomicJSON(filepath.Join(s.Store, "manifest.json"), disk)
}
func atomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".skmr-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Reject symlink parents so a replaced discovery directory cannot redirect writes.
func realParents(path string) error {
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("parent is not a real directory: %s", p)
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func mkdir(path string) error {
	if err := realParents(filepath.Join(path, "placeholder")); err != nil {
		return err
	}
	return os.MkdirAll(path, 0755)
}
func (s *Service) lock() (func(), error) {
	if err := mkdir(s.Store); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(filepath.Join(s.Store, ".lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("another skmr operation is running: %w", err)
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = syscall.Close(fd) }, nil
}
func absent(p string) bool { _, err := os.Lstat(p); return os.IsNotExist(err) }
func owned(path, target string) bool {
	link, err := os.Readlink(path)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(path), link)
	}
	return filepath.Clean(link) == filepath.Clean(target)
}
func available(path string) error {
	_, err := os.Lstat(path)
	if err == nil {
		return fmt.Errorf("destination already exists: %s", path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return realParents(path)
}

// Detect cross-filesystem moves in the read-only preview, before journaling.
func sameDevice(source, destination string) error {
	src, err := identity(source)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	for {
		dst, err := identity(parent)
		if err == nil {
			if dst.Device != src.Device {
				return fmt.Errorf("source and library must be on the same filesystem; choose XDG_DATA_HOME on the source filesystem")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return err
		}
		parent = next
	}
}
