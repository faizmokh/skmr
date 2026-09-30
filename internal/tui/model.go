// Package tui implements a keyboard-driven view over the shared manager service.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/faizmokh/skmr/internal/manager"
	"github.com/faizmokh/skmr/internal/skills"
	"github.com/faizmokh/skmr/internal/terminal"
)

type loadedMsg struct {
	result skills.Result
	err    error
}
type previewMsg struct {
	plan    manager.OperationPlan
	err     error
	confirm bool
}
type appliedMsg struct {
	err    error
	action string
}
type batchPreviewMsg struct {
	plan manager.OperationPlan
	err  error
}
type batchAppliedMsg struct {
	applyErr error
	setupErr error
	count    int
	skipped  int
}
type setupMarkedMsg struct{ err error }
type contentMsg struct{ id, content string }
type remoteFoundMsg struct {
	candidates []manager.RemoteCandidate
	url        string
	err        error
}
type remoteSearchMsg struct {
	results []manager.SearchResult
	err     error
}
type remotePreviewMsg struct {
	plan manager.OperationPlan
	err  error
}
type groupsLoadedMsg struct {
	groups []manager.InstallGroup
	err    error
}

type Model struct {
	service        *manager.Service
	project        string
	result         skills.Result
	cursor         int
	expanded       map[string]bool
	query          string
	searching      bool
	filter         skillView
	width, height  int
	content        string
	offset         int
	pending        *manager.OperationPlan
	migration      *migrationState
	selectID       string
	selectAction   string
	pane           paneMode
	returnPane     paneMode
	actionCursor   int
	diff           copyDiff
	diffTarget     int
	diffHorizontal int
	diffLoading    bool
	inventory      inventoryPhase
	busy           bool
	remoteMenu     bool
	remoteStep     string
	remoteInput    string
	remoteURL      string
	remoteCursor   int
	remoteResults  []manager.SearchResult
	remoteChoices  []manager.RemoteCandidate
	remoteSelected map[string]bool
	groupMenu      bool
	groupStep      string
	groupInput     string
	groupCursor    int
	groupItems     []manager.InstallGroup
	autoAdopt      bool
	notice         notice
}

