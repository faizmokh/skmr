package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/faizmokh/skmr/internal/skills"
)

const batchAction = "adopt-batch"

// AdoptionCandidate describes an unmanaged skill that can appear in a batch
// adoption picker. Read-only and inherited packages are deliberately omitted.
type AdoptionCandidate struct {
	Skill      skills.Skill
	Eligible   bool
	Selectable bool
	Reason     string
}

// AdoptionCandidates returns writable, unmanaged packages in stable inventory
// order. Unique valid skills are eligible by default; conflicts need an explicit
// kept-copy choice and invalid skills remain visible with a reason.
func AdoptionCandidates(result skills.Result) []AdoptionCandidate {
	out := []AdoptionCandidate{}
	for _, item := range result.Skills {
		if item.Managed || item.Inherited || item.ReadOnly {
			continue
		}
		candidate := AdoptionCandidate{Skill: item, Eligible: len(item.Issues) == 0 && item.ConflictID == ""}
		switch {
		case item.ConflictID != "":
			parsed := skills.Parse(item.Path)
			candidate.Selectable = len(parsed.Issues) == 0
			if candidate.Selectable {
				candidate.Reason = "Choose one copy to keep"
			} else {
				candidate.Reason = strings.Join(parsed.Issues, "; ")
			}
		case len(item.Issues) > 0:
			candidate.Reason = strings.Join(item.Issues, "; ")
		default:
			candidate.Selectable = true
		}
		out = append(out, candidate)
	}
	return out
}

// SafeAdoptionPaths returns the default batch selection. Duplicate-name groups
// and invalid packages are left for explicit review.
func SafeAdoptionPaths(result skills.Result) []string {
	paths := []string{}
	for _, candidate := range AdoptionCandidates(result) {
		if candidate.Eligible {
			paths = append(paths, candidate.Skill.Path)
		}
	}
	return paths
}

// BatchPlan is one reviewed, recoverable adoption transaction.
type BatchPlan struct {
	Version int      `json:"version"`
	Action  string   `json:"action"`
	Paths   []string `json:"paths"`
	Plans   []Plan   `json:"plans"`
	Before  Manifest `json:"before"`
}

func (p BatchPlan) String() string {
	lines := []string{fmt.Sprintf("Add %d skills to the library", len(p.Plans))}
	for _, item := range p.Plans {
		lines = append(lines, "", item.Record.Name, item.String())
	}
	return strings.Join(lines, "\n")
}

func (s *Service) PreviewBatchAdopt(paths []string) (BatchPlan, error) {
	if s.hasPending() {
		return BatchPlan{}, fmt.Errorf("an interrupted operation needs recovery; run doctor --recover")
	}
	return s.previewBatchAdopt(paths)
}

func (s *Service) previewBatchAdopt(paths []string) (BatchPlan, error) {
	if len(paths) == 0 {
		return BatchPlan{}, fmt.Errorf("select at least one skill")
	}
	before, err := s.load()
	if err != nil {
		return BatchPlan{}, err
	}
	result, err := s.List()
	if err != nil {
		return BatchPlan{}, err
	}
	byPath := map[string]skills.Skill{}
	for _, item := range result.Skills {
		byPath[filepath.Clean(item.Path)] = item
	}

	normalized := make([]string, 0, len(paths))
	seenPaths := map[string]bool{}
	seenNames := map[string]bool{}
	for _, path := range paths {
		absolute, absErr := filepath.Abs(path)
		if absErr != nil {
			return BatchPlan{}, absErr
		}
		absolute = filepath.Clean(absolute)
		if seenPaths[absolute] {
			return BatchPlan{}, fmt.Errorf("skill selected more than once: %s", absolute)
		}
		item, ok := byPath[absolute]
		if !ok {
			return BatchPlan{}, fmt.Errorf("skill was not found in this scope: %s", absolute)
		}
		if item.Managed || item.Inherited || item.ReadOnly {
			return BatchPlan{}, fmt.Errorf("skill is not a writable unmanaged package: %s", absolute)
		}
		if len(item.Issues) > 0 && item.ConflictID == "" {
			return BatchPlan{}, fmt.Errorf("cannot add %s: %s", item.Name, strings.Join(item.Issues, "; "))
		}
		if seenNames[item.Name] {
			return BatchPlan{}, fmt.Errorf("choose only one copy of %q", item.Name)
		}
		seenPaths[absolute] = true
		seenNames[item.Name] = true
		normalized = append(normalized, absolute)
	}

	batch := BatchPlan{Version: Version, Action: batchAction, Paths: normalized, Before: before}
	for _, path := range normalized {
		plan, previewErr := s.preview("adopt", path)
		if previewErr != nil {
			return BatchPlan{}, previewErr
		}
		if !reflect.DeepEqual(plan.Before, before) {
			return BatchPlan{}, fmt.Errorf("skill state changed while preparing the batch; review again")
		}
		batch.Plans = append(batch.Plans, plan)
	}
	return batch, nil
}

