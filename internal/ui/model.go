package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"atlas/internal/hermes"
)

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

	// composer / streaming
	inserting    bool
	input        string
	streaming    bool
	streamBuf    string
	streamCh     chan hermes.ChatEvent
	streamCancel context.CancelFunc
	helpVisible  bool

	// turn mirror / stats
	turnStart float64
	lastCard  string
	cardFor   string

	// transcript interaction
	scroll      int
	expandAll   bool
	jumpMsgID   int
	searchMode  bool
	searchQuery string
	searchHits  []hermes.SearchHit
	searchSel   int
	searching   bool

	// per-turn stats cards
	showCards bool
	turnCards []hermes.TurnCard
	cardLines map[int]string
	cardsFor  string
	sessTotal hermes.SessionTotal
	totalFor  string

	// live refresh / unread / stop
	read      map[string]float64
	readDirty bool
	lastSave  time.Time
	activeRun string
}

// New returns the initial model.
func New() Model {
	c := hermes.NewFromEnv()
	h := hermes.NewHubFromEnv()
	m := Model{client: c, hub: h, focus: 0, read: loadReadState()}
	if !c.Configured() {
		m.tree = demoTree()
		m.status = "demo data · set ATLAS_API_KEY for live (0.3.0)"
	} else {
		m.status = "loading…"
	}
	return m
}

// OpenSession pins the app to one session id at startup (--open).
func (m Model) OpenSession(id string) Model {
	if id == "" {
		return m
	}
	m.openID = id
	m.openTitle = id
	m.openMsgs = 0
	m.openLast = 0
	m.status = "opened " + id
	m.markRead(id)
	return m
}

