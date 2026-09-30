package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/faizmokh/skmr/internal/skills"
)

// OperationRequest is the common command model. Scope is selected by Service;
// callers use the same request for previews in the CLI and TUI.
type OperationRequest struct {
	Action    string
	Arguments []string
	Skills    []string
	Replace   bool
}

func (s *Service) PreviewOperation(r OperationRequest) (OperationPlan, error) {
	if s.hasPending() {
		return OperationPlan{}, fmt.Errorf("recover the interrupted operation before opening this scope")
	}
	switch r.Action {
	case "add":
		if len(r.Arguments) == 0 {
			return OperationPlan{}, fmt.Errorf("provide a skill, local path, URL, or @group")
		}
		if len(r.Arguments) == 1 && strings.Contains(r.Arguments[0], "://") {
			return s.previewRemotePlacement(r.Arguments[0], r.Skills)
		}
		if len(r.Skills) > 0 {
			return OperationPlan{}, fmt.Errorf("skill selections require a remote URL")
		}
		if len(r.Arguments) == 1 && localArgument(r.Arguments[0]) {
			return s.previewLocalPlacement(r.Arguments[0])
		}
		if s.Config.Project == "" {
			return s.previewGlobalPlacement("enable", r.Arguments)
		}
		p, err := s.PreviewPackages("add", r.Arguments)
		if err != nil {
			return OperationPlan{}, err
		}
		return s.operationFromPackages(p)
	case "remove", "sync":
		if s.Config.Project == "" {
			if r.Action == "sync" {
				return OperationPlan{}, fmt.Errorf("sync requires a project")
			}
			return s.previewGlobalPlacement("disable", r.Arguments)
		}
		p, err := s.PreviewPackages(r.Action, r.Arguments)
		if err != nil {
			return OperationPlan{}, err
		}
		return s.operationFromPackages(p)
	case "adopt-batch":
		if s.Config.Project == "" {
			p, err := s.PreviewBatchAdopt(r.Arguments)
			if err != nil {
				return OperationPlan{}, err
			}
			return s.operationFromBatch(p)
		}
		return s.previewProjectAdoption(r.Arguments)
	case "adopt":
		if len(r.Arguments) != 1 {
			return OperationPlan{}, fmt.Errorf("adopt requires one path")
		}
		return s.previewLocalPlacement(r.Arguments[0])
	case "enable", "disable", "restore", "resolve":
		if len(r.Arguments) != 1 {
			return OperationPlan{}, fmt.Errorf("%s requires one skill", r.Action)
		}
		if r.Action == "resolve" && s.Config.Project != "" {
			inventory, err := s.List()
			if err != nil {
				return OperationPlan{}, err
			}
			for _, item := range inventory.Skills {
				if item.ID == r.Arguments[0] && !item.Inherited && !item.ReadOnly {
					op, err := s.previewProjectAdoption([]string{item.Path})
					op.Action = "resolve"
					return op, err
				}
			}
			return OperationPlan{}, fmt.Errorf("select a writable project copy to keep")
		}
		p, err := s.Preview(r.Action, r.Arguments[0])
		if err != nil {
			return OperationPlan{}, err
		}
		return s.operationFromPlan(p)
	case "delete":
		if len(r.Arguments) != 1 {
			return OperationPlan{}, fmt.Errorf("delete requires one skill")
		}
		p, err := s.PreviewDelete(r.Arguments[0])
		if err != nil {
			return OperationPlan{}, err
		}
		return s.operationFromDelete(p), nil
	case "update":
		if len(r.Arguments) != 1 {
			return OperationPlan{}, fmt.Errorf("update requires one skill")
		}
		p, err := s.PrepareUpdate(r.Arguments[0], r.Replace)
		if err != nil {
			return OperationPlan{}, err
		}
		op, err := s.operationFromRemote(p)
		if err != nil {
			p.Cleanup()
			return op, err
		}
		op.Temporary = []string{p.Stage}
		return op, nil
	case "group-create", "group-delete":
		return s.previewGroupOperation(r)
	}
	return OperationPlan{}, fmt.Errorf("unknown operation %q", r.Action)
}

