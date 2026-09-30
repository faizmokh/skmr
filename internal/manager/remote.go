package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/faizmokh/skmr/internal/skills"
	"github.com/faizmokh/skmr/internal/terminal"
)

// RemoteCandidate is a skill offered by an internet source.
type RemoteCandidate struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// SearchResult is a skills.sh result; URL can be passed directly to Import.
type SearchResult struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// RemotePlan owns its temporary staging directory until ApplyRemote or Cleanup.
type RemotePlan struct {
	Action  string
	Source  string
	Stage   string
	Entries []RemoteEntry
	Replace bool
	Before  *Manifest
}

type RemoteEntry struct {
	Record      Record
	StagedPath  string
	OldDigest   string
	Differences []string
}

type remoteJournal struct {
	Version int           `json:"version"`
	Action  string        `json:"action"`
	Before  Manifest      `json:"before"`
	Entries []RemoteEntry `json:"entries"`
}

func (s *Service) remoteJournalPath() string { return filepath.Join(s.Store, "remote-journal.json") }

func (s *Service) recoverRemote() error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.recoverRemoteLocked()
}

func (s *Service) recoverRemoteLocked() error {
	data, err := os.ReadFile(s.remoteJournalPath())
	if err != nil {
		return err
	}
	var j remoteJournal
	if err = json.Unmarshal(data, &j); err != nil {
		return fmt.Errorf("read remote journal: %w", err)
	}
	if j.Version != Version || len(j.Entries) == 0 || (j.Action != "import" && j.Action != "update") {
		return fmt.Errorf("invalid remote journal")
	}
	for _, entry := range j.Entries {
		if err = s.validate(entry.Record); err != nil {
			return err
		}
	}
	current, err := s.load()
	if err != nil {
		return err
	}
	after := cloneManifest(j.Before)
	if j.Action == "import" {
		for _, entry := range j.Entries {
			after.Records = append(after.Records, entry.Record)
		}
	} else {
		if len(j.Entries) != 1 {
			return fmt.Errorf("invalid remote update journal")
		}
		found := false
		for i := range after.Records {
			if after.Records[i].ID == j.Entries[0].Record.ID {
				after.Records[i] = j.Entries[0].Record
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("remote journal record is missing")
		}
	}
	committed := reflect.DeepEqual(current, after)
	if !committed && !reflect.DeepEqual(current, j.Before) {
		return fmt.Errorf("library changed outside the interrupted remote operation; recovery stopped")
	}
	if j.Action == "import" {
		if !committed {
			for _, entry := range j.Entries {
				if err = os.RemoveAll(filepath.Dir(entry.Record.Library)); err != nil {
					return err
				}
			}
		}
	} else {
		entry := j.Entries[0]
		parent := filepath.Dir(entry.Record.Library)
		backup := filepath.Join(parent, ".skmr-backup")
		temp := filepath.Join(parent, ".skmr-update")
		if !committed && !absent(backup) {
			if err = os.RemoveAll(entry.Record.Library); err != nil {
				return err
			}
			if err = os.Rename(backup, entry.Record.Library); err != nil {
				return err
			}
		} else if !committed {
			if err = verifyDigest(entry.Record.Library, entry.OldDigest); err != nil {
				return err
			}
		}
		if err = os.RemoveAll(temp); err != nil {
			return err
		}
		if committed {
			if err = os.RemoveAll(backup); err != nil {
				return err
			}
		}
	}
	if err = os.Remove(s.remoteJournalPath()); err != nil {
		return err
	}
	return syncDir(s.Store)
}

func (p RemotePlan) String() string {
	var lines []string
	for _, e := range p.Entries {
		verb := "Import"
		if p.Action == "update" {
			verb = "Update"
		}
		lines = append(lines, verb+" "+e.Record.Name+" from "+p.Source)
		for _, d := range e.Differences {
			lines = append(lines, "  "+d)
		}
		if p.Action == "import" {
			state := "Store disabled in personal library"
			if e.Record.Enabled {
				state = "Store in personal library and enable globally"
			}
			lines = append(lines, "  "+state)
		}
	}
	if len(lines) == 0 {
		return "Up to date."
	}
	return strings.Join(lines, "\n")
}

func (p RemotePlan) Cleanup() {
	if p.Stage != "" {
		_ = os.RemoveAll(p.Stage)
	}
}

var ansiCode = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
var listedName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var urlSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var resultURL = regexp.MustCompile(`https://skills\.sh/[A-Za-z0-9._/-]+`)

func runSkills(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npx", append([]string{"--yes", "skills"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DISABLE_TELEMETRY=1", "NO_COLOR=1", "CI=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Skills CLI timed out: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("Skills CLI failed: %s: %w", terminal.Safe(strings.TrimSpace(string(out))), err)
	}
	return out, nil
}