func New(s *manager.Service) Model {
	return Model{service: s, project: s.Config.Project, width: 100, height: 30, inventory: inventoryLoading, busy: true, notice: notice{text: "Reading skill directories…", level: noticeProgress}}
}
func Run(s *manager.Service) error {
	m := New(s)
	m.autoAdopt = true
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
func (m Model) Init() tea.Cmd { return m.load() }
func (m Model) load() tea.Cmd {
	s := m.service
	return func() tea.Msg {
		if m.autoAdopt {
			if err := s.AutoAdopt(); err != nil {
				return loadedMsg{err: err}
			}
		}
		r, e := s.List()
		return loadedMsg{r, e}
	}
}
func (m Model) items() []skills.Skill {
	out := []skills.Skill{}
	q := strings.ToLower(m.query)
	view := m.currentView()
	for _, s := range m.result.Skills {
		if !view.includes(s) {
			continue
		}
		search := s.Name + " " + s.Description + " " + s.Path
		if s.Group != nil {
			search += " " + s.Group.Name + " " + s.Group.Source + " " + s.Group.Version + " " + s.Group.Marketplace
		}
		if !strings.Contains(strings.ToLower(search), q) {
			continue
		}
		out = append(out, s)
	}
	return out
}
func (m Model) selected() (skills.Skill, bool) {
	row, ok := m.selectedRow()
	if !ok || row.kind != skillRow {
		return skills.Skill{}, false
	}
	return row.skill, true
}
func (m *Model) selection() tea.Cmd {
	m.content = ""
	m.offset = 0
	if m.pane == paneActions {
		m.actionCursor = 0
	}
	item, ok := m.selected()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		b, e := skills.Read(item.Path)
		if e != nil {
			return contentMsg{item.ID, e.Error()}
		}
		return contentMsg{item.ID, terminal.Safe(string(b))}
	}
}
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case loadedMsg:
		m.busy = false
		m.inventory = inventoryReady
		m.pending = nil
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("load", msg.err))
			return m, nil
		}
		key := ""
		if row, ok := m.selectedRow(); ok {
			key = row.key()
		}
		m.result = msg.result
		if m.migration == nil && m.service.ShouldOfferSetup(m.result) {
			m.migration = newMigration(m.result, true)
			if m.migration != nil {
				m.setNotice(noticeInfo, "Choose the skills you want skmr to manage.")
				return m, nil
			}
		}
		if m.selectID != "" {
			m.revealActionResult(m.selectID, m.selectAction)
			key = "skill:" + m.selectID
			m.selectID = ""
			m.selectAction = ""
		}
		m.restoreSelection(key)
		if m.notice.level == noticeProgress {
			m.setNotice(noticeInfo, "Select a skill to inspect it or change how skmr handles it.")
		}
		return m, m.selection()
	case contentMsg:
		if item, ok := m.selected(); ok && item.ID == msg.id {
			m.content = msg.content
		}
	case remoteSearchMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("search", msg.err))
			return m, nil
		}
		m.remoteResults = msg.results
		m.remoteCursor = 0
		m.remoteStep = "results"
		if len(msg.results) == 0 {
			m.setNotice(noticeInfo, "No skills found. Enter another search.")
			m.remoteStep = "search"
		}
		return m, nil
	case remoteFoundMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("discover", msg.err))
			return m, nil
		}
		m.remoteURL = msg.url
		m.remoteChoices = msg.candidates
		m.remoteSelected = map[string]bool{}
		m.remoteCursor = 0
		m.remoteStep = "candidates"
		if len(msg.candidates) == 1 {
			m.remoteSelected[msg.candidates[0].Name] = true
			return m.prepareRemoteSelection()
		}
		return m, nil
	case remotePreviewMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("preview", msg.err))
			return m, nil
		}
		m.pending = &msg.plan
		m.remoteMenu = false
		m.remoteStep = ""
		m.openPane(paneReview)
		return m, nil
	case groupsLoadedMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("groups", msg.err))
			return m, nil
		}
		m.groupItems = msg.groups
		m.groupCursor = min(m.groupCursor, max(0, len(msg.groups)-1))
		return m, nil
	case diffMsg:
		source, target, ok := m.selectedDiffTarget()
		if m.pane != paneDiff || !ok || source.ID != msg.sourceID || target.ID != msg.targetID {
			return m, nil
		}
		m.diffLoading = false
		if msg.err != nil {
			m.diff = copyDiff{sourcePath: source.Path, targetPath: target.Path, lines: []diffLine{{kind: diffNotice, text: "Could not compare these copies. " + sanitizeNotice(msg.err)}}}
			m.setNotice(noticeError, failureNotice("diff", msg.err))
			return m, nil
		}
		m.diff = msg.diff
		m.setNotice(noticeInfo, "Differences ready. Selected is left; comparison is right.")
		return m, nil
	case previewMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("preview", msg.err))
			return m, nil
		}
		if msg.confirm {
			m.groupMenu = false
			m.openPane(paneReview)
			m.pending = &msg.plan
			return m, nil
		}
		m.busy = true
		s := m.service
		return m, func() tea.Msg { return appliedMsg{s.ApplyOperation(msg.plan), msg.plan.Action} }
	case batchPreviewMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("batch-preview", msg.err))
			return m, nil
		}
		m.pending = &msg.plan
		m.offset = 0
		return m, nil
	case batchAppliedMsg:
		m.busy = false
		if msg.applyErr != nil {
			m.setNotice(noticeError, failureNotice("batch-apply", msg.applyErr))
			return m, nil
		}
		m.migration = nil
		m.selectView(viewLibrary)
		message := fmt.Sprintf("Added %d skills. Skipped %d. Agents may need to reload skills.", msg.count, msg.skipped)
		if msg.setupErr != nil {
			message += " Setup status could not be saved: " + sanitizeNotice(msg.setupErr)
		}
		m.setNotice(noticeSuccess, message)
		m.busy = true
		return m, m.load()
	case setupMarkedMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, "Could not save setup status. "+sanitizeNotice(msg.err))
			return m, nil
		}
		m.migration = nil
		m.setNotice(noticeInfo, "Setup skipped. Press A to add skills later.")
		return m, nil
	case appliedMsg:
		m.busy = false
		m.pending = nil
		if msg.err == nil && strings.HasPrefix(msg.action, "group-") {
			m.closePane()
			m.groupMenu, m.groupStep, m.groupInput = true, "list", ""
			m.setNotice(noticeSuccess, "Group preset saved.")
			m.busy = true
			s := m.service
			return m, func() tea.Msg { groups, err := s.Groups(); return groupsLoadedMsg{groups, err} }
		}
		if msg.err != nil {
			m.closePane()
			m.setNotice(noticeError, failureNotice("apply", msg.err))
		} else {
			if item, ok := m.selected(); ok && msg.action != "delete" {
				m.selectID = item.ID
				m.selectAction = msg.action
			}
			if msg.action == "delete" {
				m.pane = paneList
				m.returnPane = paneList
			}
			m.setNotice(noticeSuccess, successNotice(msg.action))
		}
		m.busy = true
		return m, m.load()
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			if m.pending != nil {
				m.pending.Discard()
			}
			return m, tea.Quit
		}
		if m.busy {
			switch key {
			case "q":
				return m, tea.Quit
			case "f":
				m.cycleView()
				return m, nil
			case "1", "2", "3", "4":
				view, _ := viewForShortcut(key)
				m.selectView(view)
				return m, nil
			}
			return m, nil
		}
		if m.groupMenu {
			if key == "esc" || key == "q" {
				if m.groupStep == "list" {
					m.groupMenu = false
				} else {
					m.groupStep = "list"
				}
				return m, nil
			}
			switch m.groupStep {
			case "list":
				switch key {
				case "j", "down":
					m.groupCursor = min(m.groupCursor+1, max(0, len(m.groupItems)-1))
				case "k", "up":
					m.groupCursor = max(0, m.groupCursor-1)
				case "c":
					m.groupStep, m.groupInput = "create", ""
				case "d":
					if len(m.groupItems) > 0 {
						name := m.groupItems[m.groupCursor].Name
						m.busy = true
						s := m.service
						return m, func() tea.Msg {
							plan, err := s.PreviewOperation(manager.OperationRequest{Action: "group-delete", Arguments: []string{name}})
							return previewMsg{plan: plan, err: err, confirm: true}
						}
					}
				case "enter":
					if len(m.groupItems) > 0 {
						name := m.groupItems[m.groupCursor].Name
						m.busy = true
						s := m.service
						return m, func() tea.Msg {
							plan, err := s.PreviewOperation(manager.OperationRequest{Action: "add", Arguments: []string{"@" + name}})
							return previewMsg{plan: plan, err: err, confirm: true}
						}
					}
				}
			case "create":
				switch key {
				case "enter":
					fields := strings.Fields(m.groupInput)
					if len(fields) < 2 {
						m.setNotice(noticeWarning, "Enter a name followed by one or more skills.")
						return m, nil
					}
					m.busy = true
					s := m.service
					return m, func() tea.Msg {
						plan, err := s.PreviewOperation(manager.OperationRequest{Action: "group-create", Arguments: fields})
						return previewMsg{plan: plan, err: err, confirm: true}
					}
				case "backspace":
					r := []rune(m.groupInput)
					if len(r) > 0 {
						m.groupInput = string(r[:len(r)-1])
					}
				default:
					if msg.Type == tea.KeyRunes {
						m.groupInput += string(msg.Runes)
					}
					if msg.Type == tea.KeySpace {
						m.groupInput += " "
					}
				}

			}
			return m, nil
		}
		if m.remoteMenu {
			if key == "esc" || key == "q" {
				if m.remoteStep == "menu" {
					m.remoteMenu = false
				} else {
					m.remoteStep = "menu"
				}
				return m, nil
			}
			switch m.remoteStep {
			case "menu":
				if key == "l" {
					m.remoteStep, m.remoteInput = "local", ""
				}
				if key == "p" {
					m.remoteStep, m.remoteInput = "url", ""
				}
				if key == "s" {
					m.remoteStep, m.remoteInput = "search", ""
				}
			case "local", "url", "search":
				switch key {
				case "enter":
					value := strings.TrimSpace(m.remoteInput)
					if value == "" {
						m.setNotice(noticeWarning, "Enter a path, URL, or search query.")
						return m, nil
					}
					m.busy = true
					if m.remoteStep == "local" {
						s := m.service
						return m, func() tea.Msg {
							plan, err := s.PreviewOperation(manager.OperationRequest{Action: "add", Arguments: []string{value}})
							return remotePreviewMsg{plan: plan, err: err}
						}
					}
					if m.remoteStep == "url" {
						return m, m.discoverRemote(value)
					}
					return m, func() tea.Msg { results, err := manager.SearchRemote(value); return remoteSearchMsg{results, err} }
				case "backspace":
					r := []rune(m.remoteInput)
					if len(r) > 0 {
						m.remoteInput = string(r[:len(r)-1])
					}
				default:
					if msg.Type == tea.KeyRunes {
						m.remoteInput += string(msg.Runes)
					}
					if msg.Type == tea.KeySpace {
						m.remoteInput += " "
					}
				}
			case "results":
				switch key {
				case "j", "down":
					m.remoteCursor = min(m.remoteCursor+1, len(m.remoteResults)-1)
				case "k", "up":
					m.remoteCursor = max(0, m.remoteCursor-1)
				case "enter":
					if len(m.remoteResults) > 0 {
						m.busy = true
						return m, m.discoverRemote(m.remoteResults[m.remoteCursor].URL)
					}
				}
			case "candidates":
				switch key {
				case "j", "down":
					m.remoteCursor = min(m.remoteCursor+1, len(m.remoteChoices)-1)
				case "k", "up":
					m.remoteCursor = max(0, m.remoteCursor-1)
				case " ":
					if len(m.remoteChoices) > 0 {
						name := m.remoteChoices[m.remoteCursor].Name
						m.remoteSelected[name] = !m.remoteSelected[name]
					}
				case "enter":
					return m.prepareRemoteSelection()
				}
			}
			return m, nil
		}
		if m.pending != nil {
			if key == "pgdown" {
				m.offset += max(1, m.height/3)
			}
			if key == "pgup" {
				m.offset = max(0, m.offset-max(1, m.height/3))
			}
			if key == "esc" || key == "n" {
				m.pending.Discard()
				m.pending = nil
				m.offset = 0
				if m.migration != nil {
					m.setNotice(noticeInfo, "Review your skill selection.")
					return m, nil
				}
				m.closePane()
				m.setNotice(noticeInfo, "No changes made.")
			}
			if key == "q" {
				m.pending.Discard()
				return m, tea.Quit
			}
			if key == "y" {
				plan := *m.pending
				m.pending = nil
				m.busy = true
				s := m.service
				if m.migration != nil {
					initial, skipped, count := m.migration.initial, m.migration.skippedCount(), m.migration.selectedCount()
					return m, func() tea.Msg {
						defer plan.Discard()
						applyErr := s.ApplyOperation(plan)
						var setupErr error
						if applyErr == nil && initial {
							setupErr = s.MarkSetup("completed")
						}
						return batchAppliedMsg{applyErr: applyErr, setupErr: setupErr, count: count, skipped: skipped}
					}
				}
				return m, func() tea.Msg { defer plan.Discard(); return appliedMsg{s.ApplyOperation(plan), plan.Action} }
			}
			return m, nil
		}
		if m.migration != nil {
			rows := m.migration.rows()
			switch key {
			case "q":
				return m, tea.Quit
			case "j", "down":
				m.migration.cursor = min(m.migration.cursor+1, max(0, len(rows)-1))
			case "k", "up":
				m.migration.cursor = max(0, m.migration.cursor-1)
			case "home", "g":
				m.migration.cursor = 0
			case "end", "G":
				m.migration.cursor = max(0, len(rows)-1)
			case "left", "right":
				m.migration.navigateConflict(key)
			case " ":
				if reason, changed := m.migration.toggleCurrent(); !changed && reason != "" {
					m.setNotice(noticeWarning, reason)
				}
			case "a":
				m.migration.selectAllSafe()
				m.setNotice(noticeInfo, fmt.Sprintf("Selected %d safe skills.", m.migration.safeCount()))
			case "n":
				m.migration.clearSelection()
				m.setNotice(noticeInfo, "Selection cleared.")
			case "enter":
				paths := m.migration.selectedPaths()
				if len(paths) == 0 {
					m.setNotice(noticeWarning, "Select at least one skill to continue.")
					return m, nil
				}
				m.busy = true
				s := m.service
				return m, func() tea.Msg {
					plan, err := s.PreviewOperation(manager.OperationRequest{Action: "adopt-batch", Arguments: paths})
					return batchPreviewMsg{plan: plan, err: err}
				}
			case "esc":
				if m.migration.initial {
					m.busy = true
					s := m.service
					return m, func() tea.Msg { return setupMarkedMsg{err: s.MarkSetup("skipped")} }
				}
				m.migration = nil
				m.setNotice(noticeInfo, "No changes made.")
			}
			return m, nil
		}
		if m.pane == paneInstructions || m.pane == paneProblems || m.pane == paneHelp {
			switch key {
			case "pgdown", "ctrl+d":
				m.offset += max(1, m.height/3)
			case "pgup", "ctrl+u":
				m.offset = max(0, m.offset-max(1, m.height/3))
			case "esc":
				m.closePane()
			case "!":
				if m.pane == paneProblems {
					m.closePane()
				}
			case "?":
				if m.pane == paneHelp {
					m.closePane()
				}
			case "q":
				return m, tea.Quit
			}
			return m, nil
		}
		if m.pane == paneDiff {
			switch key {
			case "pgdown", "ctrl+d":
				m.offset += max(1, m.height/3)
			case "pgup", "ctrl+u":
				m.offset = max(0, m.offset-max(1, m.height/3))
			case "[":
				return m, m.cycleDiffTarget(-1)
			case "]":
				return m, m.cycleDiffTarget(1)
			case "left", "h":
				m.diffHorizontal = max(0, m.diffHorizontal-4)
			case "right", "l":
				m.diffHorizontal = min(m.diffHorizontal+4, maxDiffLineWidth(m.diff.lines))
			case "esc":
				m.closePane()
			case "q":
				return m, tea.Quit
			}
			return m, nil
		}
		if m.pane == paneActions {
			item, ok := m.selected()
			actions := []skillAction{}
			if ok {
				actions = actionsForSkill(item)
			}
			if len(actions) == 0 {
				m.closePane()
				return m, nil
			}
			switch key {
			case "j", "down":
				m.actionCursor = min(m.actionCursor+1, len(actions)-1)
				return m, nil
			case "k", "up":
				m.actionCursor = max(0, m.actionCursor-1)
				return m, nil
			case "esc", "x":
				m.closePane()
				return m, nil
			case "q":
				return m, tea.Quit
			case "enter":
				key = actions[m.actionCursor].event
			default:
				matched := false
				for _, action := range actions {
					if key == action.event || action.key != "D" && key == strings.ToLower(action.key) || action.key == "Space" && key == " " {
						key = action.event
						matched = true
						break
					}
				}
				if !matched {
					return m, nil
				}
			}
			m.closePane()
			return m.runSkillAction(key)
		}
		if m.pane == paneDetails && m.compact() {
			switch key {
			case "esc":
				m.pane = paneList
				m.returnPane = paneList
				m.offset = 0
				return m, nil
			case "pgdown", "ctrl+d":
				m.offset += max(1, m.height/3)
				return m, nil
			case "pgup", "ctrl+u":
				m.offset = max(0, m.offset-max(1, m.height/3))
				return m, nil
			}
		}
		if m.searching {
			switch key {
			case "esc":
				m.searching = false
				m.query = ""
			case "enter":
				m.searching = false
			case "backspace":
				r := []rune(m.query)
				if len(r) > 0 {
					m.query = string(r[:len(r)-1])
				}
			default:
				if msg.Type == tea.KeyRunes {
					m.query += string(msg.Runes)
				}
				if msg.Type == tea.KeySpace {
					m.query += " "
				}
			}
			m.cursor = 0
			return m, m.selection()
		}
		switch key {
		case "q":
			return m, tea.Quit
		case "/":
			m.searching = true
		case "esc":
			m.query = ""
			m.cursor = 0
			return m, m.selection()
		case "enter":
			if row, ok := m.selectedRow(); ok && row.kind == skillRow && m.compact() {
				m.openPane(paneDetails)
				return m, nil
			}
			m.navigateGroup(key)
			return m, m.selection()
		case "left", "right":
			m.navigateGroup(key)
			return m, m.selection()
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.rows())-1))
			return m, m.selection()
		case "k", "up":
			m.cursor = max(0, m.cursor-1)
			return m, m.selection()
		case "home", "g":
			m.cursor = 0
			return m, m.selection()
		case "end":
			m.cursor = max(0, len(m.rows())-1)
			return m, m.selection()
		case "pgdown", "ctrl+d":
			m.offset += max(1, m.height/3)
		case "pgup", "ctrl+u":
			m.offset = max(0, m.offset-max(1, m.height/3))
		case "f":
			m.cycleView()
			return m, m.selection()
		case "1", "2", "3", "4":
			view, _ := viewForShortcut(key)
			m.selectView(view)
			return m, m.selection()
		case "tab":
			c := m.service.Config
			if c.Project != "" {
				m.project = c.Project
				c.Project = ""
			} else {
				c.Project = m.project
				if c.Project == "" {
					c.Project = "auto"
				}
			}
			s, e := manager.New(c)
			if e != nil {
				m.setNotice(noticeError, failureNotice("scope", e))
				return m, nil
			}
			m.service = s
			m.result = skills.Result{}
			m.content = ""
			m.cursor = 0
			m.inventory = inventoryLoading
			m.selectView(m.filter)
			m.busy = true
			m.pane = paneList
			m.setNotice(noticeProgress, "Reading skill directories…")
			return m, m.load()
		case "R":
			m.busy = true
			m.setNotice(noticeProgress, "Reading skill directories…")
			return m, m.load()
		case "S":
			if m.service.Scope() != "project" {
				m.setNotice(noticeInfo, "Switch to a project to sync its placements.")
				return m, nil
			}
			s := m.service
			m.busy = true
			return m, func() tea.Msg {
				plan, err := s.PreviewOperation(manager.OperationRequest{Action: "sync"})
				return previewMsg{plan: plan, err: err, confirm: true}
			}
		case "A":
			m.migration = newMigration(m.result, false)
			if m.migration == nil {
				m.setNotice(noticeInfo, "No unmanaged skills are ready to add.")
				return m, nil
			}
			m.setNotice(noticeInfo, "Choose the skills you want skmr to manage.")
			return m, nil
		case "I":
			m.remoteMenu = true
			m.remoteStep = "menu"
			return m, nil
		case "G":
			m.groupMenu = true
			m.groupStep = "list"
			m.busy = true
			s := m.service
			return m, func() tea.Msg { groups, err := s.Groups(); return groupsLoadedMsg{groups, err} }
		case "x":
			if item, ok := m.selected(); ok && len(actionsForSkill(item)) > 0 {
				m.actionCursor = 0
				m.openPane(paneActions)
			}
			return m, nil
		case "!":
			if len(m.problems()) > 0 {
				m.openPane(paneProblems)
			}
			return m, nil
		case "?":
			m.openPane(paneHelp)
		}
	}
	return m, nil
}

