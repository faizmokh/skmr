package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type Plan struct {
	Version  int      `json:"version"`
	Action   string   `json:"action"`
	Record   Record   `json:"record"`
	Identity Identity `json:"identity"`
	Before   Manifest `json:"before"`
}

func (p Plan) String() string {
	r := p.Record
	var lines []string
	switch p.Action {
	case "adopt":
		lines = append(lines, "Move "+r.Original, "  to "+r.Library)
	case "restore":
		lines = append(lines, "Restore "+r.Library, "     to "+r.Original)
	}
	verb := "Create link"
	if p.Action == "disable" || p.Action == "restore" {
		verb = "Remove owned link"
	}
	for _, path := range r.Links {
		lines = append(lines, verb+" "+path)
	}
	return strings.Join(lines, "\n")
}

func (s *Service) Preview(action, arg string) (Plan, error) {
	if !absent(filepath.Join(s.Store, "journal.json")) {
		return Plan{}, fmt.Errorf("an interrupted operation needs recovery; run doctor --recover")
	}
	return s.preview(action, arg)
}
func (s *Service) preview(action, arg string) (Plan, error) {
	m, err := s.load()
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Version: Version, Action: action, Before: m}
	if action == "adopt" {
		p.Record, err = s.adoption(arg, m)
		if err != nil {
			return Plan{}, err
		}
		p.Identity, err = identity(p.Record.Original)
		return p, err
	}
	if action != "enable" && action != "disable" && action != "restore" {
		return p, fmt.Errorf("unknown action: %s", action)
	}
	found := false
	for _, r := range m.Records {
		if r.ID == arg || r.Name == arg {
			if found {
				return p, fmt.Errorf("ambiguous name; use an ID")
			}
			p.Record = r
			found = true
		}
	}
	if !found {
		return p, fmt.Errorf("skill %q is not managed in this scope; adopt it first", arg)
	}
	r := p.Record
	st, err := os.Lstat(r.Library)
	if err != nil {
		return p, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return p, fmt.Errorf("library content was replaced: %s", r.Library)
	}
	if err = realParents(r.Library); err != nil {
		return p, err
	}
	for _, path := range r.Links {
		if err = realParents(path); err != nil {
			return p, err
		}
		if !absent(path) && !owned(path, r.Library) {
			return p, fmt.Errorf("managed link was replaced; leave it intact and resolve the conflict: %s", path)
		}
	}
	if action == "restore" {
		if err = sameDevice(r.Library, r.Original); err != nil {
			return p, err
		}
		p.Identity, err = identity(r.Library)
	}
	return p, err
}

// Apply verifies that a reviewed plan is still current, then journals before any
// content mutation. Interrupted operations remain explicit until Recover is called.
func (s *Service) Apply(plan Plan) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	arg := plan.Record.ID
	if plan.Action == "adopt" {
		arg = plan.Record.Original
	}
	fresh, err := s.Preview(plan.Action, arg)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh, plan) {
		return fmt.Errorf("skill state changed since preview; review a fresh plan")
	}
	if err = atomicJSON(filepath.Join(s.Store, "journal.json"), plan); err != nil {
		return err
	}
	return s.finish(plan)
}

func (s *Service) Recover() error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	b, err := os.ReadFile(filepath.Join(s.Store, "journal.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var p Plan
	if err = json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("read recovery journal: %w", err)
	}
	if p.Version != Version || p.Before.Version != Version {
		return fmt.Errorf("unsupported journal version")
	}
	if err = s.validate(p.Record); err != nil {
		return err
	}
	for _, r := range p.Before.Records {
		if err = s.validate(r); err != nil {
			return err
		}
	}
	// A committed manifest may already reflect the result if cleanup was interrupted.
	current, err := s.load()
	if err != nil {
		return err
	}
	after, err := resultManifest(p)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, p.Before) && !reflect.DeepEqual(current, after) {
		return fmt.Errorf("manifest changed outside the interrupted operation; recovery stopped")
	}
	return s.finish(p)
}