func normalizeRemote(raw string) (source, one string, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", "", fmt.Errorf("provide an HTTPS GitHub or skills.sh URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("source URLs cannot contain query parameters or fragments")
	}
	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch host {
	case "github.com":
		if len(parts) < 2 || !urlSegment.MatchString(parts[0]) || !urlSegment.MatchString(parts[1]) {
			return "", "", fmt.Errorf("GitHub URL needs owner and repository")
		}
		return raw, "", nil
	case "skills.sh", "www.skills.sh":
		if len(parts) == 2 && parts[0] == "p" && urlSegment.MatchString(parts[1]) {
			return raw, "", nil
		}
		if len(parts) == 2 && strings.Contains(parts[0], ".") && urlSegment.MatchString(parts[0]) && listedName.MatchString(parts[1]) {
			return "https://" + parts[0], parts[1], nil
		}
		if len(parts) == 3 && urlSegment.MatchString(parts[0]) && urlSegment.MatchString(parts[1]) && listedName.MatchString(parts[2]) {
			return "https://github.com/" + parts[0] + "/" + parts[1], parts[2], nil
		}
	}
	return "", "", fmt.Errorf("unsupported source URL; use a GitHub skill/repository, skills.sh skill page, or skills.sh pack")
}

// DiscoverRemote asks the Skills CLI to list the skills in a source.
func (s *Service) DiscoverRemote(raw string) ([]RemoteCandidate, error) {
	source, one, err := normalizeRemote(raw)
	if err != nil {
		return nil, err
	}
	out, err := runSkills(s.Config.Home, "add", source, "--list")
	if err != nil {
		return nil, err
	}
	plain := ansiCode.ReplaceAllString(string(out), "")
	start := strings.Index(plain, "Available Skills")
	if start < 0 {
		return nil, fmt.Errorf("Skills CLI list output changed; could not identify available skills")
	}
	seen := map[string]bool{}
	var found []RemoteCandidate
	for _, line := range strings.Split(plain[start+len("Available Skills"):], "\n") {
		if strings.Contains(line, "Use --skill") || strings.Contains(line, "Run without --list") {
			break
		}
		line = strings.TrimPrefix(line, "│")
		line = strings.TrimPrefix(line, "◆")
		if !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "      ") {
			continue
		}
		name := strings.TrimSpace(line)
		if !listedName.MatchString(name) || seen[name] {
			continue
		}
		if one != "" && name != one {
			continue
		}
		seen[name] = true
		found = append(found, RemoteCandidate{Name: name, URL: raw})
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no compatible skill found in %s", raw)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, nil
}

// SearchRemote uses the Skills CLI's skills.sh search and extracts result URLs.
func SearchRemote(query string) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("provide a search query")
	}
	out, err := runSkills("", "find", query)
	if err != nil {
		return nil, err
	}
	plain := ansiCode.ReplaceAllString(string(out), "")
	var results []SearchResult
	for _, line := range strings.Split(plain, "\n") {
		link := resultURL.FindString(line)
		if link == "" {
			continue
		}
		link = strings.TrimRight(link, "/.,)")
		parts := strings.Split(strings.TrimPrefix(link, "https://skills.sh/"), "/")
		if len(parts) != 3 && !(len(parts) == 2 && strings.Contains(parts[0], ".")) {
			continue
		}
		results = append(results, SearchResult{Name: parts[len(parts)-1], URL: link})
	}
	if len(results) == 0 && !strings.Contains(plain, "No skills found") {
		return nil, fmt.Errorf("Skills CLI search output changed; try adding a URL instead")
	}
	return results, nil
}

type stagedJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Path   string `json:"path"`
	Ref    string `json:"ref"`
	Error  string `json:"error"`
}

func stageRemote(raw string, names []string) (string, map[string]stagedJSON, error) {
	source, _, err := normalizeRemote(raw)
	if err != nil {
		return "", nil, err
	}
	stage, err := os.MkdirTemp("", "skmr-remote-*")
	if err != nil {
		return "", nil, err
	}
	args := []string{"add", source}
	for _, name := range names {
		args = append(args, "--skill", name)
	}
	isPack := strings.HasPrefix(source, "https://skills.sh/p/") || strings.HasPrefix(source, "https://www.skills.sh/p/")
	isWellKnown := isPack || !strings.HasPrefix(source, "https://github.com/")
	args = append(args, "--agent", "codex", "--copy", "--yes")
	if !isWellKnown {
		args = append(args, "--json")
	}
	out, err := runSkills(stage, args...)
	if err != nil {
		os.RemoveAll(stage)
		return "", nil, err
	}
	var records []stagedJSON
	if !isWellKnown {
		payload := bytes.TrimSpace(out)
		if len(payload) == 0 || payload[0] != '[' {
			jsonStart := bytes.LastIndex(out, []byte("\n[\n"))
			if jsonStart < 0 {
				os.RemoveAll(stage)
				return "", nil, fmt.Errorf("Skills CLI install output changed; no JSON result found")
			}
			payload = bytes.TrimSpace(out[jsonStart+1:])
		}
		if err = json.Unmarshal(payload, &records); err != nil {
			os.RemoveAll(stage)
			return "", nil, fmt.Errorf("Skills CLI install output changed: %w", err)
		}
	}
	stageRoot, err := filepath.EvalSymlinks(stage)
	if err != nil {
		os.RemoveAll(stage)
		return "", nil, err
	}
	if isWellKnown {
		for _, name := range names {
			records = append(records, stagedJSON{Name: name, Status: "installed", Path: filepath.Join(stageRoot, ".agents", "skills", name)})
		}
	}
	byName := map[string]stagedJSON{}
	for _, item := range records {
		if item.Status != "installed" {
			continue
		}
		p := item.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(stage, p)
		}
		p, err = filepath.EvalSymlinks(p)
		if err != nil || !within(stageRoot, p) {
			os.RemoveAll(stage)
			return "", nil, fmt.Errorf("Skills CLI reported a path outside staging")
		}
		parsed := skills.Parse(p)
		if len(parsed.Issues) > 0 {
			os.RemoveAll(stage)
			return "", nil, fmt.Errorf("invalid downloaded skill %q: %s", item.Name, strings.Join(parsed.Issues, "; "))
		}
		if err = validateRelocation(p); err != nil {
			os.RemoveAll(stage)
			return "", nil, fmt.Errorf("unsafe downloaded skill %q: %w", item.Name, err)
		}
		item.Name, item.Path = parsed.Name, p
		byName[item.Name] = item
	}
	for _, name := range names {
		if _, ok := byName[name]; !ok {
			os.RemoveAll(stage)
			return "", nil, fmt.Errorf("selected skill %q was not staged", name)
		}
	}
	return stage, byName, nil
}

