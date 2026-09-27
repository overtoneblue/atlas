package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"atlas/internal/hermes"
)

// DataService is the app's single bridge to head services. ALL HTTP happens
// here, on the Go side: no CORS in the webview, no bearer keys in the
// frontend, and the exact same client code the TUI uses (atlas/internal/hermes).
// This is also the seam where a head-side daemon slots in later without the
// frontend noticing.
type DataService struct {
	api *hermes.Client
	hub *hermes.Hub

	mu    sync.Mutex
	turns map[string]*activeTurn // one in-flight turn per session

	initialSession string
}

// activeTurn is the bookkeeping for one in-flight agent turn.
type activeTurn struct {
	profile string
	runID   string // arrives on run.started; needed for server-side stop
	cancel  context.CancelFunc
	done    chan struct{}
	deltas  int
	chars   int
	stopped bool
}

func NewDataService() *DataService {
	return &DataService{
		api:   hermes.NewFromEnv(),
		hub:   hermes.NewHubFromEnv(),
		turns: make(map[string]*activeTurn),
	}
}

// SetInitialSession wires the --open flag: boot straight into one session.
func (d *DataService) SetInitialSession(id string) { d.initialSession = id }

// InitialSession is read by the frontend during boot.
func (d *DataService) InitialSession() string { return d.initialSession }