// runSkillAction dispatches an available action selected in the Actions pane.
func (m Model) runSkillAction(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "P", "Z":
		item, ok := m.selected()
		if !ok || m.service.Config.Project == "" {
			return m, nil
		}
		action := "add"
		if key == "Z" {
			if !item.Installed {
				return m, nil
			}
			action = "remove"
		} else if !item.Inherited || !item.Managed {
			return m, nil
		}
		s := m.service
		m.busy = true
		return m, func() tea.Msg {
			plan, err := s.PreviewOperation(manager.OperationRequest{Action: action, Arguments: []string{item.Name}})
			return previewMsg{plan: plan, err: err, confirm: true}
		}
	case "D":
		item, ok := m.selected()
		if !ok || item.ReadOnly || item.Inherited {
			return m, nil
		}
		s := m.service
		m.busy = true
		return m, func() tea.Msg {
			plan, err := s.PreviewOperation(manager.OperationRequest{Action: "delete", Arguments: []string{item.ID}})
			return previewMsg{plan: plan, err: err, confirm: true}
		}
	case "u":
		item, ok := m.selected()
		if !ok || !item.Remote || item.Inherited {
			return m, nil
		}
		return m.prepareRemoteUpdate(item.ID, false)
	case "U":
		item, ok := m.selected()
		if !ok || !item.Remote || item.Inherited {
			return m, nil
		}
		return m.prepareRemoteUpdate(item.ID, true)
	case "i":
		if _, ok := m.selected(); ok {
			m.openPane(paneInstructions)
		}
		return m, nil
	case "v":
		return m, m.openDiff()
	case "o":
		item, ok := m.selected()
		if !ok || !item.Inherited && !item.Installed {
			m.setNotice(noticeWarning, "The selected skill is already in its owning scope.")
			return m, nil
		}
		owner := item.OwnerProject
		if item.Installed {
			owner = ""
		}
		service, err := manager.New(manager.Config{Home: m.service.Config.Home, DataHome: m.service.Config.DataHome, ConfigHome: m.service.Config.ConfigHome, Project: owner})
		if err != nil {
			m.setNotice(noticeError, failureNotice("owner", err))
			return m, nil
		}
		m.service = service
		m.result = skills.Result{}
		m.content = ""
		m.cursor = 0
		m.inventory = inventoryLoading
		m.selectView(m.filter)
		if item.OwnerProject != "" {
			m.project = item.OwnerProject
		}
		m.selectID = item.ID
		m.selectAction = "open-owner"
		m.busy = true
		m.setNotice(noticeProgress, "Reading owning scope…")
		return m, m.load()
	case "a", "c", "e", "d", "r", " ":
		item, ok := m.selected()
		if !ok {
			return m, nil
		}
		if item.Inherited || item.ReadOnly {
			m.setNotice(noticeWarning, "This skill is view only here. Open its owning scope to make changes.")
			return m, nil
		}
		action := map[string]string{"a": "adopt", "c": "resolve", "e": "enable", "d": "disable", "r": "restore"}[key]
		if key == " " {
			if !item.Managed {
				m.setNotice(noticeWarning, "Add this skill to the library before changing agent discovery.")
				return m, nil
			}
			action = "enable"
			if item.Enabled {
				action = "disable"
			}
		}
		if action == "resolve" && item.ConflictID == "" {
			m.setNotice(noticeWarning, "This skill has no duplicate copies.")
			return m, nil
		}
		if action == "adopt" && item.Managed {
			m.setNotice(noticeWarning, "This skill is already in the library.")
			return m, nil
		}
		if action != "adopt" && action != "resolve" && !item.Managed {
			m.setNotice(noticeWarning, "Add this skill to the library before changing agent discovery.")
			return m, nil
		}
		arg := item.ID
		if action == "adopt" {
			arg = item.Path
		}
		s := m.service
		m.busy = true
		return m, func() tea.Msg {
			p, e := s.PreviewOperation(manager.OperationRequest{Action: action, Arguments: []string{arg}})
			return previewMsg{p, e, action == "adopt" || action == "resolve" || action == "restore"}
		}
	}
	return m, nil
}