func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.client.Configured() {
		cmds = append(cmds, fetchSessions(m.client))
		if m.openID != "" {
			cmds = append(cmds, fetchMessages(m.client, m.openID))
		}
	}
	if m.hub.Configured() {
		cmds = append(cmds, fetchHubTree(m.hub))
		if m.openID != "" {
			cmds = append(cmds, fetchTurnCards(m.hub, m.openID))
		}
	}
	cmds = append(cmds, tickCmd())
	return tea.Batch(cmds...)
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
			if s := m.findSession(m.openID); s != nil && (m.openTitle == m.openID || m.openTitle == "") {
				m.openTitle = sessionTitle(*s)
				m.openMsgs = s.MessageCount
				m.openLast = s.LastActive
			}
		}
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
			if m.cardsFor == m.openID {
				m.cardLines = assignCards(m.messages, m.turnCards)
			}
			if m.jumpMsgID != 0 {
				m.scroll = m.jumpScrollFor(m.jumpMsgID)
				m.jumpMsgID = 0
			}
		}
	case turnCardsMsg:
		if msg.err != nil || msg.sessionID != m.openID {
			return m, nil
		}
		m.cardsFor = msg.sessionID
		m.turnCards = msg.cards
		m.cardLines = assignCards(m.messages, msg.cards)
		m.sessTotal = msg.total
		m.totalFor = msg.sessionID
		if m.lastCard == "" && len(msg.cards) > 0 {
			m.lastCard = msg.cards[len(msg.cards)-1].Line
			m.cardFor = msg.sessionID
		}
		return m, nil
	case streamEventMsg:
		if msg.ev.RunID != "" {
			m.activeRun = msg.ev.RunID
		}
		switch msg.ev.Event {
		case "assistant.delta":
			m.streamBuf += msg.ev.Delta
		case "assistant.completed":
			if msg.ev.Content != "" {
				m.streamBuf = msg.ev.Content
			}
		case "tool.started":
			if msg.ev.ToolName != "" {
				m.status = "tool · " + msg.ev.ToolName
			}
		case "error":
			m.status = "stream error"
		}
		return m, waitStream(m.streamCh)
	case mirrorMsg:
		if msg.err != nil {
			m.status = "mirror failed: " + msg.err.Error()
		}
		return m, nil
	case cardMsg:
		if msg.err == nil && msg.line != "" {
			m.lastCard = msg.line
			m.cardFor = msg.sessionID
		}
		return m, nil
	case turnMirrorMsg:
		if msg.err != nil {
			m.status = "mirror failed: " + msg.err.Error()
		}
		return m, nil
	case tickMsg:
		var cmds []tea.Cmd
		if m.client.Configured() {
			cmds = append(cmds, fetchSessions(m.client))
			if m.openID != "" && !m.streaming && m.scroll == 0 {
				cmds = append(cmds, fetchMessages(m.client, m.openID))
			}
		}
		if m.hub.Configured() {
			cmds = append(cmds, fetchHubTree(m.hub))
		}
		// Parked on the open post at the tail = still reading it; keep the
		// read mark current so activity we can see never shows as unread.
		if m.openID != "" && m.scroll == 0 && !m.inserting && !m.searchMode && !m.helpVisible {
			m.markRead(m.openID)
		}
		cmds = append(cmds, tickCmd())
		return m, tea.Batch(cmds...)
	case stopRunMsg:
		if msg.err != nil {
			m.status = "stop failed: " + msg.err.Error()
		} else {
			m.status = "stop requested — the turn is aborting"
		}
		return m, nil
	case streamClosedMsg:
		m.streaming = false
		m.streamBuf = ""
		m.streamCh = nil
		m.streamCancel = nil
		m.activeRun = ""
		m.status = "turn complete"
		var cmds []tea.Cmd
		if m.client.Configured() && m.openID != "" {
			cmds = append(cmds, fetchMessages(m.client, m.openID))
		}
		if m.hub.Configured() && m.openID != "" {
			cmds = append(cmds, fetchCard(m.hub, m.openID, m.turnStart))
			cmds = append(cmds, fetchTurnCards(m.hub, m.openID))
			cmds = append(cmds, mirrorTurnCmd(m.hub, m.openID, m.turnStart))
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
	case searchMsg:
		m.searching = false
		if msg.err != nil {
			m.status = "search failed: " + msg.err.Error()
			return m, nil
		}
		m.searchHits = msg.hits
		m.searchSel = 0
		if len(msg.hits) == 0 {
			m.status = "no matches"
		} else {
			m.status = fmt.Sprintf("%d matches — ↑↓ pick · enter opens", len(msg.hits))
		}
		return m, nil
	case tea.KeyMsg:
		if m.searchMode {
			return m.updateSearch(msg)
		}
		if m.helpVisible {
			switch msg.String() {
			case "?", "esc", "q":
				m.helpVisible = false
			}
			return m, nil
		}
		if m.inserting {
			return m.updateInsert(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			saveReadState(m.read)
			m.interrupt()
			return m, tea.Quit
		case "i":
			m.inserting = true
			m.status = "insert — type a message, enter sends"
		case "?":
			m.helpVisible = true
		case "/":
			m.searchMode = true
			m.searchQuery = ""
			m.searchHits = nil
			m.searchSel = 0
			m.status = "search — type a query, enter runs it"
		case "e":
			m.expandAll = !m.expandAll
		case "c":
			m.showCards = !m.showCards
			if m.showCards {
				m.status = "cards: every turn"
			} else {
				m.status = "cards: latest only"
			}
		case "esc":
			if m.streaming {
				m.interrupt()
			} else {
				m.scroll = 0
				m.status = "bottom"
			}
		case "j", "down":
			m = m.moveDown()
		case "k", "up":
			m = m.moveUp()
		case "pgdown":
			m.scroll = clampInt(m.scroll-10, 0, 1<<30)
		case "pgup":
			m.scroll += 10
		case "g":
			if m.focus == 1 {
				m.scroll = 1 << 30 // clamped at render
			} else {
				m.cursor = 0
			}
		case "G":
			if m.focus == 1 {
				m.scroll = 0
			} else if len(m.tree) > 0 {
				m.cursor = len(m.tree) - 1
			}
		case "R":
			var cmds []tea.Cmd
			if m.client.Configured() {
				cmds = append(cmds, fetchSessions(m.client))
				if m.openID != "" {
					cmds = append(cmds, fetchMessages(m.client, m.openID))
				}
			}
			if m.hub.Configured() {
				cmds = append(cmds, fetchHubTree(m.hub))
			}
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			m.status = "nothing to refresh (demo mode)"
		case "x":
			if m.activeRun != "" {
				m.status = "stopping the running turn…"
				return m, stopRunCmd(m.client, m.activeRun)
			}
			m.status = "no active turn to stop"
		case "tab", "shift+tab":
			m.focus = (m.focus + 1) % 2
		case "enter":
			if cmd := m.openSelection(); cmd != nil {
				return m, cmd
			}
		default:
			// Burst input (fast repeats, pastes) arrives as one multi-rune
			// KeyMsg; apply per-rune movement so kkkk / jjjj all land.
			for _, r := range msg.Runes {
				switch r {
				case 'j':
					m = m.moveDown()
				case 'k':
					m = m.moveUp()
				}
			}
		}
	}
	return m, nil
}

func (m Model) moveDown() Model {
	if m.focus == 1 {
		if m.scroll > 0 {
			m.scroll--
		}
		return m
	}
	if m.cursor < len(m.tree)-1 {
		m.cursor++
	}
	return m
}

func (m Model) moveUp() Model {
	if m.focus == 1 {
		m.scroll++
		return m
	}
	if m.cursor > 0 {
		m.cursor--
	}
	return m
}