func (s *Service) previewGroupOperation(r OperationRequest) (OperationPlan, error) {
	if len(r.Arguments) == 0 || !packageNamePattern.MatchString(r.Arguments[0]) {
		return OperationPlan{}, fmt.Errorf("provide a valid group name")
	}
	name := r.Arguments[0]
	before, err := s.loadGroups()
	if err != nil {
		return OperationPlan{}, err
	}
	after := groupManifest{Version: before.Version, Groups: append([]InstallGroup{}, before.Groups...)}
	global, err := s.operationService("")
	if err != nil {
		return OperationPlan{}, err
	}
	library, err := global.load()
	if err != nil {
		return OperationPlan{}, err
	}
	op := operationPlan(r.Action, "")
	if r.Action == "group-delete" {
		if len(r.Arguments) != 1 {
			return op, fmt.Errorf("group delete takes one name")
		}
		found := false
		after.Groups = []InstallGroup{}
		for _, group := range before.Groups {
			if group.Name == name {
				found = true
			} else {
				after.Groups = append(after.Groups, group)
			}
		}
		if !found {
			return op, fmt.Errorf("group %q was not found", name)
		}
		op.Description = "Delete preset @" + name + "; existing project requests stay as saved"
	} else {
		if len(r.Arguments) < 2 {
			return op, fmt.Errorf("provide at least one skill or @group member")
		}
		for _, group := range before.Groups {
			if group.Name == name {
				return op, fmt.Errorf("group %q already exists", name)
			}
		}
		for _, member := range r.Arguments[1:] {
			if member == "@"+name {
				return op, fmt.Errorf("group %q cannot contain itself", name)
			}
		}
		members, err := s.expandPackageRequests(r.Arguments[1:])
		if err != nil {
			return op, err
		}
		known := map[string]bool{}
		for _, record := range library.Records {
			known[record.Name] = true
		}
		for _, member := range members {
			if !known[member] {
				return op, fmt.Errorf("skill %q is not in the central library; adopt it first", member)
			}
		}
		after.Groups = append(after.Groups, InstallGroup{Name: name, Members: members})
		sort.Slice(after.Groups, func(i, j int) bool { return after.Groups[i].Name < after.Groups[j].Name })
		op.Description = "Create preset @" + name + ": " + strings.Join(members, ", ")
	}
	op.Scopes = []OperationScope{{LibraryBefore: &library, LibraryAfter: &library, GroupsBefore: &before, GroupsAfter: &after}}
	return op, nil
}

func localArgument(value string) bool {
	if filepath.IsAbs(value) || strings.HasPrefix(value, ".") || strings.ContainsRune(value, filepath.Separator) {
		return true
	}
	info, err := os.Stat(value)
	return err == nil && info.IsDir()
}

func (s *Service) previewGlobalPlacement(action string, arguments []string) (OperationPlan, error) {
	if len(arguments) == 0 {
		return OperationPlan{}, fmt.Errorf("select at least one skill")
	}
	groups, err := s.loadGroups()
	if err != nil {
		return OperationPlan{}, err
	}
	var names []string
	for _, arg := range arguments {
		if !strings.HasPrefix(arg, "@") {
			names = append(names, arg)
			continue
		}
		found := false
		for _, group := range groups.Groups {
			if "@"+group.Name == arg {
				names = append(names, group.Members...)
				found = true
				break
			}
		}
		if !found {
			return OperationPlan{}, fmt.Errorf("group %s was not found", arg)
		}
	}
	before, err := s.load()
	if err != nil {
		return OperationPlan{}, err
	}
	batch := BatchPlan{Version: Version, Action: action, Before: before}
	seen := map[string]bool{}
	for _, name := range uniqueStrings(names) {
		p, err := s.Preview(action, name)
		if err != nil {
			return OperationPlan{}, err
		}
		if seen[p.Record.ID] {
			continue
		}
		seen[p.Record.ID] = true
		batch.Plans = append(batch.Plans, p)
	}
	op, err := s.operationFromBatch(batch)
	if err != nil {
		return op, err
	}
	op.Action = action
	op.Description = ""
	for _, plan := range batch.Plans {
		op.Description += plan.String() + "\n"
	}
	op.Description = strings.TrimSpace(op.Description)
	op.Scopes[0].GroupsBefore, op.Scopes[0].GroupsAfter = &groups, &groups
	return op, nil
}

