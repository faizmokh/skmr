package manager

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/faizmokh/skmr/internal/agents"
)

type Config struct {
	Home       string
	DataHome   string
	ConfigHome string
	Project    string
}

type Service struct {
	Config    Config
	Store     string
	Roots     []agents.Root
	ancestors []string
}

func Environment(project string) (*Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return New(Config{Home: home, DataHome: os.Getenv("XDG_DATA_HOME"), ConfigHome: os.Getenv("XDG_CONFIG_HOME"), Project: project})
}

func New(c Config) (*Service, error) {
	var err error
	if c.Home == "" {
		return nil, fmt.Errorf("home directory is required")
	}
	c.Home, err = filepath.Abs(c.Home)
	if err != nil {
		return nil, err
	}
	if c.DataHome == "" {
		c.DataHome = filepath.Join(c.Home, ".local", "share")
	}
	if c.ConfigHome == "" {
		c.ConfigHome = filepath.Join(c.Home, ".config")
	}
	if !filepath.IsAbs(c.DataHome) || !filepath.IsAbs(c.ConfigHome) {
		return nil, fmt.Errorf("XDG directories must be absolute")
	}
	s := &Service{Config: c, Store: filepath.Join(c.DataHome, "skmr"), Roots: agents.Global(c.Home, c.ConfigHome)}
	if c.Project != "" {
		p := c.Project
		if p == "auto" {
			cwd, e := os.Getwd()
			if e != nil {
				return nil, e
			}
			p = gitRoot(cwd)
			if p == "" {
				return nil, fmt.Errorf("outside Git: supply --project <path>")
			}
		}
		p, err = filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		p, err = filepath.EvalSymlinks(p)
		if err != nil {
			return nil, err
		}
		info, e := os.Stat(p)
		if e != nil {
			return nil, e
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("project must be a directory")
		}
		s.Config.Project = p
		s.Store = filepath.Join(p, ".skmr")
		for i := range s.Roots {
			s.Roots[i].Inherited = true
			s.Roots[i].ReadOnly = true
		}
		s.Roots = append(s.Roots, agents.Project(p, false)...)
		boundary := gitRoot(p)
		for parent := filepath.Dir(p); p != boundary && parent != p; parent = filepath.Dir(parent) {
			s.Roots = append(s.Roots, agents.Project(parent, true)...)
			s.ancestors = append(s.ancestors, parent)
			if parent == boundary || parent == filepath.Dir(parent) {
				break
			}
		}
	}
	return s, nil
}

func gitRoot(path string) string {
	for {
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			return path
		}
		next := filepath.Dir(path)
		if next == path {
			return ""
		}
		path = next
	}
}
func (s *Service) Scope() string {
	if s.Config.Project != "" {
		return "project"
	}
	return "global"
}
func (s *Service) Shared() string {
	base := s.Config.Home
	if s.Config.Project != "" {
		base = s.Config.Project
	}
	return filepath.Join(base, ".agents", "skills")
}
func (s *Service) hasPending() bool {
	return !absent(filepath.Join(s.Store, "journal.json")) || !absent(filepath.Join(s.Store, "batch.json")) || !absent(filepath.Join(s.Store, "transfer.json")) || !absent(filepath.Join(s.Store, "packages-journal.json"))
}
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !startsParent(rel)
}
func startsParent(s string) bool { return len(s) > 3 && s[:3] == ".."+string(filepath.Separator) }
