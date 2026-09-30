package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type projectRegistry struct {
	Version  int      `json:"version"`
	Projects []string `json:"projects"`
}

func (s *Service) registryPath() string { return filepath.Join(s.centralStore(), "projects.json") }

func (s *Service) loadProjectRegistry() (projectRegistry, error) {
	r := projectRegistry{Version: 1, Projects: []string{}}
	b, err := os.ReadFile(s.registryPath())
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("read project registry: %w", err)
	}
	if r.Version != 1 {
		return r, fmt.Errorf("unsupported project registry version %d", r.Version)
	}
	for _, path := range r.Projects {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return r, fmt.Errorf("invalid registered project %q", path)
		}
	}
	return r, nil
}

// Called while the project and central library locks are held. Replaying is safe.
func (s *Service) registerPackages(after PackageManifest) error {
	r, err := s.loadProjectRegistry()
	if err != nil {
		return err
	}
	paths := map[string]bool{}
	for _, path := range r.Projects {
		paths[path] = true
	}
	if len(after.Skills) == 0 && len(after.Requests) == 0 {
		delete(paths, s.Config.Project)
	} else {
		paths[s.Config.Project] = true
	}
	r.Projects = r.Projects[:0]
	for path := range paths {
		r.Projects = append(r.Projects, path)
	}
	sort.Strings(r.Projects)
	return atomicJSON(s.registryPath(), r)
}

func (s *Service) deletionDependencies(record Record) ([]string, error) {
	if s.Config.Project != "" {
		return nil, nil
	}
	var dependencies []string
	groups, err := s.loadGroups()
	if err != nil {
		return nil, err
	}
	for _, group := range groups.Groups {
		for _, member := range group.Members {
			if member == record.Name {
				dependencies = append(dependencies, "group @"+group.Name)
				break
			}
		}
	}
	registry, err := s.loadProjectRegistry()
	if err != nil {
		return nil, err
	}
	for _, path := range registry.Projects {
		project, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome, Project: path})
		if err != nil {
			return nil, fmt.Errorf("cannot check registered project %s: %w", path, err)
		}
		if project.Config.Project != path {
			return nil, fmt.Errorf("registered project path changed: %s", path)
		}
		packages, err := project.loadPackages()
		if err != nil {
			return nil, fmt.Errorf("cannot check registered project %s: %w", path, err)
		}
		for _, installed := range packages.Skills {
			if installed.ID == record.ID {
				dependencies = append(dependencies, "project "+path)
				break
			}
		}
		if contains(packages.Requests, record.Name) && !contains(dependencies, "project "+path) {
			dependencies = append(dependencies, "project "+path)
		}
	}
	return dependencies, nil
}
