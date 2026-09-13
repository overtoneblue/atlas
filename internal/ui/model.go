package ui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"atlas/internal/hermes"
)

type nodeKind int

const (
	kindGuild nodeKind = iota
	kindCategory
	kindChannel
	kindPost
)

type treeNode struct {
	label      string
	depth      int
	kind       nodeKind
	sessionID  string
	msgCount   int
	lastActive float64
}

// Model is the whole app state.
type Model struct {
	width, height int
	focus         int // 0 = tree, 1 = transcript, 2 = composer
	cursor        int
	tree          []treeNode
	status        string

	client   *hermes.Client
	hub      *hermes.Hub
	sessions []hermes.Session
	messages []hermes.Message

	openID    string
	openTitle string
	openMsgs  int
	openLast  float64
}

// New returns the initial model.
func New() Model {
	c := hermes.NewFromEnv()
	h := hermes.NewHubFromEnv()
	m := Model{client: c, hub: h, focus: 0}
	if !c.Configured() {
		m.tree = demoTree()
		m.status = "demo data · set ATLAS_API_KEY for live (0.2.0)"
	} else {
		m.status = "loading…"
	}
	return m
}

func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.client.Configured() {
		cmds = append(cmds, fetchSessions(m.client))
	}
	if m.hub.Configured() {
		cmds = append(cmds, fetchHubTree(m.hub))
	}
	return tea.Batch(cmds...)
}

type sessionsMsg struct {
	sessions []hermes.Session
	err      error
}

type hubMsg struct {
	tree *hermes.HubTree
	err  error
}

type messagesMsg struct {
	sessionID string
	messages  []hermes.Message
	err       error
}

func fetchSessions(c *hermes.Client) tea.Cmd {
	return func() tea.Msg {
		ss, err := c.ListSessions(context.Background(), 60)
		return sessionsMsg{sessions: ss, err: err}
	}
}

func fetchHubTree(h *hermes.Hub) tea.Cmd {
	return func() tea.Msg {
		t, err := h.FetchTree(context.Background())
		return hubMsg{tree: t, err: err}
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
			if len(m.tree) == 0 {
				m.status = "sessions load failed: " + msg.err.Error()
			}
		} else {
			m.sessions = visibleSessions(msg.sessions)
		}
		// Without a hub, the flat session list is the tree.
		if !m.hub.Configured() {
			if len(m.sessions) > 0 {
				m.tree = treeFromSessions(m.sessions)
				m.status = fmt.Sprintf("live · %d sessions", len(m.sessions))
			}
			if cmd := m.autoOpen(); cmd != nil {
				return m, cmd
			}
		}
	case hubMsg:
		if msg.err != nil {
			m.status = "hub unreachable — flat list"
			if len(m.sessions) > 0 {
				m.tree = treeFromSessions(m.sessions)
			} else if len(m.tree) == 0 {
				m.tree = demoTree()
			}
			if cmd := m.autoOpen(); cmd != nil {
				return m, cmd
			}
		} else {
			m.tree = treeFromHub(msg.tree.Sections)
			m.status = fmt.Sprintf("live · hub · %d posts", countPosts(m.tree))
			if cmd := m.autoOpen(); cmd != nil {
				return m, cmd
			}
		}
	case messagesMsg:
		if msg.err != nil {
			m.status = "messages load failed: " + msg.err.Error()
			return m, nil
		}
		if m.openID == msg.sessionID {
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
			if len(m.tree) > 0 {
				m.cursor = len(m.tree) - 1
			}
		case "R":
			var cmds []tea.Cmd
			if m.client.Configured() {
				cmds = append(cmds, fetchSessions(m.client))
			}
			if m.hub.Configured() {
				cmds = append(cmds, fetchHubTree(m.hub))
			}
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			m.status = "nothing to refresh (demo mode)"
		case "tab":
			m.focus = (m.focus + 1) % 3
		case "shift+tab":
			m.focus = (m.focus + 2) % 3
		case "enter":
			if cmd := m.openSelection(); cmd != nil {
				return m, cmd
			}
		case "?":
			m.status = "j/k move · enter open · R refresh · q quit"
		}
	}
	return m, nil
}