func (s *Service) previewRemotePlacement(url string, names []string) (OperationPlan, error) {
	if len(names) == 0 {
		return OperationPlan{}, fmt.Errorf("select at least one remote skill")
	}
	if s.Config.Project != "" {
		p, err := s.PrepareRemoteAdd(url, names)
		if err != nil {
			return OperationPlan{}, err
		}
		op, err := s.operationFromRemoteAdd(p)
		if err != nil {
			p.Cleanup()
			return op, err
		}
		op.Temporary = []string{p.Remote.Stage}
		return op, nil
	}
	p, err := s.prepareRemoteLibrary(url, names)
	if err != nil {
		return OperationPlan{}, err
	}
	op, err := s.operationFromRemote(p.Remote)
	if err != nil {
		p.Cleanup()
		return op, err
	}
	op.Action = "add"
	display := p.Remote
	display.Entries = append([]RemoteEntry{}, p.Remote.Entries...)
	for i := range display.Entries {
		display.Entries[i].Record.Enabled = true
	}
	op.Description = ""
	if len(display.Entries) > 0 {
		op.Description = display.String()
	}
	for _, name := range p.Reused {
		op.Description += "\nReuse " + name + " from personal library"
	}
	for i := range op.Scopes[0].LibraryAfter.Records {
		record := &op.Scopes[0].LibraryAfter.Records[i]
		for _, name := range names {
			if record.Name == name {
				record.Enabled = true
				for _, path := range record.Links {
					op.AddLinks = append(op.AddLinks, LinkPlan{Path: path, Target: record.Library})
					op.Description += "\nCreate link " + path
				}
			}
		}
	}
	op.Description = strings.TrimSpace(op.Description)
	op.Temporary = []string{p.Remote.Stage}
	return op, nil
}

func (s *Service) previewLocalPlacement(path string) (OperationPlan, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return OperationPlan{}, err
	}
	global, err := s.operationService("")
	if err != nil {
		return OperationPlan{}, err
	}
	library, err := global.load()
	if err != nil {
		return OperationPlan{}, err
	}
	for _, record := range library.Records {
		if absolute == record.Library || absolute == record.ImportedFrom || owned(absolute, record.Library) {
			if s.Config.Project == "" {
				return s.previewGlobalPlacement("enable", []string{record.ID})
			}
			p, err := s.PreviewPackages("add", []string{record.Name})
			if err != nil {
				return OperationPlan{}, err
			}
			return s.operationFromPackages(p)
		}
	}
	globalDiscovered := false
	for _, root := range global.Roots {
		if !within(root.Path, absolute) || absolute == root.Path {
			continue
		}
		rel, _ := filepath.Rel(root.Path, absolute)
		if root.ReadOnly || strings.Contains(string(filepath.Separator)+rel+string(filepath.Separator), string(filepath.Separator)+".system"+string(filepath.Separator)) {
			return OperationPlan{}, fmt.Errorf("built-in and plugin skills are view only")
		}
		globalDiscovered = true
	}
	if s.Config.Project != "" && globalDiscovered {
		p, err := global.Preview("adopt", path)
		if err != nil {
			return OperationPlan{}, err
		}
		op, err := global.operationFromPlan(p)
		if err != nil {
			return op, err
		}
		r := p.Record
		r.Enabled = false
		for i := range op.Scopes[0].LibraryAfter.Records {
			if op.Scopes[0].LibraryAfter.Records[i].ID == r.ID {
				op.Scopes[0].LibraryAfter.Records[i].Enabled = false
			}
		}
		op.AddLinks = nil
		placements, err := s.previewPackages("add", []string{r.Name}, []Record{r}, false)
		if err != nil {
			return op, err
		}
		op.Scopes = append(op.Scopes, OperationScope{Project: s.Config.Project, RequestsBefore: &placements.Before, RequestsAfter: &placements.After})
		op.RemoveLinks = append(op.RemoveLinks, placements.RemoveLinks...)
		for _, ref := range placements.After.Skills {
			op.AddLinks = append(op.AddLinks, LinkPlan{Path: filepath.Join(s.Shared(), ref.Name), Target: s.centralLibrary(ref)})
		}
		op.Action, op.Description = "add", ""
		for _, c := range op.Content {
			op.Description += "Move " + c.From + "\n  to " + c.To + "\n"
		}
		op.Description += placements.String()
		return op, nil
	}
	discovered := false
	for _, root := range s.Roots {
		if within(root.Path, absolute) && root.Path != absolute && !root.ReadOnly && !root.Inherited {
			discovered = true
		}
		if within(root.Path, absolute) && root.ReadOnly && !root.Inherited {
			return OperationPlan{}, fmt.Errorf("built-in and plugin skills are view only")
		}
	}
	if !discovered {
		return s.previewLocalImport(absolute)
	}
	if s.Config.Project == "" {
		p, err := s.Preview("adopt", path)
		if err != nil {
			return OperationPlan{}, err
		}
		return s.operationFromPlan(p)
	}
	return s.previewProjectAdoption([]string{path})
}

