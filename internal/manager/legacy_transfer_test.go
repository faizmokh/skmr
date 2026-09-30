package manager

import (
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/faizmokh/skmr/internal/skills"
)

// These builders reproduce historical journals for compatibility tests. New
// operations use the central library and never create scope transfers.
func (p TransferPlan) String() string {
	verb := "Move"
	if p.Action == "copy" {
		verb = "Copy"
	}
	lines := []string{
		verb + " " + p.SourceRecord.Library,
		"  to " + p.DestinationRecord.Library,
		"Source scope: " + scopeLabel(p.SourceProject),
		"Destination scope: " + scopeLabel(p.DestinationProject),
	}
	if p.SourceRecord.Enabled {
		lines = append(lines, "Result: enabled in "+scopeLabel(p.DestinationProject))
	} else {
		lines = append(lines, "Result: disabled in "+scopeLabel(p.DestinationProject))
	}
	if p.Action == "copy" {
		state := "disabled"
		if p.SourceRecord.Enabled {
			state = "enabled"
		}
		lines = append(lines, "Source remains "+state+" in "+scopeLabel(p.SourceProject))
	}
	if p.Action == "move" && p.SourceRecord.Enabled {
		for _, link := range p.SourceRecord.Links {
			lines = append(lines, "Remove discovery link "+link)
		}
	}
	return joinLines(lines)
}

func scopeLabel(project string) string {
	if project == "" {
		return "global"
	}
	return "project " + project
}

func joinLines(lines []string) string {
	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

// previewLegacyTransfer validates a move or independent copy without changing state.
func (s *Service) previewLegacyTransfer(action, id string, destination *Service) (TransferPlan, error) {
	p := TransferPlan{Version: Version, Action: action, SourceProject: s.Config.Project, DestinationProject: destination.Config.Project, SourceStore: s.Store, DestinationStore: destination.Store}
	if action != "move" && action != "copy" {
		return p, fmt.Errorf("unknown transfer action: %s", action)
	}
	if filepath.Clean(s.Store) == filepath.Clean(destination.Store) {
		return p, fmt.Errorf("source and destination scopes are the same")
	}
	if s.hasPending() || destination.hasPending() {
		return p, fmt.Errorf("an interrupted operation needs recovery; run doctor --recover in the affected scope")
	}
	var err error
	if p.SourceBefore, err = s.load(); err != nil {
		return p, err
	}
	if p.DestinationBefore, err = destination.load(); err != nil {
		return p, err
	}
	found := false
	for _, record := range p.SourceBefore.Records {
		if record.ID == id || record.Name == id {
			if found {
				return p, fmt.Errorf("ambiguous name; use an ID")
			}
			p.SourceRecord = record
			found = true
		}
	}
	if !found {
		return p, fmt.Errorf("skill %q is not managed in this scope; adopt it first", id)
	}
	if len(p.SourceRecord.Origins) != 1 {
		return p, fmt.Errorf("skills with duplicate backups cannot be transferred; restore the skill, then adopt the copy you want to keep")
	}
	if pathsOverlap(p.SourceRecord.Library, destination.Store) || pathsOverlap(p.SourceRecord.Library, destination.Shared()) {
		return p, fmt.Errorf("destination scope overlaps the source skill package")
	}
	if err = validateRelocation(p.SourceRecord.Library); err != nil {
		return p, err
	}
	for _, link := range p.SourceRecord.Links {
		if !absent(link) && !owned(link, p.SourceRecord.Library) {
			return p, fmt.Errorf("managed link was replaced; leave it intact and resolve the conflict: %s", link)
		}
	}
	for _, record := range p.DestinationBefore.Records {
		if record.Name == p.SourceRecord.Name {
			return p, fmt.Errorf("destination already manages a skill named %s", record.Name)
		}
	}
	list, err := destination.List()
	if err != nil {
		return p, err
	}
	for _, item := range list.Skills {
		isMovingInheritedSource := action == "move" && item.Inherited && item.Managed && item.ID == p.SourceRecord.ID
		if item.Name == p.SourceRecord.Name && !isMovingInheritedSource {
			return p, fmt.Errorf("destination already contains a skill named %s at %s", item.Name, item.Path)
		}
	}
	destinationID := p.SourceRecord.ID
	if action == "copy" {
		destinationID = skills.ID(destination.Shared() + string(filepath.Separator) + p.SourceRecord.Name)
	}
	for _, record := range p.DestinationBefore.Records {
		if record.ID == destinationID {
			return p, fmt.Errorf("destination already contains skill ID %s", destinationID)
		}
	}
	original := filepath.Join(destination.Shared(), p.SourceRecord.Name)
	p.DestinationRecord = Record{
		ID: destinationID, Name: p.SourceRecord.Name, Original: original,
		Library: filepath.Join(destination.Store, "library", destinationID, p.SourceRecord.Name),
		Links:   []string{original}, Enabled: p.SourceRecord.Enabled,
		Origins: []Origin{{Path: original, Canonical: true}},
	}
	if err = destination.validate(p.DestinationRecord); err != nil {
		return p, err
	}
	if err = available(p.DestinationRecord.Library); err != nil {
		return p, err
	}
	if err = available(original); err != nil {
		return p, err
	}
	if action == "move" {
		if err = sameDevice(p.SourceRecord.Library, p.DestinationRecord.Library); err != nil {
			return p, err
		}
	}
	if p.Identity, err = identity(p.SourceRecord.Library); err != nil {
		return p, err
	}
	if p.Digest, err = skills.Digest(p.SourceRecord.Library); err != nil {
		return p, err
	}
	return p, nil
}

func pathsOverlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return a == b || within(a, b) || within(b, a)
}

func (s *Service) applyLegacyTransfer(destination *Service, plan TransferPlan) error {
	unlock, err := lockServices(s, destination)
	if err != nil {
		return err
	}
	defer unlock()
	fresh, err := s.previewLegacyTransfer(plan.Action, plan.SourceRecord.ID, destination)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh, plan) {
		return fmt.Errorf("skill state changed since preview; review a fresh plan")
	}
	if err = mkdir(s.Store); err != nil {
		return err
	}
	if err = mkdir(destination.Store); err != nil {
		return err
	}
	if err = atomicJSON(filepath.Join(s.Store, "transfer.json"), plan); err != nil {
		return err
	}
	if err = atomicJSON(filepath.Join(destination.Store, "transfer.json"), plan); err != nil {
		return fmt.Errorf("transfer journal interrupted: %w; run doctor --recover in the source scope", err)
	}
	return s.finishTransfer(destination, plan)
}
