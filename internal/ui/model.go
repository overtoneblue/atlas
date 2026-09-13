package ui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"atlas/internal/hermes"
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

	// Hermes wiring (live mode)
	client   *hermes.Client
	sessions []hermes.Session
	messages []hermes.Message
	openIdx  int // index into sessions of the open conversation; -1 = none
}

// New returns the initial model.
func New() Model {
	c := hermes.NewFromEnv()
	m := Model{
		client:  c,
		focus:   0,
		openIdx: -1,
	}
	if !c.Configured() {
		m.tree = demoTree()
		m.status = "demo data · set ATLAS_API_KEY for live (0.1.0)"
	} else {
		m.status = "loading sessions…"
	}
	return m
}

func (m Model) Init() tea.Cmd {
	if m.client.Configured() {
		return fetchSessions(m.client)
	}
	return nil
}

type sessionsMsg struct {
	sessions []hermes.Session
	err      error
}

type messagesMsg struct {
	sessionID string
	messages  []hermes.Message
	err       error
}

func fetchSessions(c *hermes.Client) tea.Cmd {
	return func() tea.Msg {
		ss, err := c.ListSessions(context.Background(), 40)
		return sessionsMsg{sessions: ss, err: err}
	}
}

func fetchMessages(c *hermes.Client, id string) tea.Cmd {
	return func() tea.Msg {
		ms, err := c.Messages(context.Background(), id, 100000)
		return messagesMsg{sessionID: id, messages: ms, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case sessionsMsg:
		if msg.err != nil {
			m.tree = demoTree()
			m.status = "session load failed: " + msg.err.Error()
			return m, nil
		}
		m.sessions = visibleSessions(msg.sessions)
		m.tree = treeFromSessions(m.sessions)
		m.status = fmt.Sprintf("live · %d sessions", len(m.sessions))
		if len(m.sessions) > 0 {
			m.openIdx = 0
			return m, fetchMessages(m.client, m.sessions[0].ID)
		}
	case messagesMsg:
		if msg.err != nil {
			m.status = "messages load failed: " + msg.err.Error()
			return m, nil
		}
		if m.openIdx >= 0 && m.openIdx < len(m.sessions) && m.sessions[m.openIdx].ID == msg.sessionID {
			m.messages = msg.messages
		}
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
		case "R":
			if m.client.Configured() {
				m.status = "reloading sessions…"
				return m, fetchSessions(m.client)
			}
		case "tab":
			m.focus = (m.focus + 1) % 3
		case "shift+tab":
			m.focus = (m.focus + 2) % 3
		case "enter":
			m = m.openSelection()
			if m.client.Configured() && m.openIdx >= 0 && m.openIdx < len(m.sessions) {
				return m, fetchMessages(m.client, m.sessions[m.openIdx].ID)
			}
		case "?":
			m.status = "j/k move · g/G ends · enter open · R refresh · q quit"
		}
	}
	return m, nil
}

// openSelection maps the tree cursor to a session when in live mode.
func (m Model) openSelection() Model {
	if len(m.sessions) == 0 {
		return m
	}
	// Tree layout in live mode: [category header] + one row per session.
	idx := m.cursor - 1
	if idx < 0 || idx >= len(m.sessions) {
		return m
	}
	m.openIdx = idx
	m.messages = nil
	m.status = "opened " + sessionTitle(m.sessions[idx])
	return m
}

// LoadSync does a blocking load for --once renders (no event loop).
func (m Model) LoadSync() Model {
	if !m.client.Configured() {
		return m
	}
	ctx := context.Background()
	ss, err := m.client.ListSessions(ctx, 40)
	if err != nil {
		m.status = "session load failed: " + err.Error()
		m.tree = demoTree()
		return m
	}
	m.sessions = visibleSessions(ss)
	m.tree = treeFromSessions(m.sessions)
	m.status = fmt.Sprintf("live · %d sessions", len(m.sessions))
	if len(m.sessions) > 0 {
		m.openIdx = 0
		if ms, err := m.client.Messages(ctx, m.sessions[0].ID, 100000); err == nil {
			m.messages = ms
		}
	}
	return m
}

func visibleSessions(in []hermes.Session) []hermes.Session {
	out := make([]hermes.Session, 0, len(in))
	for _, s := range in {
		if s.Hidden || s.Archived {
			continue
		}
		out = append(out, s)
	}
	return out
}

func sessionTitle(s hermes.Session) string {
	if s.Title != "" {
		return s.Title
	}
	if s.Preview != "" {
		return s.Preview
	}
	return s.ID
}

func treeFromSessions(ss []hermes.Session) []treeNode {
	nodes := []treeNode{{label: "RECENT SESSIONS", depth: 0, kind: kindCategory}}
	for _, s := range ss {
		nodes = append(nodes, treeNode{label: sessionTitle(s), depth: 1, kind: kindPost})
	}
	return nodes
}
