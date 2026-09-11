package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faizmokh/skmr/internal/skills"
)

func writeAgentConfig(t *testing.T, root, body string) {
	t.Helper()
	path := filepath.Join(root, "agents", "openai.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestAgentConfigClassification(t *testing.T) {
	for _, tc := range []struct{ name, left, right, extra, want string }{
		{"missing", "", "one", "", "agent_config"},
		{"identical", "one", "one", "", "identical"},
		{"changed", "one", "two", "", "agent_config"},
		{"supporting file", "", "one", "agents/other.yaml", "divergent"},
		{"instructions", "one", "two", "SKILL.md", "divergent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, left := fixture(t)
			right := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
			skill(t, right)
			if tc.left != "" {
				writeAgentConfig(t, left, tc.left)
			}
			if tc.right != "" {
				writeAgentConfig(t, right, tc.right)
			}
			if tc.extra != "" {
				path := filepath.Join(right, tc.extra)
				f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("\nExtra instructions\n")
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
			}
			result, err := s.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Skills) != 2 {
				t.Fatal(result.Skills)
			}
			for _, item := range result.Skills {
				if item.ConflictKind != tc.want {
					t.Fatalf("got %s, want %s", item.ConflictKind, tc.want)
				}
			}
		})
	}
}

func TestAgentConfigAdoptionPreservesBothCopies(t *testing.T) {
	s, left := fixture(t)
	right := filepath.Join(s.Config.ConfigHome, "opencode", "skills", "sample")
	skill(t, right)
	writeAgentConfig(t, left, "selected config")
	writeAgentConfig(t, right, "backup config")
	leftDigest, err := skills.Digest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := skills.Digest(right)
	if err != nil || leftDigest == rightDigest {
		t.Fatal("full digest must include configuration", err)
	}
	plan, err := s.Preview("adopt", left)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Comparisons) != 1 || !strings.Contains(strings.Join(plan.Comparisons[0].Differences, "\n"), "openai.yaml") {
		t.Fatal("configuration missing from preview", plan.Comparisons)
	}
	if err := s.Apply(plan); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(plan.Record.Library, "agents", "openai.yaml"))
	if err != nil || string(data) != "selected config" {
		t.Fatal("selected configuration changed", err)
	}
	apply(t, s, "restore", plan.Record.ID)
	for path, want := range map[string]string{left: "selected config", right: "backup config"} {
		data, err := os.ReadFile(filepath.Join(path, "agents", "openai.yaml"))
		if err != nil || string(data) != want {
			t.Fatal("configuration not restored", path, err)
		}
	}
}

func TestAgentConfigThreeCopiesAndExternalPrecedence(t *testing.T) {
	paths := []string{filepath.Join(t.TempDir(), "sample"), filepath.Join(t.TempDir(), "sample"), filepath.Join(t.TempDir(), "sample")}
	result := skills.Result{}
	for i, path := range paths {
		skill(t, path)
		if i > 0 {
			writeAgentConfig(t, path, strings.Repeat("x", i))
		}
		result.Skills = append(result.Skills, skills.Skill{Name: "sample", Path: path})
	}
	annotateConflicts(&result, Manifest{})
	for _, item := range result.Skills {
		if item.ConflictKind != "agent_config" || item.ConflictCount != 3 {
			t.Fatal(item)
		}
	}
	result.Skills[2].ReadOnly = true
	annotateConflicts(&result, Manifest{})
	for _, item := range result.Skills {
		if item.ConflictKind != "external" {
			t.Fatal(item)
		}
	}
}
