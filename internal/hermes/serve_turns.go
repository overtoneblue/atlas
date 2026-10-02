package hermes

// serve_turns.go — the turn surface of hermes-serve's JSON-RPC: submit,
// interrupt, attach images, resume with running-state, event replay, and the
// Watch supervisor that keeps one event-receiving connection alive.
//
// This is the same path the official Hermes desktop uses, so the session's
// runtime owns the model, the turn and the events together (no REST/serve
// split, no fork patches).

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

// ServeEvent is one server->client "event" frame's params. Seq is a
// per-session monotonic watermark (absent on session-less events).
type ServeEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Payload   json.RawMessage `json:"payload"`
}

// ResumeInfo is what a resume tells Atlas: the live runtime id, whether a
// turn is in flight there right now, and the runtime's live settings
// snapshot (serve's session.info shape: model, provider, reasoning, usage…).
type ResumeInfo struct {
	Runtime string
	Running bool
	Info    json.RawMessage
}

// LiveInfo is the slice of serve's session.info payload Atlas reads. Every
// field is optional on purpose: serve's info grows release to release and
// Atlas must keep working when a field is renamed or dropped.
type LiveInfo struct {
	Model           string     `json:"model"`
	Provider        string     `json:"provider"`
	ReasoningEffort string     `json:"reasoning_effort"`
	ServiceTier     string     `json:"service_tier"`
	Fast            bool       `json:"fast"`
	Running         bool       `json:"running"`
	Title           string     `json:"title"`
	ProfileName     string     `json:"profile_name"`
	Contract        int        `json:"desktop_contract"`
	Usage           *LiveUsage `json:"usage"`
}

// LiveUsage is the usage snapshot serve attaches to session.info /
// session.usage / message.complete (the CLI status-bar numbers).
type LiveUsage struct {
	Model          string  `json:"model"`
	Input          int64   `json:"input"`
	Output         int64   `json:"output"`
	Total          int64   `json:"total"`
	Calls          int64   `json:"calls"`
	ContextUsed    int64   `json:"context_used"`
	ContextMax     int64   `json:"context_max"`
	ContextPercent float64 `json:"context_percent"`
	ContextEst     bool    `json:"context_estimated"`
	CacheHitPct    float64 `json:"cache_hit_pct"`
	AvgTPS         float64 `json:"avg_tps"`
	AvgLatency     float64 `json:"avg_latency_s"`
}

// ParseLiveInfo decodes a session.info payload leniently (nil on garbage).
func ParseLiveInfo(raw json.RawMessage) *LiveInfo {
	if len(raw) == 0 {
		return nil
	}
	var li LiveInfo
	if json.Unmarshal(raw, &li) != nil {
		return nil
	}
	return &li
}

// ResumeWith revives a stored session as a live runtime (attaching THIS
// connection as a viewer) and reports whether a turn is running. profile
// selects a non-default profile home ("" / "default" = the launch profile).
// omit_messages + eager_build are the pair that keeps the reply small and the
// runtime identity resolved (see Resume).
func (s *Serve) ResumeWith(ctx context.Context, sessionID, profile string) (ResumeInfo, error) {
	params := map[string]any{
		"session_id":    sessionID,
		"source":        "desktop",
		"omit_messages": true,
		"eager_build":   true,
	}
	if profile != "" && profile != "default" {
		params["profile"] = profile
	}
	var out resumeWire
	if err := s.call(ctx, "session.resume", params, &out); err != nil {
		return ResumeInfo{}, err
	}
	return ResumeInfo{Runtime: out.SessionID, Running: out.Running, Info: out.Info}, nil
}

