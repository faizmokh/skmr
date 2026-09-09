// Package tui implements a keyboard-driven view over the shared manager service.
package tui

import (
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
type appliedMsg struct {
	err    error
	action string
}
type contentMsg struct{ id, content string }

type Model struct {
	service       *manager.Service
	project       string
	result        skills.Result
	cursor        int
	query         string
	searching     bool
	filter        int
	width, height int
	content       string
	offset        int
	pending       *manager.Plan
	busy          bool
	message       string
	showHelp      bool
}

func New(s *manager.Service) Model {
	return Model{service: s, project: s.Config.Project, width: 100, height: 30, busy: true, message: "Reading skill directories…"}
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
	for _, s := range m.result.Skills {
		if m.filter == 1 && !s.Managed || m.filter == 2 && s.Managed || m.filter == 3 && !s.Inherited {
			continue
		}
		if !strings.Contains(strings.ToLower(s.Name+" "+s.Description+" "+s.Path), q) {
			continue
		}
		out = append(out, s)
	}
	return out
}
func (m Model) selected() (skills.Skill, bool) {
	items := m.items()
	if m.cursor < 0 || m.cursor >= len(items) {
		return skills.Skill{}, false
	}
	return items[m.cursor], true
}
func (m *Model) selection() tea.Cmd {
	m.content = ""
	m.offset = 0
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
		m.pending = nil
		if msg.err != nil {
			m.message = "Could not read skills: " + msg.err.Error()
			return m, nil
		}
		m.result = msg.result
		m.cursor = min(m.cursor, max(0, len(m.items())-1))
		if strings.HasPrefix(m.message, "Reading") {
			m.message = "Select a skill to inspect its source and instructions."
		}
		return m, m.selection()
	case contentMsg:
		if item, ok := m.selected(); ok && item.ID == msg.id {
			m.content = msg.content
		}
	case previewMsg:
		m.busy = false
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
		}
		if msg.confirm {
			m.offset = 0
			m.pending = &msg.plan
			return m, nil
		}
		m.busy = true
		s := m.service
		return m, func() tea.Msg { return appliedMsg{s.Apply(msg.plan), msg.plan.Action} }
	case appliedMsg:
		m.busy = false
		m.pending = nil
		if msg.err != nil {
			m.message = msg.err.Error()
		} else {
			m.message = "Done: " + msg.action + ". Agent sessions may need to reload skills."
		}
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
			if key == "q" {
				return m, tea.Quit
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
				m.pending = nil
				m.message = "No changes made."
			}
			if key == "y" {
				p := *m.pending
				m.pending = nil
				m.busy = true
				s := m.service
				return m, func() tea.Msg { return appliedMsg{s.Apply(p), p.Action} }
			}
			return m, nil
		}
		if m.searching {
			switch key {
			case "esc", "enter":
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
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.items())-1))
			return m, m.selection()
		case "k", "up":
			m.cursor = max(0, m.cursor-1)
			return m, m.selection()
		case "home", "g":
			m.cursor = 0
			return m, m.selection()
		case "end", "G":
			m.cursor = max(0, len(m.items())-1)
			return m, m.selection()
		case "pgdown", "ctrl+d":
			m.offset += max(1, m.height/3)
		case "pgup", "ctrl+u":
			m.offset = max(0, m.offset-max(1, m.height/3))
		case "f":
			m.filter = (m.filter + 1) % 4
			m.cursor = 0
			return m, m.selection()
		case "1", "2", "3", "4":
			m.filter = int(key[0] - '1')
			m.cursor = 0
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
				m.message = e.Error()
				return m, nil
			}
			m.service = s
			m.cursor = 0
			m.busy = true
			m.message = "Reading skill directories…"
			return m, m.load()
		case "R":
			m.busy = true
			m.message = "Reading skill directories…"
			return m, m.load()
		case "a", "e", "d", "r":
			item, ok := m.selected()
			if !ok {
				return m, nil
			}
			if item.Inherited || item.ReadOnly {
				m.message = "This skill is read-only in this scope. Switch scope or manage its original source."
				return m, nil
			}
			action := map[string]string{"a": "adopt", "e": "enable", "d": "disable", "r": "restore"}[key]
			if action == "adopt" && item.Managed {
				m.message = "This skill is already managed."
				return m, nil
			}
			if action != "adopt" && !item.Managed {
				m.message = "Adopt this skill before changing its discovery links."
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
				return previewMsg{p, e, action == "adopt" || action == "restore"}
			}
		case "?":
			m.showHelp = !m.showHelp
			m.offset = 0
		}
	}
	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.busy || m.width < 30 || m.height < 10 {
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
		m.cursor = min(max(0, m.cursor+delta), max(0, len(m.items())-1))
		return m, m.selection()
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Y == 0 && msg.X >= 7 && msg.X < 18 {
		return m.Update(tea.KeyMsg{Type: tea.KeyTab})
	}
	if msg.Y == 1 {
		if filter := m.filterAt(msg.X); filter >= 0 {
			m.filter = filter
			m.cursor = 0
			return m, m.selection()
		}
		if msg.X >= m.searchStart() {
			m.searching = true
			return m, nil
		}
	}
	if index, ok := m.skillAt(msg.X, msg.Y); ok {
		m.cursor = index
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
	if y < 2 || y >= 2+m.bodyHeight() {
		return false
	}
	if m.width >= wideLayoutWidth {
		return x < m.listWidth()
	}
	return y < 2+m.stackedListHeight()
}

func (m Model) skillAt(x, y int) (int, bool) {
	if !m.mouseInList(x, y) {
		return 0, false
	}
	contentHeight := m.bodyHeight() - 2
	if m.width < wideLayoutWidth {
		contentHeight = m.stackedListHeight() - 2
	}
	row := y - 3
	if row < 0 || row >= contentHeight {
		return 0, false
	}
	start := max(0, m.cursor-contentHeight/2)
	start = min(start, max(0, len(m.items())-contentHeight))
	index := start + row
	return index, index >= 0 && index < len(m.items())
}
