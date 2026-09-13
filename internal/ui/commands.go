package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"atlas/internal/hermes"
)

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

type streamEventMsg struct{ ev hermes.ChatEvent }
type streamClosedMsg struct{}
type stopRunMsg struct{ err error }
type tickMsg time.Time

// tickCmd schedules the quiet periodic refresh (tree + sessions, transcript
// only while parked at the tail).
func tickCmd() tea.Cmd {
	return tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// stopRunCmd asks the API server to interrupt a running turn.
func stopRunCmd(c *hermes.Client, runID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return stopRunMsg{err: c.StopRun(ctx, runID)}
	}
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

type mirrorMsg struct {
	role string
	ok   bool
	err  error
}

func mirrorMessage(h *hermes.Hub, sessionID, role, content string) tea.Cmd {
	return func() tea.Msg {
		ok, err := h.Mirror(context.Background(), sessionID, role, content)
		return mirrorMsg{role: role, ok: ok, err: err}
	}
}

type cardMsg struct {
	sessionID string
	line      string
	err       error
}

type turnMirrorMsg struct {
	ok  bool
	err error
}

func fetchCard(h *hermes.Hub, sessionID string, since float64) tea.Cmd {
	return func() tea.Msg {
		line, err := h.Card(context.Background(), sessionID, since)
		return cardMsg{sessionID: sessionID, line: line, err: err}
	}
}

func mirrorTurnCmd(h *hermes.Hub, sessionID string, since float64) tea.Cmd {
	return func() tea.Msg {
		ok, err := h.MirrorTurn(context.Background(), sessionID, since)
		return turnMirrorMsg{ok: ok, err: err}
	}
}

type searchMsg struct {
	hits []hermes.SearchHit
	err  error
}

func searchCmd(h *hermes.Hub, q string) tea.Cmd {
	return func() tea.Msg {
		hits, err := h.Search(context.Background(), q, 40)
		return searchMsg{hits: hits, err: err}
	}
}

type turnCardsMsg struct {
	sessionID string
	cards     []hermes.TurnCard
	total     hermes.SessionTotal
	err       error
}

func fetchTurnCards(h *hermes.Hub, sessionID string) tea.Cmd {
	return func() tea.Msg {
		cards, total, err := h.TurnCards(context.Background(), sessionID)
		return turnCardsMsg{sessionID: sessionID, cards: cards, total: total, err: err}
	}
}

// assignCards maps each turn card to the turn's final assistant message.
func assignCards(msgs []hermes.Message, cards []hermes.TurnCard) map[int]string {
	out := map[int]string{}
	if len(msgs) == 0 || len(cards) == 0 {
		return out
	}
	ai := 0
	for _, c := range cards {
		best := -1
		for ai < len(msgs) && msgs[ai].Timestamp <= c.EndTS+1.0 {
			if msgs[ai].Role == "assistant" {
				best = ai
			}
			ai++
		}
		if best >= 0 {
			out[msgs[best].ID] = c.Line
		}
	}
	return out
}

func waitStream(ch chan hermes.ChatEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return streamEventMsg{ev: ev}
	}
}
