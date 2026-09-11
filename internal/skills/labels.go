package skills

// ConflictLabel returns plain-language copy for a conflict kind. ConflictKind
// values remain stable for JSON consumers; this helper is only for display.
func ConflictLabel(kind string) string {
	switch kind {
	case "identical":
		return "same copies"
	case "divergent":
		return "different copies"
	case "agent_config":
		return "same content, different agent config"
	case "external":
		return "view-only copies"
	default:
		return "copy conflict"
	}
}

// HasCopyDifferences reports whether the copy classification supports a diff.
func HasCopyDifferences(kind string) bool {
	return kind == "divergent" || kind == "agent_config"
}