// TurnEvent is one live-turn update, pushed to the frontend on "atlas:turn".
type TurnEvent struct {
	Kind      string `json:"kind"` // started | delta | tool | done | error
	SessionID string `json:"session_id"`
	Profile   string `json:"profile,omitempty"`
	Text      string `json:"text,omitempty"`       // assistant text delta
	Tool      string `json:"tool,omitempty"`       // tool name
	ToolState string `json:"tool_state,omitempty"` // running | done
	RunID     string `json:"run_id,omitempty"`
	OK        bool   `json:"ok,omitempty"`
	Stopped   bool   `json:"stopped,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Status reports which upstreams are configured, for the status bar.
type Status struct {
	API    bool   `json:"api"`
	Hub    bool   `json:"hub"`
	APIURL string `json:"api_url"`
	HubURL string `json:"hub_url"`
}

func (d *DataService) Status() Status {
	return Status{
		API:    d.api.Configured(),
		Hub:    d.hub.Configured(),
		APIURL: d.api.BaseURL,
		HubURL: d.hub.BaseURL,
	}
}

// GetTree fetches the full workstream tree from the hub (all profiles).
func (d *DataService) GetTree() (*hermes.HubTree, error) {
	if !d.hub.Configured() {
		return nil, errors.New("hub not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.hub.FetchTree(ctx)
}

// GetSessions is the flat fallback list when the hub is unavailable.
func (d *DataService) GetSessions(limit int) ([]hermes.Session, error) {
	if !d.api.Configured() {
		return nil, errors.New("API server not configured")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return d.api.ListSessions(ctx, limit)
}

// GetMessages reads one conversation's transcript.
func (d *DataService) GetMessages(profile, sessionID string, limit int) ([]hermes.Message, error) {
	if !d.api.ConfiguredFor(profile) {
		return nil, errors.New("no API key for profile " + profile)
	}
	if limit <= 0 || limit > 2000 {
		limit = 400
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.api.Messages(ctx, profile, sessionID, limit)
}

// SendMessage starts one agent turn asynchronously. Deltas and lifecycle
// updates stream back on the "atlas:turn" event as they arrive; the call
// returns immediately.
func (d *DataService) SendMessage(profile, sessionID, text string) error {
	if !d.api.ConfiguredFor(profile) {
		return errors.New("no API key for profile " + profile)
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("empty message")
	}

	d.mu.Lock()
	if _, busy := d.turns[sessionID]; busy {
		d.mu.Unlock()
		return errors.New("a turn is already running in this session")
	}
	ctx, cancel := context.WithCancel(context.Background())
	turn := &activeTurn{profile: profile, cancel: cancel, done: make(chan struct{})}
	d.turns[sessionID] = turn
	d.mu.Unlock()

	d.emit(TurnEvent{Kind: "started", SessionID: sessionID, Profile: profile})
	go d.runTurn(ctx, turn, sessionID, text)
	return nil
}

// StopTurn asks the API server to interrupt the session's in-flight run.
// If the run id has not arrived yet, it waits briefly for it; failing that
// it detaches the local stream (the run may finish server-side).
func (d *DataService) StopTurn(sessionID string) error {
	d.mu.Lock()
	turn, ok := d.turns[sessionID]
	if !ok {
		d.mu.Unlock()
		return errors.New("no active turn for this session")
	}
	runID := turn.runID
	d.mu.Unlock()

	if runID != "" {
		if err := d.stopRun(turn.profile, runID); err != nil {
			return err
		}
		d.mu.Lock()
		turn.stopped = true
		d.mu.Unlock()
		return nil
	}
	go func() {
		select {
		case <-turn.done:
			return
		case <-time.After(2500 * time.Millisecond):
		}
		d.mu.Lock()
		runID := turn.runID
		profile := turn.profile
		d.mu.Unlock()
		if runID != "" {
			if err := d.stopRun(profile, runID); err == nil {
				d.mu.Lock()
				turn.stopped = true
				d.mu.Unlock()
			}
			return
		}
		turn.cancel() // detach; server-side run is left to finish
	}()
	return nil
}

func (d *DataService) stopRun(profile, runID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return d.api.StopRun(ctx, profile, runID)
}

// runTurn consumes the SSE stream, relaying each event to the frontend.
func (d *DataService) runTurn(ctx context.Context, turn *activeTurn, sessionID, text string) {
	defer func() {
		d.mu.Lock()
		delete(d.turns, sessionID)
		deltas, chars := turn.deltas, turn.chars
		d.mu.Unlock()
		close(turn.done)
		log.Printf("atlas:turn done session=%s deltas=%d chars=%d", sessionID, deltas, chars)
	}()

	err := d.api.ChatStream(ctx, turn.profile, sessionID, text, func(ev hermes.ChatEvent) {
		switch ev.Event {
		case "run.started":
			if ev.RunID != "" {
				d.mu.Lock()
				turn.runID = ev.RunID
				d.mu.Unlock()
			}
		case "assistant.delta":
			if ev.Delta != "" {
				d.mu.Lock()
				turn.deltas++
				turn.chars += len(ev.Delta)
				n, chars := turn.deltas, turn.chars
				d.mu.Unlock()
				if n == 1 || n%50 == 0 {
					log.Printf("atlas:turn delta session=%s n=%d chars=%d", sessionID, n, chars)
				}
				d.emit(TurnEvent{Kind: "delta", SessionID: sessionID, Text: ev.Delta})
			}
		case "tool.started":
			if ev.ToolName != "" {
				d.emit(TurnEvent{Kind: "tool", SessionID: sessionID, Tool: ev.ToolName, ToolState: "running"})
			}
		case "tool.completed":
			if ev.ToolName != "" {
				d.emit(TurnEvent{Kind: "tool", SessionID: sessionID, Tool: ev.ToolName, ToolState: "done"})
			}
		}
	})

	ok := true
	if err != nil && ctx.Err() == nil {
		ok = false
		d.emit(TurnEvent{Kind: "error", SessionID: sessionID, Error: err.Error()})
	}
	d.mu.Lock()
	stopped := turn.stopped
	d.mu.Unlock()
	d.emit(TurnEvent{Kind: "done", SessionID: sessionID, OK: ok, Stopped: stopped})
}

// emit pushes one turn event over the Wails event bridge ("atlas:turn").
func (d *DataService) emit(ev TurnEvent) {
	if ev.Kind != "delta" {
		log.Printf("atlas:turn %s session=%s tool=%s err=%s", ev.Kind, ev.SessionID, ev.Tool, ev.Error)
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("atlas:turn", ev)
	}
}
