package tui

import "github.com/faizmokh/skmr/internal/skills"

type skillAction struct {
	key   string
	label string
	event string
}

func actionsForSkill(skill skills.Skill) []skillAction {
	actions := []skillAction{{key: "i", label: "Read instructions", event: "i"}}
	if skill.Installed {
		return append(actions, skillAction{key: "z", label: "Remove from this project", event: "Z"}, skillAction{key: "o", label: "Open in personal library", event: "o"})
	}
	if !skill.Inherited && !skill.ReadOnly && skill.ConflictID != "" {
		actions = append(actions, skillAction{key: "c", label: "Keep this copy", event: "c"})
	}
	if skills.HasCopyDifferences(skill.ConflictKind) {
		actions = append(actions, skillAction{key: "v", label: "View differences", event: "v"})
	}
	if skill.Inherited {
		if skill.Managed {
			actions = append(actions, skillAction{key: "a", label: "Add to this project", event: "P"})
		}
		return append(actions, skillAction{key: "o", label: "Open owner", event: "o"})
	}
	if skill.ReadOnly {
		return actions
	}
	if skill.Managed {
		if skill.Remote {
			actions = append(actions, skillAction{key: "u", label: "Update from source", event: "u"})
			actions = append(actions, skillAction{key: "U", label: "Replace local edits from source", event: "U"})
		}
		label, event := "Enable", "e"
		if skill.Enabled {
			label, event = "Disable", "d"
		}
		actions = append(actions, skillAction{key: "Space", label: label, event: event})
		if !skill.Remote && skill.SourcePath == "" {
			actions = append(actions, skillAction{key: "r", label: "Return to origin", event: "r"})
		}
		if !skill.Enabled {
			actions = append(actions, skillAction{key: "D", label: "Permanently delete", event: "D"})
		}
	} else if skill.ConflictID == "" && len(skill.Issues) == 0 {
		actions = append(actions, skillAction{key: "a", label: "Add to library", event: "a"})
	}
	return actions
}