func (m Model) discoverRemote(url string) tea.Cmd {
	s := m.service
	return func() tea.Msg {
		candidates, err := s.DiscoverRemote(url)
		return remoteFoundMsg{candidates: candidates, url: url, err: err}
	}
}

func (m Model) prepareRemoteSelection() (tea.Model, tea.Cmd) {
	var names []string
	for _, candidate := range m.remoteChoices {
		if m.remoteSelected[candidate.Name] {
			names = append(names, candidate.Name)
		}
	}
	if len(names) == 0 {
		m.setNotice(noticeWarning, "Select at least one skill.")
		return m, nil
	}
	s, url := m.service, m.remoteURL
	m.busy = true
	return m, func() tea.Msg {
		plan, err := s.PreviewOperation(manager.OperationRequest{Action: "add", Arguments: []string{url}, Skills: names})
		return remotePreviewMsg{plan: plan, err: err}
	}
}

func (m Model) prepareRemoteUpdate(id string, replace bool) (tea.Model, tea.Cmd) {
	s := m.service
	m.busy = true
	return m, func() tea.Msg {
		plan, err := s.PreviewOperation(manager.OperationRequest{Action: "update", Arguments: []string{id}, Replace: replace})
		return remotePreviewMsg{plan: plan, err: err}
	}
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.width < 30 || m.height < 10 {
		return m, nil
	}
	if m.groupMenu || m.remoteMenu {
		return m, nil
	}
	if m.busy {
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && msg.Y == 1 {
			if view, ok := m.filterAt(msg.X); ok {
				m.selectView(view)
				return m, nil
			}
		}
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && msg.Y == m.height-1 {
			if key := m.footerKeyAt(msg.X); key == "q" {
				return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
			}
		}
		return m, nil
	}
	if m.migration != nil {
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			delta := 3
			if msg.Button == tea.MouseButtonWheelUp {
				delta = -3
			}
			m.migration.cursor = min(max(0, m.migration.cursor+delta), max(0, len(m.migration.rows())-1))
			return m, nil
		}
		if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
			return m, nil
		}
		if msg.Y == m.height-1 {
			if key := m.footerKeyAt(msg.X); key != "" {
				return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			}
			return m, nil
		}
		if index, ok := m.migrationRowAt(msg.Y); ok {
			m.migration.cursor = index
			rows := m.migration.rows()
			if rows[index].kind == migrationConflictRow {
				m.migration.expanded[rows[index].conflict] = !m.migration.expanded[rows[index].conflict]
				return m, nil
			}
			if reason, changed := m.migration.toggleCurrent(); !changed && reason != "" {
				m.setNotice(noticeWarning, reason)
			}
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -3
		}
		if m.pending != nil || !m.mouseInList(msg.X, msg.Y) {
			m.offset = max(0, m.offset+delta)
			return m, nil
		}
		m.cursor = min(max(0, m.cursor+delta), max(0, len(m.rows())-1))
		return m, m.selection()
	}
	if m.pending != nil {
		if msg.Y == m.height-1 {
			if key := m.footerKeyAt(msg.X); key != "" {
				return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			}
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Y == 0 {
		header := m.headerLayout(m.width)
		if msg.X >= header.scopeStart && msg.X < header.scopeEnd {
			return m.Update(tea.KeyMsg{Type: tea.KeyTab})
		}
		if msg.X >= header.problemsStart && msg.X < header.problemsEnd {
			return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
		}
	}
	if msg.Y == 1 {
		if view, ok := m.filterAt(msg.X); ok {
			m.selectView(view)
			return m, m.selection()
		}
		if msg.X >= m.searchStart() {
			m.searching = true
			return m, nil
		}
	}
	if index, ok := m.skillAt(msg.X, msg.Y); ok {
		m.cursor = index
		m.navigateGroup("enter")
		return m, m.selection()
	}
	if msg.Y == m.height-1 {
		if key := m.footerKeyAt(msg.X); key != "" {
			return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		}
	}
	return m, nil
}

func (m Model) mouseInList(x, y int) bool {
	bodyTop := m.bodyTop()
	if y < bodyTop || y >= bodyTop+m.bodyHeight() {
		return false
	}
	if m.width >= wideLayoutWidth {
		return x < m.listWidth()
	}
	return m.pane == paneList
}

func (m Model) skillAt(x, y int) (int, bool) {
	if !m.mouseInList(x, y) {
		return 0, false
	}
	contentHeight := m.bodyHeight() - 2
	row := y - m.bodyTop() - 1
	if row < 0 || row >= contentHeight {
		return 0, false
	}
	start := m.rowStart(contentHeight)
	index := start + row
	return index, index >= 0 && index < len(m.rows())
}