func (s *Service) PrepareImport(raw string, names []string) (RemotePlan, error) {
	if s.Config.Project != "" {
		return RemotePlan{}, fmt.Errorf("remote imports belong to the personal library")
	}
	if s.hasPending() {
		return RemotePlan{}, fmt.Errorf("recover the interrupted operation before importing")
	}
	if len(names) == 0 {
		return RemotePlan{}, fmt.Errorf("select at least one skill")
	}
	selected := map[string]bool{}
	for _, name := range names {
		if selected[name] {
			return RemotePlan{}, fmt.Errorf("skill %q was selected more than once", name)
		}
		selected[name] = true
	}
	stage, staged, err := stageRemote(raw, names)
	if err != nil {
		return RemotePlan{}, err
	}
	p := RemotePlan{Action: "import", Source: raw, Stage: stage}
	m, err := s.load()
	if err != nil {
		p.Cleanup()
		return RemotePlan{}, err
	}
	p.Before = &m
	for _, name := range names {
		item := staged[name]
		for _, existing := range m.Records {
			if existing.Name == name {
				p.Cleanup()
				return RemotePlan{}, fmt.Errorf("skill %q already exists in the library (%s)", name, existing.ID)
			}
		}
		for _, found := range skills.Scan(s.Roots).Skills {
			if found.Name == name {
				p.Cleanup()
				return RemotePlan{}, fmt.Errorf("skill %q already exists at %s", name, found.Path)
			}
		}
		digest, e := skills.Digest(item.Path)
		if e != nil {
			p.Cleanup()
			return RemotePlan{}, e
		}
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(raw+"#"+name)))[:12]
		r := Record{ID: id, Name: name, Library: filepath.Join(s.Store, "library", id, name), Links: []string{filepath.Join(s.Shared(), name)}, Remote: &RemoteSource{URL: raw, Skill: name, Digest: digest, Revision: item.Ref}}
		if err = s.validate(r); err != nil {
			p.Cleanup()
			return RemotePlan{}, err
		}
		if err = available(r.Library); err != nil {
			p.Cleanup()
			return RemotePlan{}, err
		}
		p.Entries = append(p.Entries, RemoteEntry{Record: r, StagedPath: item.Path})
	}
	return p, nil
}

func (s *Service) PrepareUpdate(id string, replace bool) (RemotePlan, error) {
	if s.Config.Project != "" {
		return RemotePlan{}, fmt.Errorf("remote updates belong to the personal library")
	}
	m, err := s.load()
	if err != nil {
		return RemotePlan{}, err
	}
	var old *Record
	for i := range m.Records {
		if m.Records[i].ID == id || m.Records[i].Name == id {
			old = &m.Records[i]
			break
		}
	}
	if old == nil || old.Remote == nil {
		return RemotePlan{}, fmt.Errorf("skill %q is not a remote library skill", id)
	}
	stage, staged, err := stageRemote(old.Remote.URL, []string{old.Remote.Skill})
	if err != nil {
		return RemotePlan{}, err
	}
	p := RemotePlan{Action: "update", Source: old.Remote.URL, Stage: stage, Replace: replace, Before: &m}
	item := staged[old.Remote.Skill]
	localDigest, err := skills.Digest(old.Library)
	if err != nil {
		p.Cleanup()
		return RemotePlan{}, err
	}
	newDigest, err := skills.Digest(item.Path)
	if err != nil {
		p.Cleanup()
		return RemotePlan{}, err
	}
	diffs, err := skills.Differences(old.Library, item.Path)
	if err != nil {
		p.Cleanup()
		return RemotePlan{}, err
	}
	if localDigest != old.Remote.Digest && newDigest != localDigest && !replace {
		p.Cleanup()
		return RemotePlan{}, fmt.Errorf("local edits detected in %s; update would change:\n  %s\nreview them and pass --replace to overwrite", old.Name, strings.Join(diffs, "\n  "))
	}
	if newDigest == localDigest && newDigest == old.Remote.Digest {
		return p, nil
	}
	r := *old
	remote := *old.Remote
	remote.Digest, remote.Revision = newDigest, item.Ref
	r.Remote = &remote
	p.Entries = []RemoteEntry{{Record: r, StagedPath: item.Path, OldDigest: localDigest, Differences: diffs}}
	return p, nil
}

// ApplyRemote copies a reviewed stage into the library and saves its records.
func (s *Service) ApplyRemote(p RemotePlan) error {
	defer p.Cleanup()
	if len(p.Entries) == 0 {
		return nil
	}
	op, err := s.operationFromRemote(p)
	if err != nil {
		return err
	}
	return s.ApplyOperation(op)
}