func (m Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.interrupt()
		return m, tea.Quit
	case "esc":
		m.inserting = false
		m.status = "normal"
	case "enter":
		if m.streaming {
			m.status = "a turn is already running — esc to detach"
			return m, nil
		}
		text := strings.TrimSpace(m.input)
		if text == "" {
			return m, nil
		}
		if !m.client.Configured() {
			m.status = "can't send — no api connection (demo mode)"
			return m, nil
		}
		if m.openID == "" {
			m.status = "open a post first (enter on the tree)"
			return m, nil
		}
		m.input = ""
		m.inserting = false
		return m, m.beginTurn(text)
	case "backspace":
		if len(m.input) > 0 {
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
		}
	case "space":
		m.input += " "
	default:
		if len(msg.Runes) > 0 {
			m.input += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	hasResults := !m.searching && len(m.searchHits) > 0
	switch msg.String() {
	case "ctrl+c":
		m.interrupt()
		return m, tea.Quit
	case "esc":
		m.searchMode = false
		m.searchQuery = ""
		m.searchHits = nil
		m.searchSel = 0
		m.status = "normal"
		return m, nil
	case "enter":
		if m.searching {
			return m, nil
		}
		if hasResults {
			hit := m.searchHits[clampInt(m.searchSel, 0, len(m.searchHits)-1)]
			m.searchMode = false
			m.searchQuery = ""
			m.searchHits = nil
			m.searchSel = 0
			return m, m.jumpTo(hit)
		}
		if q := strings.TrimSpace(m.searchQuery); q != "" && m.hub.Configured() {
			m.searching = true
			m.status = "searching…"
			return m, searchCmd(m.hub, q)
		}
		return m, nil
	case "up":
		if hasResults && m.searchSel > 0 {
			m.searchSel--
		}
		return m, nil
	case "down":
		if hasResults && m.searchSel < len(m.searchHits)-1 {
			m.searchSel++
		}
		return m, nil
	case "backspace":
		if len(m.searchQuery) > 0 {
			r := []rune(m.searchQuery)
			m.searchQuery = string(r[:len(r)-1])
			m.searchHits = nil
		}
		return m, nil
	case "space":
		m.searchQuery += " "
		m.searchHits = nil
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.searchQuery += string(msg.Runes)
			m.searchHits = nil
		}
		return m, nil
	}
}

// jumpTo opens the hit's session (if needed) and scrolls to the message.
func (m *Model) jumpTo(hit hermes.SearchHit) tea.Cmd {
	if hit.SessionID == m.openID {
		m.jumpMsgID = hit.MessageID
		m.scroll = m.jumpScrollFor(hit.MessageID)
		m.jumpMsgID = 0
		m.status = fmt.Sprintf("jumped to message %d", hit.MessageID)
		return nil
	}
	n := treeNode{label: hit.Title, kind: kindPost, sessionID: hit.SessionID}
	cmd := m.open(n)
	m.jumpMsgID = hit.MessageID
	m.status = "opening " + hit.Title
	return cmd
}

// beginTurn echoes the user message and starts an SSE turn on the open session.
func (m *Model) beginTurn(text string) tea.Cmd {
	m.streaming = true
	m.streamBuf = ""
	m.messages = append(m.messages, hermes.Message{
		Role:      "user",
		Content:   hermes.FlexibleText(text),
		Timestamp: float64(time.Now().Unix()),
	})
	m.status = "streaming…"
	m.turnStart = float64(time.Now().Unix()) - 1
	m.lastCard = ""
	m.cardFor = ""
	m.scroll = 0
	ctx, cancel := context.WithCancel(context.Background())
	m.streamCancel = cancel
	ch := make(chan hermes.ChatEvent, 256)
	m.streamCh = ch
	id := m.openID
	c := m.client
	go func() {
		defer close(ch)
		err := c.ChatStream(ctx, id, text, func(ev hermes.ChatEvent) {
			select {
			case ch <- ev:
			case <-ctx.Done():
			}
		})
		if err != nil && ctx.Err() == nil {
			select {
			case ch <- hermes.ChatEvent{Event: "error", Delta: err.Error()}:
			default:
			}
		}
	}()
	cmds := []tea.Cmd{waitStream(ch)}
	if m.hub.Configured() {
		cmds = append(cmds, mirrorMessage(m.hub, id, "user", text))
	}
	return tea.Batch(cmds...)
}

func (m *Model) interrupt() {
	if m.streamCancel != nil {
		m.streamCancel()
		m.status = "detaching from the stream…"
	}
}

// ---- unread / read-state ----

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
	m.markRead(n.sessionID)
	m.messages = nil
	m.scroll = 0
	m.lastCard = ""
	m.cardFor = ""
	m.cardLines = nil
	m.cardsFor = ""
	m.sessTotal = hermes.SessionTotal{}
	m.totalFor = ""
	cmds := []tea.Cmd{fetchMessages(m.client, n.sessionID)}
	if m.hub.Configured() {
		cmds = append(cmds, fetchTurnCards(m.hub, n.sessionID))
	}
	return tea.Batch(cmds...)
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
	if m.openID != "" {
		if s := m.findSession(m.openID); s != nil {
			m.openTitle = sessionTitle(*s)
			m.openMsgs = s.MessageCount
			m.openLast = s.LastActive
		}
		if ms, err := m.client.Messages(ctx, m.openID, 100000); err == nil {
			m.messages = ms
		}
		return m
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

// msgCount reports the open conversation's message count, preferring the
// live-loaded transcript length over the (possibly stale) session record.
func (m Model) msgCount() int {
	if n := len(m.messages); n > m.openMsgs {
		return n
	}
	return m.openMsgs
}