// Skills outside discovery directories are authoring sources. Import an owned
// copy while leaving the author's folder intact; adopted discovery folders use
// moves and retain their real origin instead.
func (s *Service) previewLocalImport(path string) (OperationPlan, error) {
	if err := verifyDirectory(path); err != nil {
		return OperationPlan{}, err
	}
	if err := validateRelocation(path); err != nil {
		return OperationPlan{}, err
	}
	item := skills.Parse(path)
	if len(item.Issues) > 0 {
		return OperationPlan{}, fmt.Errorf("cannot add: %s", strings.Join(item.Issues, "; "))
	}
	global, err := s.operationService("")
	if err != nil {
		return OperationPlan{}, err
	}
	before, err := global.load()
	if err != nil {
		return OperationPlan{}, err
	}
	inventory, err := global.List()
	if err != nil {
		return OperationPlan{}, err
	}
	for _, found := range inventory.Skills {
		if found.Name == item.Name {
			return OperationPlan{}, fmt.Errorf("name conflict for %s at %s", item.Name, found.Path)
		}
	}
	r := Record{ID: skills.ID(path), Name: item.Name, Original: filepath.Join(global.Shared(), item.Name), ImportedFrom: path, Enabled: s.Config.Project == ""}
	r.Library = filepath.Join(global.Store, "library", r.ID, r.Name)
	r.Links = []string{r.Original}
	r.Origins = []Origin{{Path: r.Original, Canonical: true}}
	if err := global.validate(r); err != nil {
		return OperationPlan{}, err
	}
	digest, err := skills.Digest(path)
	if err != nil {
		return OperationPlan{}, err
	}
	op := operationPlan("add", "Copy "+path+"\n  to "+r.Library)
	after := cloneManifest(before)
	after.Records = append(after.Records, r)
	forgetReturned(&after, path)
	op.Scopes = []OperationScope{{LibraryBefore: &before, LibraryAfter: &after}}
	op.Content = []ContentChange{{Kind: "copy", From: path, To: r.Library, Digest: digest}}
	if s.Config.Project == "" {
		for _, link := range r.Links {
			op.AddLinks = append(op.AddLinks, LinkPlan{Path: link, Target: r.Library})
			op.Description += "\nCreate link " + link
		}
	} else {
		packages, err := s.previewPackages("add", []string{r.Name}, []Record{r}, false)
		if err != nil {
			return op, err
		}
		op.Scopes = append(op.Scopes, OperationScope{Project: s.Config.Project, RequestsBefore: &packages.Before, RequestsAfter: &packages.After})
		op.RemoveLinks = packages.RemoveLinks
		for _, ref := range packages.After.Skills {
			op.AddLinks = append(op.AddLinks, LinkPlan{Path: filepath.Join(s.Shared(), ref.Name), Target: s.centralLibrary(ref)})
		}
		op.Description += "\n" + packages.String()
	}
	// A link to a skill outside discovery roots still has to be an actual
	// folder. verifyDirectory uses Lstat and never follows a replaced source.
	if info, err := os.Lstat(path); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return op, fmt.Errorf("local source was replaced: %s", path)
	}
	return op, nil
}