func (s *Service) ApplyBatch(plan BatchPlan) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if s.hasPending() {
		return fmt.Errorf("an interrupted operation needs recovery; run doctor --recover")
	}
	fresh, err := s.previewBatchAdopt(plan.Paths)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh, plan) {
		return fmt.Errorf("skill state changed since preview; review a fresh batch")
	}
	if err = atomicJSON(filepath.Join(s.Store, "batch.json"), plan); err != nil {
		return err
	}
	return s.finishBatch(plan)
}

func batchResult(plan BatchPlan) (Manifest, error) {
	manifest := plan.Before
	manifest.Records = append([]Record{}, plan.Before.Records...)
	for _, item := range plan.Plans {
		item.Before = manifest
		var err error
		manifest, err = resultManifest(item)
		if err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

func (s *Service) finishBatch(plan BatchPlan) error {
	after, err := batchResult(plan)
	if err != nil {
		return err
	}
	current, err := s.load()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, plan.Before) && !reflect.DeepEqual(current, after) {
		return fmt.Errorf("manifest changed outside the interrupted batch; recovery stopped")
	}
	if reflect.DeepEqual(current, plan.Before) {
		for _, item := range plan.Plans {
			if err = s.execute(item); err != nil {
				return fmt.Errorf("batch paused: %w; resolve the conflict, then run doctor --recover", err)
			}
		}
		if err = s.save(after); err != nil {
			return fmt.Errorf("batch save interrupted: %w; run doctor --recover", err)
		}
	}
	if err = os.Remove(filepath.Join(s.Store, "batch.json")); err != nil {
		return err
	}
	return syncDir(s.Store)
}

func (s *Service) recoverBatch() error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	b, err := os.ReadFile(filepath.Join(s.Store, "batch.json"))
	if err != nil {
		return err
	}
	var plan BatchPlan
	if err = json.Unmarshal(b, &plan); err != nil {
		return fmt.Errorf("read batch recovery journal: %w", err)
	}
	if plan.Version != Version || plan.Action != batchAction || len(plan.Plans) == 0 {
		return fmt.Errorf("unsupported batch recovery journal")
	}
	for _, item := range plan.Plans {
		if err = s.validate(item.Record); err != nil {
			return err
		}
	}
	for _, record := range plan.Before.Records {
		if err = s.validate(record); err != nil {
			return err
		}
	}
	return s.finishBatch(plan)
}

// SetupState records whether the first global migration prompt was completed or
// dismissed. Manual batch adoption remains available regardless of this state.
type SetupState struct {
	Version int    `json:"version"`
	Outcome string `json:"outcome"`
}

func (s *Service) ShouldOfferSetup(result skills.Result) bool {
	if s.Scope() != "global" {
		return false
	}
	if len(AdoptionCandidates(result)) == 0 {
		return false
	}
	if _, err := os.Stat(filepath.Join(s.Store, "manifest.json")); err == nil || !os.IsNotExist(err) {
		return false
	}
	if _, err := os.Stat(filepath.Join(s.Store, "setup.json")); err == nil || !os.IsNotExist(err) {
		return false
	}
	return true
}

func (s *Service) MarkSetup(outcome string) error {
	if outcome != "completed" && outcome != "skipped" {
		return fmt.Errorf("invalid setup outcome %q", outcome)
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = mkdir(s.Store); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(s.Store, "setup.json"), SetupState{Version: 1, Outcome: outcome})
}

// SortedConflictNames is useful to both interfaces when reporting batches that
// need an explicit kept-copy choice.
func SortedConflictNames(result skills.Result) []string {
	names := map[string]bool{}
	for _, candidate := range AdoptionCandidates(result) {
		if candidate.Skill.ConflictID != "" {
			names[candidate.Skill.Name] = true
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
