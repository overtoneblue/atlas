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
	subs    map[int]*subscriber
	nextSub int

	// per-session live info + route-guard bookkeeping (session.go)
	infoMu sync.Mutex
	sess   sessionState

	// api/hub reachability, probed in the background (session.go)
	healthMu sync.Mutex
	health   map[string]LinkHealth

	// lastErr remembers each chat's latest turn failure (text + when), so a
	// reload still explains the "Your request was not processed" row Hermes
	// leaves behind — the real cause only ever rode the live event.
	errMu   sync.Mutex
	lastErr map[string]TurnFailure

	version string // atlasd build version (ldflags), for the status bar
	boot    string // per-process id: clients detect a daemon restart
	started time.Time

	// slash-command catalog cache (serve restarts don't change it often;
	// a stale copy is served when the service is briefly unreachable)
	catMu    sync.Mutex
	catCache *hermes.Catalog
	catAt    time.Time

	// stored session id -> live runtime id in hermes-serve (resume mints
	// one; commands address sessions by runtime id)
	rtMu      sync.Mutex
	runtimes  map[string]string // stored -> runtime
	byRuntime map[string]string // runtime -> stored (serve events name runtimes)
	profileOf map[string]string // stored -> profile home it lives in
	lastSeq   map[string]int64  // runtime -> newest serve event seq seen (replay dedupe)

	initialSession string
}

// activeTurn is the bookkeeping for one in-flight agent turn.
type activeTurn struct {
	profile   string
	runtime   string // hermes-serve runtime session id this turn runs on
	done      chan struct{}
	deltas    int
	chars     int
	stopped   bool
	lastEvent time.Time // watchdog: a turn that goes quiet gets re-checked

	// segments mirrors the live-transcript shape the UI renders, so a
	// client attaching mid-turn (app reopen, second window) gets the whole
	// turn so far instead of only the deltas after it showed up.
	segments []TurnSegment
}

// TurnSegment is one live-transcript block (assistant text or a tool row).
type TurnSegment struct {
	Type  string `json:"type"` // "text" | "tool" | "reasoning"
	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"`
	State string `json:"state,omitempty"` // tool: running | done
}

// TurnState is the mid-turn snapshot served to late-attaching clients.
type TurnState struct {
	SessionID string        `json:"session_id"`
	Profile   string        `json:"profile"`
	Segments  []TurnSegment `json:"segments"`
}

// TurnBrief names one live turn (status payload).
type TurnBrief struct {
	Session string `json:"session"`
	Profile string `json:"profile"`
}

func New() *Service {
	d := newBare()
	d.api = hermes.NewFromEnv()
	d.hub = hermes.NewHubFromEnv()
	d.serve = hermes.NewServeFromEnv()
	return d
}

// newBare builds a Service with every map initialised and no upstreams
// (tests fill in what they need).
func newBare() *Service {
	return &Service{
		turns:     make(map[string]*activeTurn),
		subs:      make(map[int]*subscriber),
		runtimes:  make(map[string]string),
		byRuntime: make(map[string]string),
		profileOf: make(map[string]string),
		lastSeq:   make(map[string]int64),
		sess:      newSessionState(),
		health:    make(map[string]LinkHealth),
		lastErr:   make(map[string]TurnFailure),
		boot:      fmt.Sprintf("%x", time.Now().UnixNano()),
		started:   time.Now(),
	}
}

// TurnFailure is a chat's latest failed turn, as serve explained it.
type TurnFailure struct {
	Error string  `json:"error"`
	Code  string  `json:"code,omitempty"` // serve error_surface code (rate_limit, auth, …)
	At    float64 `json:"at"`
}

func (d *Service) noteFailure(stored string, f TurnFailure) {
	d.errMu.Lock()
	d.lastErr[stored] = f
	d.errMu.Unlock()
}

func (d *Service) clearFailure(stored string) {
	d.errMu.Lock()
	delete(d.lastErr, stored)
	d.errMu.Unlock()
}

// LastFailure returns the chat's most recent turn failure (nil when its last
// turn succeeded or the daemon has not seen one).
func (d *Service) LastFailure(stored string) *TurnFailure {
	d.errMu.Lock()
	defer d.errMu.Unlock()
	if f, ok := d.lastErr[stored]; ok {
		return &f
	}
	return nil
}

// SetVersion records the build version reported in Status.
func (d *Service) SetVersion(v string) { d.version = v }

// SetInitialSession wires the --open flag: boot straight into one session.
func (d *Service) SetInitialSession(id string) { d.initialSession = id }

