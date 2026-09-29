// Package daemon is Atlas's data service: one process that owns every HTTP
// conversation with the Hermes API server and the hub, and re-exposes the
// results as a small localhost bridge (HTTP + Server-Sent Events).
//
// It is the transport-agnostic seam that all shells share — the Electron
// window, a browser tab, and (later) the phone speak this same API — and it
// is where every bearer key stays, on the Go side, deliberately.
//
// This is a direct port of the Wails DataService (desktop/data.go): the
// logic is identical; only the transport changed (Wails event emitter →
// SSE fan-out).
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"atlas/internal/hermes"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	ErrBusy   = errors.New("a turn is already running in this session")
	ErrNoTurn = errors.New("no active turn for this session")
	ErrEmpty  = errors.New("empty message")
)

// Service is the single bridge to head services.
type Service struct {
	api   *hermes.Client
	hub   *hermes.Hub
	serve *hermes.Serve

	mu    sync.Mutex
	turns map[string]*activeTurn // one in-flight turn per session

	subMu   sync.Mutex
	subs    map[int]chan TurnEvent
	nextSub int

	// slash-command catalog cache (serve restarts don't change it often;
	// a stale copy is served when the service is briefly unreachable)
	catMu    sync.Mutex
	catCache *hermes.Catalog
	catAt    time.Time

	// stored session id -> live runtime id in hermes-serve (resume mints
	// one; commands address sessions by runtime id)
	rtMu     sync.Mutex
	runtimes map[string]string

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

func New() *Service {
	return &Service{
		api:      hermes.NewFromEnv(),
		hub:      hermes.NewHubFromEnv(),
		serve:    hermes.NewServeFromEnv(),
		turns:    make(map[string]*activeTurn),
		subs:     make(map[int]chan TurnEvent),
		runtimes: make(map[string]string),
	}
}

// SetInitialSession wires the --open flag: boot straight into one session.
func (d *Service) SetInitialSession(id string) { d.initialSession = id }

// InitialSession is read by the frontend during boot.
func (d *Service) InitialSession() string { return d.initialSession }

// TurnEvent is one live-turn update, delivered on the event stream.
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

// Subscribe registers a live-turn event stream. Cancel (unsubscribe) when
// the consumer goes away; late events are dropped per-subscriber so one
// slow reader can never stall a turn.
func (d *Service) Subscribe() (<-chan TurnEvent, func()) {
	d.subMu.Lock()
	id := d.nextSub
	d.nextSub++
	ch := make(chan TurnEvent, 256)
	d.subs[id] = ch
	d.subMu.Unlock()

	cancel := func() {
		d.subMu.Lock()
		if c, ok := d.subs[id]; ok {
			delete(d.subs, id)
			close(c)
		}
		d.subMu.Unlock()
	}
	return ch, cancel
}

// emit fans one turn event out to every subscriber (non-blocking; sends and
// unsubscribe both happen under subMu, so no send-on-closed can race).
func (d *Service) emit(ev TurnEvent) {
	if ev.Kind != "delta" {
		log.Printf("atlas:turn %s session=%s tool=%s err=%s", ev.Kind, ev.SessionID, ev.Tool, ev.Error)
	}
	d.subMu.Lock()
	for _, ch := range d.subs {
		select {
		case ch <- ev:
		default: // full buffer: drop rather than stall the turn
		}
	}
	d.subMu.Unlock()
}

// Status reports which upstreams are configured, for the status bar.
type Status struct {
	API      bool   `json:"api"`
	Hub      bool   `json:"hub"`
	Serve    bool   `json:"serve"`
	APIURL   string `json:"api_url"`
	HubURL   string `json:"hub_url"`
	ServeURL string `json:"serve_url"`
}

func (d *Service) Status() Status {
	return Status{
		API:      d.api.Configured(),
		Hub:      d.hub.Configured(),
		Serve:    d.serve.Configured(),
		APIURL:   d.api.BaseURL,
		HubURL:   d.hub.BaseURL,
		ServeURL: d.serve.BaseURL,
	}
}

// GetCatalog returns the slash-command catalog from hermes-serve, cached
// briefly; a stale cache is served when the service is unreachable, so the
// palette degrades instead of emptying.
func (d *Service) GetCatalog(refresh bool) (*hermes.Catalog, error) {
	d.catMu.Lock()
	defer d.catMu.Unlock()
	if !refresh && d.catCache != nil && time.Since(d.catAt) < 10*time.Minute {
		return d.catCache, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cat, err := d.serve.Catalog(ctx)
	if err != nil {
		if d.catCache != nil {
			return d.catCache, nil
		}
		return nil, fmt.Errorf("serve catalog: %w", err)
	}
	d.catCache, d.catAt = cat, time.Now()
	return cat, nil
}

// CompleteSlash proxies ranked slash/skill completions for the composer.
func (d *Service) CompleteSlash(text, sessionID string) ([]hermes.Completion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.serve.CompleteSlash(ctx, text, sessionID)
}

// ExecSlash runs one slash command against the open session. The command
// engine addresses sessions by their LIVE runtime id; for a stored id we
// resume once (minting a runtime), remember the mapping, and retry — the
// same recovery the Hermes desktop performs.
func (d *Service) ExecSlash(sessionID, command string) (*hermes.ExecResult, error) {
	if sessionID == "" || strings.TrimSpace(command) == "" {
		return nil, ErrEmpty
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	d.rtMu.Lock()
	sid := d.runtimes[sessionID]
	d.rtMu.Unlock()
	if sid == "" {
		sid = sessionID
	}
	res, err := d.serve.ExecSlash(ctx, sid, command)
	var rpc *hermes.RPCError
	if errors.As(err, &rpc) && rpc.Code == 4001 {
		runtime, rerr := d.serve.Resume(ctx, sessionID)
		if rerr != nil {
			return nil, fmt.Errorf("serve exec: %w", err)
		}
		if runtime == "" {
			runtime = sessionID
		}
		d.rtMu.Lock()
		d.runtimes[sessionID] = runtime
		d.rtMu.Unlock()
		if runtime != sid {
			res, err = d.serve.ExecSlash(ctx, runtime, command)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("serve exec: %w", err)
	}
	return res, nil
}

// GetTree fetches the full workstream tree from the hub (all profiles).
func (d *Service) GetTree() (*hermes.HubTree, error) {
	if !d.hub.Configured() {
		return nil, errors.New("hub not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.hub.FetchTree(ctx)
}

// GetSpawned lists recent spawned work (subagent runs + pi tasks), parents
// resolved to session ids where the hub could resolve them.
func (d *Service) GetSpawned() (*hermes.SpawnList, error) {
	if !d.hub.Configured() {
		return nil, errors.New("hub not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.hub.FetchSpawned(ctx)
}

// GetSpawnLog tails one spawned run's live log (delegation task-N.log or a
// pi task log; the hub constrains the paths).
func (d *Service) GetSpawnLog(kind, id string, task, lines int) (string, error) {
	if !d.hub.Configured() {
		return "", errors.New("hub not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return d.hub.FetchSpawnLog(ctx, kind, id, task, lines)
}

// GetSessions is the flat fallback list when the hub is unavailable.
func (d *Service) GetSessions(limit int) ([]hermes.Session, error) {
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

// GetMessages reads one conversation's transcript page.
func (d *Service) GetMessages(profile, sessionID string, q hermes.MessageQuery) ([]hermes.Message, error) {
	if !d.api.ConfiguredFor(profile) {
		return nil, errors.New("no API key for profile " + profile)
	}
	if q.Limit <= 0 || q.Limit > 2000 {
		q.Limit = 400
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.api.MessagesPage(ctx, profile, sessionID, q)
}

// SendMessage starts one agent turn asynchronously. input is the raw JSON
// `input` the chat endpoint accepts: a plain string, or a content-parts
// array (text + image_url — native vision). Deltas and lifecycle updates
// stream back on the event channel; the call returns immediately.
func (d *Service) SendMessage(profile, sessionID string, input json.RawMessage) error {
	if !d.api.ConfiguredFor(profile) {
		return errors.New("no API key for profile " + profile)
	}
	if emptyInput(input) {
		return ErrEmpty
	}

	d.mu.Lock()
	if _, busy := d.turns[sessionID]; busy {
		d.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	turn := &activeTurn{profile: profile, cancel: cancel, done: make(chan struct{})}
	d.turns[sessionID] = turn
	d.mu.Unlock()

	d.emit(TurnEvent{Kind: "started", SessionID: sessionID, Profile: profile})
	go d.runTurn(ctx, turn, sessionID, input)
	return nil
}

// emptyInput rejects null / blank-string / empty-array payloads.
func emptyInput(input json.RawMessage) bool {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil && strings.TrimSpace(s) == "" {
			return true
		}
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err == nil && len(arr) == 0 {
			return true
		}
	}
	return false
}

// StopTurn asks the API server to interrupt the session's in-flight run.
// If the run id has not arrived yet, it waits briefly for it; failing that
// it detaches the local stream (the run may finish server-side).
func (d *Service) StopTurn(sessionID string) error {
	d.mu.Lock()
	turn, ok := d.turns[sessionID]
	if !ok {
		d.mu.Unlock()
		return ErrNoTurn
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

func (d *Service) stopRun(profile, runID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return d.api.StopRun(ctx, profile, runID)
}

// runTurn consumes the SSE stream from the API server, relaying each event.
func (d *Service) runTurn(ctx context.Context, turn *activeTurn, sessionID string, input json.RawMessage) {
	defer func() {
		d.mu.Lock()
		delete(d.turns, sessionID)
		deltas, chars := turn.deltas, turn.chars
		d.mu.Unlock()
		close(turn.done)
		log.Printf("atlas:turn done session=%s deltas=%d chars=%d", sessionID, deltas, chars)
	}()

	err := d.api.ChatStream(ctx, turn.profile, sessionID, input, func(ev hermes.ChatEvent) {
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
