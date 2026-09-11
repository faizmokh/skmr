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
	plan    manager.Plan
	err     error
	confirm bool
}
type transferPreviewMsg struct {
	plan        manager.TransferPlan
	destination *manager.Service
	err         error
}
type appliedMsg struct {
	err    error
	action string
}
type batchPreviewMsg struct {
	plan manager.BatchPlan
	err  error
}
type batchAppliedMsg struct {
	applyErr error
	setupErr error
	count    int
	skipped  int
}
type setupMarkedMsg struct{ err error }
type transferAppliedMsg struct {
	err         error
	action      string
	destination *manager.Service
	id          string
}
type pendingTransfer struct {
	plan        manager.TransferPlan
	destination *manager.Service
}
type contentMsg struct{ id, content string }

type Model struct {
	service         *manager.Service
	project         string
	result          skills.Result
	cursor          int
	expanded        map[string]bool
	query           string
	searching       bool
	filter          skillView
	width, height   int
	content         string
	offset          int
	pending         *manager.Plan
	pendingBatch    *manager.BatchPlan
	pendingTransfer *pendingTransfer
	migration       *migrationState
	transferAction  string
	transferInput   string
	transferItem    string
	selectID        string
	selectAction    string
	pane            paneMode
	returnPane      paneMode
	actionCursor    int
	diff            copyDiff
	diffTarget      int
	diffHorizontal  int
	diffLoading     bool
	inventory       inventoryPhase
	busy            bool
	notice          notice
}