// InitialSession is read by the frontend during boot.
func (d *Service) InitialSession() string { return d.initialSession }

// TurnEvent is one live-turn update, delivered on the event stream.
type TurnEvent struct {
	// started | delta | reasoning | tool | done | error | info | note |
	// link | resync | hello
	Kind      string       `json:"kind"`
	SessionID string       `json:"session_id"`
	Profile   string       `json:"profile,omitempty"`
	Text      string       `json:"text,omitempty"`       // assistant text delta / note text
	Tool      string       `json:"tool,omitempty"`       // tool name
	ToolState string       `json:"tool_state,omitempty"` // running | done
	RunID     string       `json:"run_id,omitempty"`
	OK        bool         `json:"ok,omitempty"`
	Stopped   bool         `json:"stopped,omitempty"`
	Error     string       `json:"error,omitempty"`
	Code      string       `json:"code,omitempty"`      // serve error_surface code on failures
	TPS       float64      `json:"tps,omitempty"`       // rolling output tokens/sec (serve usage)
	Latency   float64      `json:"latency_s,omitempty"` // rolling avg API latency (serve usage)
	Info      *SessionInfo `json:"info,omitempty"`      // kind=info: the session's live snapshot
	Link      string       `json:"link,omitempty"`      // kind=link: serve up|down
	Boot      string       `json:"boot,omitempty"`      // kind=hello: daemon process id
}

type subscriber struct {
	ch    chan TurnEvent
	lossy bool // a frame was dropped: the next delivery leads with "resync"
}

// Subscribe registers a live-turn event stream. Cancel (unsubscribe) when
// the consumer goes away. A slow reader never stalls a turn: when its buffer
// is full the frame is dropped and the subscriber is marked lossy, and the
// next frame it gets is preceded by a "resync" so the client re-reads the
// live snapshot instead of rendering a turn with holes in it.
func (d *Service) Subscribe() (<-chan TurnEvent, func()) {
	d.subMu.Lock()
	id := d.nextSub
	d.nextSub++
	sub := &subscriber{ch: make(chan TurnEvent, 2048)}
	d.subs[id] = sub
	d.subMu.Unlock()

	cancel := func() {
		d.subMu.Lock()
		if s, ok := d.subs[id]; ok {
			delete(d.subs, id)
			close(s.ch)
		}
		d.subMu.Unlock()
	}
	return sub.ch, cancel
}

// emit fans one turn event out to every subscriber (non-blocking; sends and
// unsubscribe both happen under subMu, so no send-on-closed can race).
func (d *Service) emit(ev TurnEvent) {
	switch ev.Kind {
	case "delta", "reasoning", "info":
	default:
		log.Printf("atlas:turn %s session=%s tool=%s err=%s", ev.Kind, ev.SessionID, ev.Tool, ev.Error)
	}
	d.subMu.Lock()
	for _, s := range d.subs {
		if s.lossy {
			select {
			case s.ch <- TurnEvent{Kind: "resync"}:
				s.lossy = false
			default:
				continue // still full: stay lossy, drop this one too
			}
		}
		select {
		case s.ch <- ev:
		default: // full buffer: drop rather than stall the turn
			s.lossy = true
		}
	}
	d.subMu.Unlock()
}

// Status reports the upstreams (configured AND reachable), live turns, and
// the daemon's identity, for the status bar.
type Status struct {
	API      bool                  `json:"api"`
	Hub      bool                  `json:"hub"`
	Serve    bool                  `json:"serve"`
	APIURL   string                `json:"api_url"`
	HubURL   string                `json:"hub_url"`
	ServeURL string                `json:"serve_url"`
	Turns    []TurnBrief           `json:"turns"`
	Links    map[string]LinkHealth `json:"links"`
	Version  string                `json:"version,omitempty"`
	Boot     string                `json:"boot"`
	Uptime   float64               `json:"uptime_s"`
}

func (d *Service) Status() Status {
	d.mu.Lock()
	turns := make([]TurnBrief, 0, len(d.turns))
	for sid, t := range d.turns {
		turns = append(turns, TurnBrief{Session: sid, Profile: t.profile})
	}
	d.mu.Unlock()
	return Status{
		API:      d.api.Configured(),
		Hub:      d.hub.Configured(),
		Serve:    d.serve.Configured(),
		APIURL:   d.api.BaseURL,
		HubURL:   d.hub.BaseURL,
		ServeURL: d.serve.BaseURL,
		Turns:    turns,
		Links:    d.Links(),
		Version:  d.version,
		Boot:     d.boot,
		Uptime:   time.Since(d.started).Seconds(),
	}
}

