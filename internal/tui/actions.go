package tui

import "github.com/faizmokh/skmr/internal/skills"

type skillAction struct {
	key   string
	label string
	event string
}

func actionsForSkill(skill skills.Skill) []skillAction {
	actions := []skillAction{{key: "i", label: "Read instructions", event: "i"}}
	if !skill.Inherited && !skill.ReadOnly && skill.ConflictID != "" {
		actions = append(actions, skillAction{key: "c", label: "Keep this copy", event: "c"})
	}
	if skills.HasCopyDifferences(skill.ConflictKind) {
		actions = append(actions, skillAction{key: "v", label: "View differences", event: "v"})
	}
	if skill.Inherited {
		return append(actions, skillAction{key: "o", label: "Open owner", event: "o"})
	}
	if skill.ReadOnly {
		return actions
	}
	if skill.Managed {
		label, event := "Enable", "e"
		if skill.Enabled {
			label, event = "Disable", "d"
		}
		actions = append(actions, skillAction{key: "Space", label: label, event: event})
		actions = append(actions,
			skillAction{key: "m", label: "Move to another scope", event: "m"},
			skillAction{key: "p", label: "Copy to another scope", event: "p"},
			skillAction{key: "r", label: "Remove from library", event: "r"},
		)
	} else if skill.ConflictID == "" && len(skill.Issues) == 0 {
		actions = append(actions, skillAction{key: "a", label: "Add to library", event: "a"})
	}
	return actions
}
