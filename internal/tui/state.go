package tui

import (
	"fmt"
	"strings"

	"github.com/faizmokh/skmr/internal/terminal"
)

type paneMode uint8

const (
	paneList paneMode = iota
	paneDetails
	paneInstructions
	paneActions
	paneProblems
	paneDiff
	paneHelp
	paneReview
)

type inventoryPhase uint8

const (
	inventoryReady inventoryPhase = iota
	inventoryLoading
)

type noticeLevel uint8

const (
	noticeInfo noticeLevel = iota
	noticeProgress
	noticeSuccess
	noticeWarning
	noticeError
)

type notice struct {
	text  string
	level noticeLevel
}

type problem struct {
	title  string
	detail string
}

func (m *Model) setNotice(level noticeLevel, text string) {
	m.notice = notice{text: text, level: level}
}

func (m *Model) openPane(next paneMode) {
	if m.pane != next {
		m.returnPane = m.pane
	}
	m.pane = next
	m.offset = 0
}

func (m *Model) closePane() {
	m.pane = m.returnPane
	m.returnPane = paneList
	m.offset = 0
}

func (m Model) compact() bool { return m.width < wideLayoutWidth }

func (m Model) problems() []problem {
	seen := map[string]bool{}
	out := []problem{}
	add := func(key, title, detail string) {
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, problem{title: terminal.Safe(title), detail: terminal.Safe(detail)})
	}
	for _, issue := range m.result.Issues {
		add("scope:"+issue, issue, "Current scope")
	}
	for _, skill := range m.result.Skills {
		for _, issue := range skill.Issues {
			key := skill.ID + ":" + issue
			if skill.ConflictID != "" {
				key = "copies:" + skill.ConflictID + ":" + issue
			}
			add(key, skill.Name+": "+issue, skill.Path)
		}
	}
	return out
}

func (m Model) viewAfterAction(action string) skillView {
	switch action {
	case "add", "adopt", "resolve", "enable", "disable", "sync":
		return viewLibrary
	case "restore":
		return viewOtherFolders
	default:
		return m.filter
	}
}

func (m *Model) revealActionResult(id, action string) {
	m.selectView(m.viewAfterAction(action))
	key := "skill:" + id
	for _, item := range m.result.Skills {
		if item.ID == id && item.Group != nil {
			m.setExpanded(item.Group.ID, true)
		}
	}
	m.restoreSelection(key)
	if item, ok := m.selected(); ok && item.ID == id {
		m.pane = paneList
		if m.compact() {
			m.pane = paneDetails
		}
		m.returnPane = paneList
		return
	}
	m.query = ""
	m.restoreSelection(key)
	if item, ok := m.selected(); ok && item.ID == id {
		m.pane = paneList
		if m.compact() {
			m.pane = paneDetails
		}
		m.returnPane = paneList
		return
	}
	m.selectView(viewAll)
	m.restoreSelection(key)
	m.pane = paneList
	if m.compact() {
		if item, ok := m.selected(); ok && item.ID == id {
			m.pane = paneDetails
		}
	}
	m.returnPane = paneList
}

func (m Model) problemSummary() string {
	count := len(m.problems())
	if count == 1 {
		return "! 1 problem"
	}
	return fmt.Sprintf("! %d problems", count)
}

func sanitizeNotice(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(terminal.Safe(err.Error()))
}

func successNotice(action string) string {
	prefix := map[string]string{
		"add":              "Added to this scope.",
		"remove":           "Placement removed.",
		"sync":             "Project placements synced.",
		"update":           "Updated from source.",
		"group-create":     "Preset created.",
		"group-delete":     "Preset deleted.",
		"add-placement":    "Added to this project.",
		"remove-placement": "Removed from this project.",
		"adopt":            "Added to library.",
		"resolve":          "Kept this copy.",
		"enable":           "Enabled for agent discovery.",
		"disable":          "Disabled for agent discovery.",
		"restore":          "Removed from library.",
		"delete":           "Skill permanently deleted.",
	}[action]
	if prefix == "" {
		prefix = "Change applied."
	}
	return prefix + " Agents may need to reload skills."
}

func failureNotice(context string, err error) string {
	prefix := map[string]string{
		"load":          "Could not read skill folders.",
		"preview":       "Could not prepare this change.",
		"batch-preview": "Could not prepare these changes.",
		"apply":         "Could not apply this change.",
		"batch-apply":   "Could not add the selected skills.",
		"scope":         "Could not switch scope.",
		"owner":         "Could not open the owning scope.",
		"diff":          "Could not compare these copies.",
	}[context]
	if prefix == "" {
		prefix = "The operation failed."
	}
	detail := sanitizeNotice(err)
	if detail == "" {
		return prefix
	}
	return prefix + " " + detail
}