func (s *Service) previewProjectAdoption(paths []string) (OperationPlan, error) {
	if len(paths) == 0 {
		return OperationPlan{}, fmt.Errorf("select at least one skill")
	}
	global, err := s.operationService("")
	if err != nil {
		return OperationPlan{}, err
	}
	before, err := global.load()
	if err != nil {
		return OperationPlan{}, err
	}
	inventory, err := global.List()
	if err != nil {
		return OperationPlan{}, err
	}
	projectBefore, err := s.load()
	if err != nil {
		return OperationPlan{}, err
	}
	if len(projectBefore.Records) > 0 {
		return OperationPlan{}, fmt.Errorf("upgrade the project library before adding skills; run skmr list --project %s", s.Config.Project)
	}
	after := cloneManifest(before)
	op := operationPlan("add", "")
	var additions []Record
	var names []string
	for _, path := range paths {
		p, err := s.Preview("adopt", path)
		if err != nil {
			return op, err
		}
		for _, item := range inventory.Skills {
			if item.Name == p.Record.Name {
				return op, fmt.Errorf("cannot add %s: personal scope already contains that name at %s", p.Record.Name, item.Path)
			}
		}
		for _, record := range after.Records {
			if record.Name == p.Record.Name || record.ID == p.Record.ID {
				return op, fmt.Errorf("cannot add %s: personal library already contains that name or ID", p.Record.Name)
			}
		}
		r := p.Record
		r.Library = filepath.Join(global.Store, "library", r.ID, r.Name)
		r.Enabled = false
		r.MigratedFrom = &MigrationOrigin{Project: s.Config.Project, Path: r.Original}
		for _, origin := range p.Record.Origins {
			if origin.Backup != "" {
				rel, err := filepath.Rel(filepath.Dir(p.Record.Library), origin.Backup)
				if err != nil {
					return op, err
				}
				origin.Backup = filepath.Join(filepath.Dir(r.Library), rel)
			}
			r.MigratedFrom.Origins = append(r.MigratedFrom.Origins, origin)
		}
		r.Original = filepath.Join(global.Shared(), r.Name)
		r.Links = []string{r.Original}
		r.Origins = []Origin{{Path: r.Original, Canonical: true}}
		if err := global.validate(r); err != nil {
			return op, err
		}
		after.Records = append(after.Records, r)
		additions = append(additions, r)
		names = append(names, r.Name)
		for _, move := range p.Moves {
			forgetReturned(&after, move.From)
			rel, err := filepath.Rel(filepath.Dir(p.Record.Library), move.To)
			if err != nil {
				return op, err
			}
			op.Content = append(op.Content, ContentChange{Kind: "move", From: move.From, To: filepath.Join(filepath.Dir(r.Library), rel), Identity: move.Identity, Digest: move.Digest})
			op.Description += "Move " + move.From + "\n  to " + filepath.Join(filepath.Dir(r.Library), rel) + "\n"
		}
		for _, comparison := range p.Comparisons {
			op.Description += "Compare " + comparison.Path + "\n  " + strings.Join(comparison.Differences, "\n  ") + "\n"
		}
	}
	requestsBefore, err := s.loadPackages()
	if err != nil {
		return op, err
	}
	requestsAfter, err := s.resolvePackages(uniqueStrings(append(append([]string{}, requestsBefore.Requests...), names...)), additions)
	if err != nil {
		return op, err
	}
	op.Scopes = []OperationScope{{LibraryBefore: &before, LibraryAfter: &after}, {Project: s.Config.Project, LibraryBefore: &projectBefore, LibraryAfter: &projectBefore, RequestsBefore: &requestsBefore, RequestsAfter: &requestsAfter}}
	for _, skill := range requestsAfter.Skills {
		link := LinkPlan{Path: filepath.Join(s.Shared(), skill.Name), Target: s.centralLibrary(skill)}
		op.AddLinks = append(op.AddLinks, link)
		op.Description += "Create link " + link.Path + "\n"
	}
	if err := s.validateOperation(op, false); err != nil {
		return op, err
	}
	if err := preflightOperation(op); err != nil {
		return op, err
	}
	return op, nil
}
