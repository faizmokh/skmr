package manager

import (
	"fmt"
	"path/filepath"
	"strings"
)

// AutoAdopt brings discovered writable skills under skmr ownership. Ambiguous
// copies stay untouched so the user can choose which one to keep.
func (s *Service) AutoAdopt() error {
	return s.autoAdopt(nil)
}

// AutoAdoptExcept opens a scope while preserving explicit import paths for the
// operation the user is reviewing.
func (s *Service) AutoAdoptExcept(paths []string) error {
	excluded := map[string]bool{}
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		excluded[filepath.Clean(absolute)] = true
	}
	return s.autoAdopt(excluded)
}

func (s *Service) autoAdopt(excluded map[string]bool) error {
	if s.Config.Project != "" {
		global, err := New(Config{Home: s.Config.Home, DataHome: s.Config.DataHome, ConfigHome: s.Config.ConfigHome})
		if err != nil {
			return err
		}
		if err = global.autoAdopt(excluded); err != nil {
			return err
		}
	}
	if err := s.Upgrade(); err != nil {
		return err
	}
	if s.hasPending() {
		return nil
	}
	global, err := s.operationService("")
	if err != nil {
		return err
	}
	library, err := global.load()
	if err != nil {
		return err
	}
	if excluded == nil {
		excluded = map[string]bool{}
	}
	for _, path := range library.Returned {
		excluded[path] = true
	}
	result, err := s.List()
	if err != nil {
		return err
	}
	var paths []string
	for _, path := range SafeAdoptionPaths(result) {
		if !excluded[filepath.Clean(path)] {
			paths = append(paths, path)
		}
	}
	if len(paths) > 0 {
		plan, err := s.PreviewOperation(OperationRequest{Action: "adopt-batch", Arguments: paths})
		if err != nil {
			return fmt.Errorf("automatic skill adoption: %w", err)
		}
		if err = s.ApplyOperation(plan); err != nil {
			return fmt.Errorf("automatic skill adoption: %w", err)
		}
		if err = s.Upgrade(); err != nil {
			return err
		}
	}
	if s.Config.Project == "" && s.ShouldOfferSetup(result) {
		if err = s.MarkSetup("completed"); err != nil {
			return err
		}
	}
	return nil
}

// AutoAdoptConflicts reports names that were left untouched because multiple
// writable copies exist. This is informational and does not block safe skills.
func (s *Service) AutoAdoptConflicts() (string, error) {
	result, err := s.List()
	if err != nil {
		return "", err
	}
	return strings.Join(SortedConflictNames(result), ", "), nil
}