func resultManifest(p Plan) (Manifest, error) {
	m := Manifest{Version: Version, Records: append([]Record{}, p.Before.Records...)}
	switch p.Action {
	case "adopt":
		m.Records = append(m.Records, p.Record)
	case "enable", "disable", "restore":
		found := false
		for i, r := range m.Records {
			if r.ID == p.Record.ID {
				found = true
				if p.Action == "restore" {
					m.Records = append(m.Records[:i], m.Records[i+1:]...)
				} else {
					m.Records[i].Enabled = p.Action == "enable"
				}
				break
			}
		}
		if !found {
			return m, fmt.Errorf("journal refers to an unknown managed skill")
		}
	default:
		return m, fmt.Errorf("unknown journal action %q", p.Action)
	}
	return m, nil
}
func (s *Service) finish(p Plan) error {
	if err := s.execute(p); err != nil {
		return fmt.Errorf("operation paused: %w; resolve the conflict, then run doctor --recover", err)
	}
	m, err := resultManifest(p)
	if err != nil {
		return err
	}
	if err = s.save(m); err != nil {
		return fmt.Errorf("save interrupted: %w; run doctor --recover", err)
	}
	if err = os.Remove(filepath.Join(s.Store, "journal.json")); err != nil {
		return err
	}
	return syncDir(s.Store)
}
func (s *Service) execute(p Plan) error {
	r := p.Record
	// Preflight every path before removing any links, including on replay.
	for _, path := range append(append([]string{}, r.Links...), r.Library) {
		if err := realParents(path); err != nil {
			return err
		}
	}
	switch p.Action {
	case "adopt":
		if err := move(r.Original, r.Library, p.Identity); err != nil {
			return err
		}
		for _, path := range r.Links {
			if err := s.link(path, r.Library); err != nil {
				return err
			}
		}
	case "enable":
		if st, e := os.Lstat(r.Library); e != nil {
			return e
		} else if !st.IsDir() {
			return fmt.Errorf("library folder was replaced")
		}
		for _, path := range r.Links {
			if !absent(path) && !owned(path, r.Library) {
				return fmt.Errorf("link was replaced: %s", path)
			}
		}
		for _, path := range r.Links {
			if err := s.link(path, r.Library); err != nil {
				return err
			}
		}
	case "disable", "restore":
		restored := false
		if p.Action == "restore" {
			id, e := identity(r.Original)
			restored = e == nil && id == p.Identity
			if !restored {
				source, err := identity(r.Library)
				if err != nil {
					return err
				}
				if source != p.Identity {
					return fmt.Errorf("source folder changed: %s", r.Library)
				}
			}
		}
		for _, path := range r.Links {
			if restored && path == r.Original {
				continue
			}
			if !absent(path) && !owned(path, r.Library) {
				return fmt.Errorf("link was replaced: %s", path)
			}
		}
		for _, path := range r.Links {
			if restored && path == r.Original {
				continue
			}
			if err := unlink(path, r.Library); err != nil {
				return err
			}
		}
		if p.Action == "restore" {
			return move(r.Library, r.Original, p.Identity)
		}
	default:
		return fmt.Errorf("unknown action %q", p.Action)
	}
	return nil
}
func move(from, to string, id Identity) error {
	if dest, e := identity(to); e == nil && dest == id {
		return nil
	}
	if err := available(to); err != nil {
		return err
	}
	current, err := identity(from)
	if err != nil {
		return err
	}
	if current != id {
		return fmt.Errorf("source folder changed: %s", from)
	}
	if err = mkdir(filepath.Dir(to)); err != nil {
		return err
	}
	// Cross-device rename fails safely; no copy/delete fallback can lose metadata.
	if err = renameExclusive(from, to); err != nil {
		return fmt.Errorf("move %s to %s (both must be on the same filesystem): %w", from, to, err)
	}
	if err = syncDir(filepath.Dir(from)); err != nil {
		return err
	}
	return syncDir(filepath.Dir(to))
}
func (s *Service) link(path, target string) error {
	if owned(path, target) {
		return nil
	}
	if err := available(path); err != nil {
		return err
	}
	if err := mkdir(filepath.Dir(path)); err != nil {
		return err
	}
	value := target
	if s.Config.Project != "" {
		var err error
		value, err = filepath.Rel(filepath.Dir(path), target)
		if err != nil {
			return err
		}
	}
	if err := os.Symlink(value, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func unlink(path, target string) error {
	if absent(path) {
		return nil
	}
	if !owned(path, target) {
		return fmt.Errorf("refusing to remove replaced link: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

type Report struct {
	Healthy bool     `json:"healthy"`
	Issues  []string `json:"issues"`
}

func (s *Service) Doctor() (Report, error) {
	out, err := s.List()
	if err != nil {
		return Report{}, err
	}
	report := Report{Healthy: true, Issues: append([]string{}, out.Issues...)}
	for _, item := range out.Skills {
		for _, issue := range item.Issues {
			report.Issues = append(report.Issues, item.Name+" ("+item.Path+"): "+issue)
		}
	}
	report.Healthy = len(report.Issues) == 0
	return report, nil
}
