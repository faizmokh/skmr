package skills

import "testing"

func TestConflictLabel(t *testing.T) {
	tests := map[string]string{
		"identical":    "same copies",
		"agent_config": "same content, different agent config",
		"divergent":    "different copies",
		"external":     "view-only copies",
		"future":       "copy conflict",
	}
	for kind, want := range tests {
		if got := ConflictLabel(kind); got != want {
			t.Errorf("ConflictLabel(%q) = %q, want %q", kind, got, want)
		}
	}
}
