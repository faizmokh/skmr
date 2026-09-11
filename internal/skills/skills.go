// Package skills parses and discovers directory-based Agent Skills.
package skills

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/faizmokh/skmr/internal/agents"
	"gopkg.in/yaml.v3"
)

type Skill struct {
	Group         *Group   `json:"group,omitempty"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Path          string   `json:"path"`
	Scope         string   `json:"scope"`
	OwnerProject  string   `json:"owner_project,omitempty"`
	Agents        []string `json:"agents"`
	Managed       bool     `json:"managed"`
	Enabled       bool     `json:"enabled"`
	Inherited     bool     `json:"inherited"`
	ReadOnly      bool     `json:"read_only"`
	Issues        []string `json:"issues"`
	ConflictID    string   `json:"conflict_id,omitempty"`
	ConflictKind  string   `json:"conflict_kind,omitempty"`
	ConflictCount int      `json:"conflict_count,omitempty"`
	Canonical     bool     `json:"canonical,omitempty"`
	Unresolved    []string `json:"unresolved_paths,omitempty"`
}

type Result struct {
	Skills    []Skill                     `json:"skills"`
	Issues    []string                    `json:"issues"`
	Snapshots map[string]*PackageSnapshot `json:"-"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const MaxDocument = 1024 * 1024

func ID(path string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(path)))[:12] }