// Hello is the first frame of every event stream: the daemon's boot id (a
// changed id = the daemon restarted under the client) and the serve link.
func (d *Service) Hello() TurnEvent {
	link := "down"
	if d.serve.Connected() {
		link = "up"
	}
	return TurnEvent{Kind: "hello", Boot: d.boot, Link: link}
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
// engine addresses sessions by their LIVE runtime id; withRuntime resumes the
// stored id (minting a runtime) and retries once on a stale one — the same
// recovery the Hermes desktop performs.
func (d *Service) ExecSlash(sessionID, command string) (*hermes.ExecResult, error) {
	if sessionID == "" || strings.TrimSpace(command) == "" {
		return nil, ErrEmpty
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	var res *hermes.ExecResult
	err := d.withRuntime(ctx, sessionID, nil, func(rt string) (e error) {
		res, e = d.serve.ExecSlash(ctx, rt, command)
		return e
	})
	if err != nil {
		var re *resumeErr
		if errors.As(err, &re) {
			return nil, fmt.Errorf("serve exec: %w", err)
		}
		// An RPC error means the command engine answered (usage text like
		// "/moa <prompt>", unknown-command hints, dead session) — that is
		// transcript output, not a transport failure.
		var rpcErr *hermes.RPCError
		if errors.As(err, &rpcErr) {
			return &hermes.ExecResult{Output: rpcErr.Message}, nil
		}
		return nil, fmt.Errorf("serve exec: %w", err)
	}
	return res, nil
}

// ModelOptions returns the model-picker payload for a stored session, bound
// to its live runtime (resumed on demand — the same binding exec uses). The
// payload is passed through as the serve built it.
func (d *Service) ModelOptions(sessionID string) (json.RawMessage, error) {
	if sessionID == "" {
		return nil, ErrEmpty
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var raw json.RawMessage
	err := d.withRuntime(ctx, sessionID, nil, func(rt string) (e error) {
		raw, e = d.serve.ModelOptions(ctx, rt)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("serve models: %w", err)
	}
	return raw, nil
}

// GetTree fetches the full workstream tree from the hub (all profiles).
func (d *Service) GetTree() (json.RawMessage, error) {
	if !d.hub.Configured() {
		return nil, errors.New("hub not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.hub.FetchTreeRaw(ctx)
}

// GetSpawned lists recent spawned work (subagent runs + pi tasks), parents
// resolved to session ids where the hub could resolve them.
func (d *Service) GetSpawned() (json.RawMessage, error) {
	if !d.hub.Configured() {
		return nil, errors.New("hub not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.hub.FetchSpawnedRaw(ctx)
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
	d.noteProfile(sessionID, profile)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.api.MessagesPage(ctx, profile, sessionID, q)
}

// TurnState returns the live snapshot for a session, nil when idle.
func (d *Service) TurnState(sessionID string) *TurnState {
	d.mu.Lock()
	defer d.mu.Unlock()
	turn, ok := d.turns[sessionID]
	if !ok {
		return nil
	}
	segs := make([]TurnSegment, len(turn.segments))
	copy(segs, turn.segments)
	return &TurnState{SessionID: sessionID, Profile: turn.profile, Segments: segs}
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

// noteSegment folds one live event into the turn's snapshot buffer (capped
// so a pathological turn can't grow memory without bound).
func (d *Service) noteSegment(turn *activeTurn, seg TurnSegment) {
	d.mu.Lock()
	defer d.mu.Unlock()
	const maxSegs, maxText = 500, 1 << 18
	if (seg.Type == "text" || seg.Type == "reasoning") && len(turn.segments) > 0 && turn.segments[len(turn.segments)-1].Type == seg.Type {
		if len(turn.segments[len(turn.segments)-1].Text) < maxText {
			turn.segments[len(turn.segments)-1].Text += seg.Text
		}
		return
	}
	turn.segments = append(turn.segments, seg)
	if len(turn.segments) > maxSegs {
		turn.segments = turn.segments[len(turn.segments)-maxSegs:]
	}
}

// completeSegment marks the newest running tool row with this name as done.
func (d *Service) completeSegment(turn *activeTurn, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(turn.segments) - 1; i >= 0; i-- {
		if s := turn.segments[i]; s.Type == "tool" && s.State == "running" && s.Name == name {
			turn.segments[i].State = "done"
			return
		}
	}
}