// ConfigSet runs one session-scoped config.set (reasoning, fast, …) against
// a live runtime — the same contract the official desktop's settings use.
// Session-scoped keys never touch config.yaml: a pick in one chat stays in
// that chat.
func (s *Serve) ConfigSet(ctx context.Context, runtimeID, key, value string) (map[string]any, error) {
	params := map[string]any{"key": key, "value": value, "session_id": runtimeID}
	var out map[string]any
	if err := s.call(ctx, "config.set", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Submit sends one user turn to a live runtime. Status is streaming | queued
// | steered | redirected (a busy session queues/steers instead of refusing).
func (s *Serve) Submit(ctx context.Context, runtimeID, text string) (string, error) {
	var out struct {
		Status string `json:"status"`
	}
	if err := s.call(ctx, "prompt.submit", map[string]any{"session_id": runtimeID, "text": text}, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

// Interrupt stops the runtime's in-flight turn (the serve then ends it with
// a message.complete carrying status "interrupted").
func (s *Serve) Interrupt(ctx context.Context, runtimeID string) error {
	return s.call(ctx, "session.interrupt", map[string]any{"session_id": runtimeID}, nil)
}

// AttachImageBytes queues a base64 image for the runtime's next turn.
func (s *Serve) AttachImageBytes(ctx context.Context, runtimeID, b64, filename string) error {
	return s.call(ctx, "image.attach_bytes", map[string]any{
		"session_id":     runtimeID,
		"content_base64": b64,
		"filename":       filename,
	}, nil)
}

// ActiveSession is one row of serve's live-session list.
type ActiveSession struct {
	ID         string `json:"id"`          // runtime id
	SessionKey string `json:"session_key"` // stored id
	Status     string `json:"status"`      // idle | starting | waiting | working | streaming | resuming
	Title      string `json:"title"`
}

// Running reports whether a turn is (or is about to be) in flight.
func (a ActiveSession) Running() bool {
	switch a.Status {
	case "starting", "waiting", "working", "streaming":
		return true
	}
	return false
}

// ActiveList lists the live runtimes serve holds for a profile home.
func (s *Serve) ActiveList(ctx context.Context, profile string) ([]ActiveSession, error) {
	params := map[string]any{}
	if profile != "" && profile != "default" {
		params["profile"] = profile
	}
	var out struct {
		Sessions []ActiveSession `json:"sessions"`
	}
	if err := s.call(ctx, "session.active_list", params, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// SetHidden sets/clears a session's hidden flag — out of every Hermes list,
// nothing deleted, still resumable by its owner. id may be the live runtime
// id or the stored id (the latter updates the profile db tier).
func (s *Serve) SetHidden(ctx context.Context, id, profile string, hidden bool) error {
	params := map[string]any{"session_id": id, "hidden": hidden}
	if profile != "" && profile != "default" {
		params["profile"] = profile
	}
	return s.call(ctx, "session.set_hidden", params, nil)
}

// CreateSession mints a fresh live chat (source "atlas" files it under the
// profile's Atlas channel in the tree) and returns its stored and runtime ids.
// A non-empty seed rides session.create's messages: display_kind "hidden"
// rows are model-facing scaffolding the UI never paints (guided-chat runbook)
// — that is how a channel's guidelines reach the agent without cluttering
// the transcript.
func (s *Serve) CreateSession(ctx context.Context, profile string, seed []map[string]any) (stored, runtime string, err error) {
	params := map[string]any{"source": "atlas"}
	if profile != "" && profile != "default" {
		params["profile"] = profile
	}
	if len(seed) > 0 {
		params["messages"] = seed
	}
	var out struct {
		Runtime string `json:"session_id"`
		Stored  string `json:"stored_session_id"`
	}
	if err := s.call(ctx, "session.create", params, &out); err != nil {
		return "", "", err
	}
	return out.Stored, out.Runtime, nil
}

// ConfigSetModel routes a model switch through serve's confirm-capable
// config.set contract. The slash /model path flattens the large-context (or
// cost/policy) guard into plain output with no way to confirm — a dead end
// in clients. This returns the deferred-confirm envelope untouched:
// {value, warning, confirm_required, confirm_message, scope}; re-send with
// confirmed=true once the user confirms.
func (s *Serve) ConfigSetModel(ctx context.Context, sessionID, value string, confirmed bool) (map[string]any, error) {
	params := map[string]any{
		"key": "model", "value": value, "session_id": sessionID,
		"confirm_expensive_model": confirmed,
	}
	var out map[string]any
	if err := s.call(ctx, "config.set", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CloseSession releases a live runtime (required before deleting it).
func (s *Serve) CloseSession(ctx context.Context, runtimeID string) error {
	return s.call(ctx, "session.close", map[string]any{"session_id": runtimeID}, nil)
}

// DeleteStored removes a stored session (and its transcript) from its
// profile's store. A live runtime must be closed first (4023 otherwise).
func (s *Serve) DeleteStored(ctx context.Context, storedID, profile string) error {
	params := map[string]any{"session_id": storedID}
	if profile != "" && profile != "default" {
		params["profile"] = profile
	}
	return s.call(ctx, "session.delete", params, nil)
}

// EventsSince is serve's bounded replay ring. Truncated means the gap fell
// off the ring — the caller must refetch state instead of trusting Events.
type EventsSince struct {
	Events    []ServeEvent `json:"events"`
	LatestSeq int64        `json:"latest_seq"`
	Truncated bool         `json:"truncated"`
}

func (s *Serve) EventsSince(ctx context.Context, runtimeID string, lastSeen int64) (*EventsSince, error) {
	var out EventsSince
	if err := s.call(ctx, "session.events.since", map[string]any{"session_id": runtimeID, "last_seen": lastSeen}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Watch keeps ONE connection dialed so server events always have a
// receiver: onEvent fires per event frame (from the read loop — it must not
// block or make RPCs), onReconnect fires in its own goroutine each time the
// connection is re-established after a drop (resume + resync running turns).
//
// onState (optional) fires on every up/down edge so clients can show the real
// link state instead of "a token is configured".
func (s *Serve) Watch(onEvent func(ServeEvent), onReconnect func(), onState func(up bool)) {
	s.mu.Lock()
	s.onEvent = onEvent
	s.mu.Unlock()
	state := func(up bool) {
		if onState != nil {
			onState(up)
		}
	}
	go func() {
		backoff := time.Second
		var last *wsConn
		warned := false
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			conn, err := s.getConn(ctx)
			cancel()
			if err != nil {
				if !warned {
					log.Printf("atlasd: serve watch: %v (retrying)", err)
					warned = true
				}
				time.Sleep(backoff)
				if backoff < 15*time.Second {
					backoff *= 2
				}
				continue
			}
			backoff, warned = time.Second, false
			state(true)
			if last != nil && conn != last && onReconnect != nil {
				log.Printf("atlasd: serve connection re-established")
				go onReconnect()
			}
			last = conn
			<-conn.closed
			s.noteErr(conn.closeErr())
			s.dropIf(conn)
			state(false)
			time.Sleep(300 * time.Millisecond)
		}
	}()
}