func Read(path string) ([]byte, error) {
	f, err := os.OpenFile(filepath.Join(path, "SKILL.md"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("SKILL.md must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxDocument+1))
	if len(b) > MaxDocument {
		return nil, fmt.Errorf("SKILL.md exceeds 1 MiB")
	}
	return b, err
}

// Digest identifies package content and structure without depending on
// permissions, timestamps, ownership, or the package's absolute location.
func Digest(path string) (string, error) {
	snapshot, err := Snapshot(path)
	if err != nil {
		return "", err
	}
	return snapshot.Digest()
}

// Differences returns stable, relative package differences from canonical to copy.
func Differences(canonical, copy string) ([]string, error) {
	left, err := Snapshot(canonical)
	if err != nil {
		return nil, err
	}
	right, err := Snapshot(copy)
	if err != nil {
		return nil, err
	}
	return SnapshotDifferences(left, right)
}

type packageEntry struct {
	kind    string
	size    int64
	target  string
	digest  string
	hashed  bool
	regular bool
	symlink bool
}

// PackageSnapshot caches package metadata and content hashes for one operation.
// It is excluded from Result JSON and must not be reused after the filesystem changes.
type PackageSnapshot struct {
	root    string
	entries map[string]packageEntry
}

// Snapshot reads package structure and sizes without reading regular-file contents.
func Snapshot(path string) (*PackageSnapshot, error) {
	snapshot := &PackageSnapshot{root: path, entries: map[string]packageEntry{}}
	err := filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := packageEntry{kind: info.Mode().Type().String(), regular: info.Mode().IsRegular(), symlink: info.Mode()&os.ModeSymlink != 0}
		if item.regular {
			item.size = info.Size()
		}
		if item.symlink {
			item.target, err = os.Readlink(current)
			if err != nil {
				return err
			}
		}
		snapshot.entries[rel] = item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

// MetadataDigest identifies paths, entry types, file sizes, and symlink targets.
func (s *PackageSnapshot) MetadataDigest() string {
	h := sha256.New()
	for _, rel := range sortedEntryPaths(s.entries) {
		item := s.entries[rel]
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%s\x00", rel, item.kind, item.size, item.target)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Digest reads regular files once and caches their content hashes.
func (s *PackageSnapshot) Digest() (string, error) {
	return s.digest(false)
}

// ContentDigest excludes agent configuration for copy classification only.
// Transaction verification must continue using Digest.
func (s *PackageSnapshot) ContentDigest() (string, error) {
	return s.digest(true)
}

func (s *PackageSnapshot) digest(contentOnly bool) (string, error) {
	configPath := filepath.Join("agents", "openai.yaml")
	config, hasConfig := s.entries[configPath]
	hasConfig = hasConfig && (config.regular || config.symlink)
	configOnlyDirectory := hasConfig
	for rel := range s.entries {
		if strings.HasPrefix(rel, "agents"+string(filepath.Separator)) && rel != configPath {
			configOnlyDirectory = false
		}
	}
	h := sha256.New()
	for _, rel := range sortedEntryPaths(s.entries) {
		item := s.entries[rel]
		if contentOnly && hasConfig && (rel == configPath || (rel == "agents" && configOnlyDirectory)) {
			continue
		}
		value := item.kind + ":"
		if item.symlink {
			value += item.target
		} else if item.regular {
			digest, err := s.fileDigest(rel)
			if err != nil {
				return "", err
			}
			value += digest
		}
		fmt.Fprintf(h, "%s\x00%s\x00", rel, value)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// SnapshotDifferences compares snapshots and reuses any hashes already read.
func SnapshotDifferences(left, right *PackageSnapshot) ([]string, error) {
	paths := map[string]bool{}
	for path := range left.entries {
		paths[path] = true
	}
	for path := range right.entries {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		if path != "." {
			ordered = append(ordered, path)
		}
	}
	sort.Strings(ordered)
	differences := []string{}
	for _, path := range ordered {
		leftValue, inLeft := left.entries[path]
		rightValue, inRight := right.entries[path]
		switch {
		case !inLeft:
			differences = append(differences, "only in copy: "+path)
		case !inRight:
			differences = append(differences, "only in canonical: "+path)
		case leftValue.kind != rightValue.kind || leftValue.size != rightValue.size || leftValue.target != rightValue.target:
			differences = append(differences, "changed: "+path)
		case leftValue.regular:
			leftDigest, err := left.fileDigest(path)
			if err != nil {
				return nil, err
			}
			rightDigest, err := right.fileDigest(path)
			if err != nil {
				return nil, err
			}
			if leftDigest != rightDigest {
				differences = append(differences, "changed: "+path)
			}
		}
	}
	return differences, nil
}

func sortedEntryPaths(entries map[string]packageEntry) []string {
	paths := make([]string, 0, len(entries))
	for rel := range entries {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths
}

func (s *PackageSnapshot) fileDigest(rel string) (string, error) {
	item := s.entries[rel]
	if item.hashed {
		return item.digest, nil
	}
	path := filepath.Join(s.root, rel)
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() != item.size {
		file.Close()
		return "", fmt.Errorf("package changed while comparing: %s", path)
	}
	contentHash := sha256.New()
	_, copyErr := io.Copy(contentHash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	item.digest = fmt.Sprintf("%x", contentHash.Sum(nil))
	item.hashed = true
	s.entries[rel] = item
	return item.digest, nil
}

func Parse(path string) Skill {
	s := Skill{ID: ID(path), Path: path, Name: filepath.Base(path), Agents: []string{}, Issues: []string{}}
	data, err := Read(path)
	if err != nil {
		s.Issues = append(s.Issues, err.Error())
		return s
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 3 || lines[0] != "---" {
		s.Issues = append(s.Issues, "SKILL.md needs YAML frontmatter")
		return s
	}
	end := 0
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == 0 {
		s.Issues = append(s.Issues, "YAML frontmatter is not closed")
		return s
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.NewDecoder(bytes.NewBufferString(strings.Join(lines[1:end], "\n"))).Decode(&meta); err != nil {
		s.Issues = append(s.Issues, "Invalid frontmatter: "+err.Error())
		return s
	}
	if meta.Name != "" {
		s.Name = meta.Name
	}
	s.Description = meta.Description
	if !namePattern.MatchString(meta.Name) || len(meta.Name) > 64 {
		s.Issues = append(s.Issues, "Name must be 1–64 lowercase letters, numbers, or single hyphens")
	}
	if len(meta.Description) == 0 || len([]rune(meta.Description)) > 1024 {
		s.Issues = append(s.Issues, "Description must contain 1–1024 characters")
	}
	if meta.Name != filepath.Base(path) {
		s.Issues = append(s.Issues, "Name must match the folder for cross-agent compatibility")
	}
	return s
}

// Scan follows symlink directories but tracks ancestors per traversal, preserving
// distinct discovery paths while preventing loops. Supporting files are not scanned.
func Scan(roots []agents.Root) Result {
	out := Result{Skills: []Skill{}, Issues: []string{}}
	seen := map[string]bool{}
	for _, root := range roots {
		var walk func(string, map[string]bool, bool, int)
		walk = func(path string, stack map[string]bool, linked bool, depth int) {
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				return
			}
			if err != nil {
				out.Issues = append(out.Issues, path+": "+err.Error())
				return
			}
			linked = linked || info.Mode()&os.ModeSymlink != 0
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				out.Issues = append(out.Issues, path+": "+err.Error())
				return
			}
			info, err = os.Stat(path)
			if err != nil || !info.IsDir() {
				return
			}
			if stack[real] {
				out.Issues = append(out.Issues, "Symlink cycle: "+path)
				return
			}
			if depth > 64 {
				out.Issues = append(out.Issues, "Discovery depth exceeded: "+path)
				return
			}
			if _, err := os.Lstat(filepath.Join(path, "SKILL.md")); err == nil {
				if seen[path] {
					return
				}
				seen[path] = true
				s := Parse(path)
				s.Group = GroupFor(path, roots)
				s.Scope = root.Scope
				s.Agents = append([]string{}, root.Agents...)
				s.Inherited = root.Inherited
				s.Enabled = true
				rel, _ := filepath.Rel(root.Path, path)
				s.ReadOnly = root.ReadOnly || linked || root.Inherited || strings.HasPrefix(rel, ".system"+string(filepath.Separator)) || rel == ".system"
				out.Skills = append(out.Skills, s)
				return
			}
			stack[real] = true
			defer delete(stack, real)
			entries, err := os.ReadDir(path)
			if err != nil {
				out.Issues = append(out.Issues, path+": "+err.Error())
				return
			}
			for _, entry := range entries {
				switch entry.Name() {
				case ".git", "node_modules":
					continue
				}
				walk(filepath.Join(path, entry.Name()), stack, linked, depth+1)
			}
		}
		walk(root.Path, map[string]bool{}, false, 0)
	}
	sort.Slice(out.Skills, func(i, j int) bool {
		if out.Skills[i].Name == out.Skills[j].Name {
			return out.Skills[i].Path < out.Skills[j].Path
		}
		return out.Skills[i].Name < out.Skills[j].Name
	})
	return out
}
