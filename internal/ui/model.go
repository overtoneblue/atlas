package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type nodeKind int

const (
	kindCategory nodeKind = iota
	kindChannel
	kindPost
)

type treeNode struct {
	label  string
	depth  int
	kind   nodeKind
	unread int
}

// Model is the whole app state.
type Model struct {
	width, height int
	focus         int // 0 = tree, 1 = transcript, 2 = composer
	cursor        int
	tree          []treeNode
	status        string
}

// New returns the initial model.
func New() Model {
	return Model{
		tree:   demoTree(),
		focus:  0,
		status: "mock data · Hermes wiring next (0.0.1)",
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.cursor < len(m.tree)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "g":
			m.cursor = 0
		case "G":
			m.cursor = len(m.tree) - 1
		case "tab":
			m.focus = (m.focus + 1) % 3
		case "shift+tab":
			m.focus = (m.focus + 2) % 3
		case "enter":
			if len(m.tree) > 0 {
				m.status = "open " + m.tree[m.cursor].label + " — comes with the Hermes wiring"
			}
		case "?":
			m.status = "j/k move · g/G ends · tab focus · enter open · q quit"
		}
	}
	return m, nil
}