func New(s *manager.Service) Model {
	return Model{service: s, project: s.Config.Project, width: 100, height: 30, inventory: inventoryLoading, busy: true, notice: notice{text: "Reading skill directories…", level: noticeProgress}}
}
func Run(s *manager.Service) error {
	_, err := tea.NewProgram(New(s), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
func (m Model) Init() tea.Cmd { return m.load() }
func (m Model) load() tea.Cmd {
	s := m.service
	return func() tea.Msg { r, e := s.List(); return loadedMsg{r, e} }
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
		m.pendingBatch = nil
		m.pendingTransfer = nil
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
			m.openPane(paneReview)
			m.pending = &msg.plan
			return m, nil
		}
		m.busy = true
		s := m.service
		return m, func() tea.Msg { return appliedMsg{s.Apply(msg.plan), msg.plan.Action} }
	case batchPreviewMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("batch-preview", msg.err))
			return m, nil
		}
		m.pendingBatch = &msg.plan
		m.offset = 0
		return m, nil
	case batchAppliedMsg:
		m.busy = false
		m.pendingBatch = nil
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
	case transferPreviewMsg:
		m.busy = false
		if msg.err != nil {
			m.setNotice(noticeError, failureNotice("transfer-preview", msg.err))
			return m, nil
		}
		m.openPane(paneReview)
		m.pendingTransfer = &pendingTransfer{msg.plan, msg.destination}
		return m, nil
	case appliedMsg:
		m.busy = false
		m.pending = nil
		if msg.err != nil {
			m.closePane()
			m.setNotice(noticeError, failureNotice("apply", msg.err))
		} else {
			if item, ok := m.selected(); ok {
				m.selectID = item.ID
				m.selectAction = msg.action
			}
			m.setNotice(noticeSuccess, successNotice(msg.action))
		}
		m.busy = true
		return m, m.load()
	case transferAppliedMsg:
		m.busy = false
		m.pendingTransfer = nil
		if msg.err != nil {
			m.closePane()
			m.setNotice(noticeError, failureNotice("transfer-apply", msg.err))
			return m, nil
		}
		m.service = msg.destination
		m.result = skills.Result{}
		m.content = ""
		m.cursor = 0
		m.inventory = inventoryLoading
		m.selectView(m.filter)
		if msg.destination.Config.Project != "" {
			m.project = msg.destination.Config.Project
		}
		m.selectID = msg.id
		m.selectAction = msg.action
		m.setNotice(noticeSuccess, successNotice(msg.action))
		m.busy = true
		return m, m.load()
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
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
		if m.pending != nil || m.pendingBatch != nil || m.pendingTransfer != nil {
			if key == "pgdown" {
				m.offset += max(1, m.height/3)
			}
			if key == "pgup" {
				m.offset = max(0, m.offset-max(1, m.height/3))
			}
			if key == "esc" || key == "n" {
				if m.pendingBatch != nil {
					m.pendingBatch = nil
					m.offset = 0
					m.setNotice(noticeInfo, "Review your skill selection.")
					return m, nil
				}
				m.pending = nil
				m.pendingTransfer = nil
				m.closePane()
				m.setNotice(noticeInfo, "No changes made.")
			}
			if key == "q" {
				return m, tea.Quit
			}
			if key == "y" {
				m.busy = true
				if m.pendingBatch != nil {
					plan := *m.pendingBatch
					m.pendingBatch = nil
					initial := m.migration != nil && m.migration.initial
					skipped := 0
					if m.migration != nil {
						skipped = m.migration.skippedCount()
					}
					s := m.service
					return m, func() tea.Msg {
						applyErr := s.ApplyBatch(plan)
						var setupErr error
						if applyErr == nil && initial {
							setupErr = s.MarkSetup("completed")
						}
						return batchAppliedMsg{applyErr: applyErr, setupErr: setupErr, count: len(plan.Plans), skipped: skipped}
					}
				}
				if m.pendingTransfer != nil {
					p := *m.pendingTransfer
					m.pendingTransfer = nil
					s := m.service
					return m, func() tea.Msg {
						return transferAppliedMsg{s.ApplyTransfer(p.destination, p.plan), p.plan.Action, p.destination, p.plan.DestinationRecord.ID}
					}
				}
				p := *m.pending
				m.pending = nil
				s := m.service
				return m, func() tea.Msg { return appliedMsg{s.Apply(p), p.Action} }
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
					plan, err := s.PreviewBatchAdopt(paths)
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
					if key == action.event || key == strings.ToLower(action.key) || action.key == "Space" && key == " " {
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
		if m.transferAction != "" {
			switch key {
			case "esc":
				m.transferAction, m.transferInput, m.transferItem = "", "", ""
				m.setNotice(noticeInfo, "No changes made.")
				return m, nil
			case "enter":
				destinationText := strings.TrimSpace(m.transferInput)
				if destinationText == "" {
					m.setNotice(noticeWarning, "Enter global or a project directory.")
					return m, nil
				}
				project := destinationText
				if strings.EqualFold(destinationText, "global") {
					project = ""
				}
				destination, err := manager.New(manager.Config{Home: m.service.Config.Home, DataHome: m.service.Config.DataHome, ConfigHome: m.service.Config.ConfigHome, Project: project})
				if err != nil {
					m.setNotice(noticeError, failureNotice("destination", err))
					return m, nil
				}
				action, id, source := m.transferAction, m.transferItem, m.service
				m.transferAction, m.transferInput, m.transferItem = "", "", ""
				m.busy = true
				return m, func() tea.Msg {
					plan, previewErr := source.PreviewTransfer(action, id, destination)
					return transferPreviewMsg{plan, destination, previewErr}
				}
			case "backspace":
				runes := []rune(m.transferInput)
				if len(runes) > 0 {
					m.transferInput = string(runes[:len(runes)-1])
				}
			default:
				if msg.Type == tea.KeyRunes {
					m.transferInput += string(msg.Runes)
				} else if msg.Type == tea.KeySpace {
					m.transferInput += " "
				}
			}
			return m, nil
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
		case "end", "G":
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
		case "A":
			m.migration = newMigration(m.result, false)
			if m.migration == nil {
				m.setNotice(noticeInfo, "No unmanaged skills are ready to add.")
				return m, nil
			}
			m.setNotice(noticeInfo, "Choose the skills you want skmr to manage.")
			return m, nil
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
	case "i":
		if _, ok := m.selected(); ok {
			m.openPane(paneInstructions)
		}
		return m, nil
	case "v":
		return m, m.openDiff()
	case "m", "p":
		item, ok := m.selected()
		if !ok {
			return m, nil
		}
		if item.Inherited || item.ReadOnly || !item.Managed {
			m.setNotice(noticeWarning, "Only a library skill owned by this scope can be transferred.")
			return m, nil
		}
		m.transferAction = map[string]string{"m": "move", "p": "copy"}[key]
		m.transferItem = item.ID
		m.transferInput = ""
		if m.service.Config.Project != "" {
			m.transferInput = "global"
		} else if m.project != "" {
			m.transferInput = m.project
		}
		m.setNotice(noticeInfo, "Enter global or a project directory, then press Enter to review.")
		return m, nil
	case "o":
		item, ok := m.selected()
		if !ok || !item.Inherited {
			m.setNotice(noticeWarning, "The selected skill is already in its owning scope.")
			return m, nil
		}
		service, err := manager.New(manager.Config{Home: m.service.Config.Home, DataHome: m.service.Config.DataHome, ConfigHome: m.service.Config.ConfigHome, Project: item.OwnerProject})
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
			p, e := s.Preview(action, arg)
			return previewMsg{p, e, action == "adopt" || action == "resolve" || action == "restore"}
		}
	}
	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.width < 30 || m.height < 10 {
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
		if m.pending != nil || m.pendingBatch != nil || m.pendingTransfer != nil || !m.mouseInList(msg.X, msg.Y) {
			m.offset = max(0, m.offset+delta)
			return m, nil
		}
		m.cursor = min(max(0, m.cursor+delta), max(0, len(m.rows())-1))
		return m, m.selection()
	}
	if m.pending != nil || m.pendingBatch != nil || m.pendingTransfer != nil || m.transferAction != "" {
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
