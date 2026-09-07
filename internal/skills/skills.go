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
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Scope       string   `json:"scope"`
	Agents      []string `json:"agents"`
	Managed     bool     `json:"managed"`
	Enabled     bool     `json:"enabled"`
	Inherited   bool     `json:"inherited"`
	ReadOnly    bool     `json:"read_only"`
	Issues      []string `json:"issues"`
}

type Result struct {
	Skills []Skill  `json:"skills"`
	Issues []string `json:"issues"`
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