// autoOpen opens the first post in the tree when nothing is open yet.
func (m *Model) autoOpen() tea.Cmd {
	if m.openID != "" || !m.client.Configured() {
		return nil
	}
	for _, n := range m.tree {
		if n.kind == kindPost && n.sessionID != "" {
			return m.open(n)
		}
	}
	return nil
}

func (m *Model) open(n treeNode) tea.Cmd {
	m.openID = n.sessionID
	m.openTitle = n.label
	m.openMsgs = n.msgCount
	m.openLast = n.lastActive
	m.messages = nil
	return fetchMessages(m.client, n.sessionID)
}

// openSelection maps the tree cursor to a session when it's on a post row.
func (m *Model) openSelection() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.tree) {
		return nil
	}
	n := m.tree[m.cursor]
	if n.kind != kindPost || n.sessionID == "" || !m.client.Configured() {
		return nil
	}
	return m.open(n)
}

// LoadSync does a blocking load for --once renders (no event loop).
func (m Model) LoadSync() Model {
	if !m.client.Configured() {
		return m
	}
	ctx := context.Background()
	if ss, err := m.client.ListSessions(ctx, 60); err == nil {
		m.sessions = visibleSessions(ss)
	}
	if m.hub.Configured() {
		if t, err := m.hub.FetchTree(ctx); err == nil {
			m.tree = treeFromHub(t.Sections)
			m.status = fmt.Sprintf("live · hub · %d posts", countPosts(m.tree))
		} else {
			m.status = "hub unreachable: " + err.Error()
		}
	}
	if len(m.tree) == 0 && len(m.sessions) > 0 {
		m.tree = treeFromSessions(m.sessions)
		m.status = fmt.Sprintf("live · %d sessions", len(m.sessions))
	}
	for _, n := range m.tree {
		if n.kind == kindPost && n.sessionID != "" {
			m.openID = n.sessionID
			m.openTitle = n.label
			m.openMsgs = n.msgCount
			m.openLast = n.lastActive
			if ms, err := m.client.Messages(ctx, n.sessionID, 100000); err == nil {
				m.messages = ms
			}
			break
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
	nodes := []treeNode{{label: "RECENT SESSIONS", kind: kindCategory}}
	for _, s := range ss {
		nodes = append(nodes, treeNode{
			label:      sessionTitle(s),
			depth:      1,
			kind:       kindPost,
			sessionID:  s.ID,
			msgCount:   s.MessageCount,
			lastActive: s.LastActive,
		})
	}
	return nodes
}

func treeFromHub(sections []hermes.HubNode) []treeNode {
	var out []treeNode
	var walk func(n hermes.HubNode, depth int)
	walk = func(n hermes.HubNode, depth int) {
		var kind nodeKind
		switch n.Kind {
		case "guild":
			kind = kindGuild
		case "category":
			kind = kindCategory
		case "channel":
			kind = kindChannel
		default:
			kind = kindPost
		}
		out = append(out, treeNode{
			label:      n.Name,
			depth:      depth,
			kind:       kind,
			sessionID:  n.SessionID,
			msgCount:   n.MessageCount,
			lastActive: n.LastActive,
		})
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	for _, s := range sections {
		walk(s, 0)
	}
	return out
}

func countPosts(tree []treeNode) int {
	n := 0
	for _, t := range tree {
		if t.kind == kindPost {
			n++
		}
	}
	return n
}

func (m Model) findSession(id string) *hermes.Session {
	for i := range m.sessions {
		if m.sessions[i].ID == id {
			return &m.sessions[i]
		}
	}
	return nil
}
