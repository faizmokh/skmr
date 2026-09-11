package skills

import (
	"path/filepath"
	"strings"

	"github.com/faizmokh/skmr/internal/agents"
)

// Group describes a discovery bundle, not a unit of management.
type Group struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Scope       string `json:"scope"`
	Version     string `json:"version,omitempty"`
	Marketplace string `json:"marketplace,omitempty"`
}

// GroupFor uses lexical discovery paths so symlinks retain their source identity.
func GroupFor(path string, roots []agents.Root) *Group {
	var root *agents.Root
	for i := range roots {
		rel, err := filepath.Rel(roots[i].Path, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == "." {
			continue
		}
		if root == nil || len(roots[i].Path) > len(root.Path) {
			root = &roots[i]
		}
	}
	if root == nil {
		return nil
	}
	rel, _ := filepath.Rel(root.Path, filepath.Dir(path))
	if rel == "." {
		return nil
	}
	group := &Group{Name: filepath.ToSlash(rel), Source: filepath.Dir(path), Scope: root.Scope}
	parts := strings.Split(rel, string(filepath.Separator))
	if filepath.Base(root.Path) == "cache" && filepath.Base(filepath.Dir(root.Path)) == "plugins" && len(parts) >= 3 {
		group.Marketplace, group.Name, group.Version = parts[0], parts[1], parts[2]
		group.Source = filepath.Join(root.Path, parts[0], parts[1], parts[2])
	}
	group.ID = ID(root.Scope + ":" + root.Path + ":" + group.Source)
	return group
}
