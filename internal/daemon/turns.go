package daemon

// turns.go — the turn lifecycle over hermes-serve.
//
// Atlas drives turns through the same JSON-RPC surface the official Hermes
// desktop uses (prompt.submit / session.interrupt / session.resume /
// session.events.since), so ONE session runtime owns the model, the turn and
// the events — no REST/serve split. A turn outlives its client: serve parks a
// detached session (20s grace) and only interrupts a turn whose activity
// clock went idle for 600s, so a dropped socket or an atlasd restart does not
// kill a run. After a reconnect we re-attach (resume) and replay what we
// missed; serve's replay ring is bounded, so a truncated replay is fine — the
// frontend refetches the transcript when the turn ends.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"atlas/internal/hermes"
)

const (
	turnWatchEvery = 30 * time.Second
	turnQuiet      = 120 * time.Second // no events this long => ask serve if it is still running
	turnGrace      = 10 * time.Second  // never declare a fresh turn dead
)

// Start connects the serve event stream (reconnecting forever) and starts
// the stuck-turn watchdog. Safe to skip (tests, serve unconfigured).
func (d *Service) Start() {
	go d.probeLoop() // api/hub health is useful even without serve
	if !d.serve.Configured() {
		log.Printf("atlasd: hermes-serve not configured — turns, commands and models unavailable")
		return
	}
	d.serve.Watch(d.onServeEvent, d.onServeReconnect, d.onServeLink)
	go d.turnWatchdog()
	go d.adoptWhenReady()
}

// onServeLink relays serve socket up/down edges to every client so the
// status bar shows the real link state (and a reconnect can resync).
func (d *Service) onServeLink(up bool) {
	link := "down"
	if up {
		link = "up"
	}
	d.emit(TurnEvent{Kind: "link", Link: link})
}

// adoptWhenReady adopts turns already running on serve (started by a previous
// atlasd, another client, or before a restart), retrying until serve answers.
func (d *Service) adoptWhenReady() {
	for i := 0; i < 20; i++ {
		if d.adoptRunning() {
			return
		}
		time.Sleep(3 * time.Second)
	}
}

// adoptRunning registers every turn serve reports as in flight — for the
// default profile plus any profile Atlas has already talked to — so a daemon
// restart mid-turn re-attaches (live view + stop) instead of going blind.
// Reports whether serve could be asked.
func (d *Service) adoptRunning() bool {
	profiles := map[string]bool{"default": true}
	d.rtMu.Lock()
	for _, p := range d.profileOf {
		profiles[p] = true
	}
	d.rtMu.Unlock()
	reached := false
	for prof := range profiles {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		rows, err := d.serve.ActiveList(ctx, prof)
		cancel()
		if err != nil {
			continue
		}
		reached = true
		for _, r := range rows {
			if !r.Running() || r.SessionKey == "" {
				continue
			}
			d.mu.Lock()
			_, have := d.turns[r.SessionKey]
			var turn *activeTurn
			if !have {
				turn = &activeTurn{profile: prof, runtime: r.ID, done: make(chan struct{}), lastEvent: time.Now()}
				d.turns[r.SessionKey] = turn
			}
			d.mu.Unlock()
			if have {
				continue
			}
			d.rtMu.Lock()
			d.bindRuntimeLocked(r.SessionKey, r.ID)
			d.profileOf[r.SessionKey] = prof
			d.rtMu.Unlock()
			log.Printf("atlas:turn adopt session=%s runtime=%s status=%s", r.SessionKey, r.ID, r.Status)
			d.emit(TurnEvent{Kind: "started", SessionID: r.SessionKey, Profile: prof})
			go d.reconcileTurn(r.SessionKey, turn) // attach as viewer + replay what we missed
		}
	}
	return reached
}

// ---- runtime binding ------------------------------------------------------

// bindRuntimeLocked records stored<->runtime. Caller holds rtMu. A changed
// runtime id means the old runtime is gone, so its replay watermark is too.
func (d *Service) bindRuntimeLocked(stored, runtime string) {
	if old := d.runtimes[stored]; old != "" && old != runtime {
		delete(d.byRuntime, old)
		delete(d.lastSeq, old)
	}
	d.runtimes[stored] = runtime
	d.byRuntime[runtime] = stored
}

func (d *Service) unbindRuntime(stored string) {
	d.rtMu.Lock()
	if rt := d.runtimes[stored]; rt != "" {
		delete(d.byRuntime, rt)
		delete(d.lastSeq, rt)
	}
	delete(d.runtimes, stored)
	d.rtMu.Unlock()
}

// noteProfile remembers which profile home a stored session lives in, so
// resumes (command exec, model picker, reconnect) address the right home.
func (d *Service) noteProfile(stored, profile string) {
	if stored == "" || profile == "" {
		return
	}
	d.rtMu.Lock()
	d.profileOf[stored] = profile
	d.rtMu.Unlock()
}

// resumeErr marks a failure of the resume leg (vs the operation itself), so
// callers can tell "the session could not be bound" from "serve answered".
type resumeErr struct {
	op  error // the operation's own 4001, when the resume was a retry
	err error
}

func (e *resumeErr) Error() string {
	if e.op != nil {
		return fmt.Sprintf("%v; resume: %v", e.op, e.err)
	}
	return "resume: " + e.err.Error()
}
func (e *resumeErr) Unwrap() error { return e.err }

// runtimeFor returns the live runtime id for a stored session, resuming it
// (which also attaches this connection as a viewer) when unknown or forced.
// Concurrent callers for one chat are serialised so a burst (open + send +
// model picker) resumes once. A fresh binding runs the route guard before
// anything is submitted on it.
func (d *Service) runtimeFor(ctx context.Context, stored string, force bool) (string, error) {
	d.rtMu.Lock()
	rt := d.runtimes[stored]
	d.rtMu.Unlock()
	if rt != "" && !force {
		return rt, nil
	}
	lk := d.bindLock(stored)
	lk.Lock()
	defer lk.Unlock()
	d.rtMu.Lock()
	cur, prof := d.runtimes[stored], d.profileOf[stored]
	d.rtMu.Unlock()
	if cur != "" && (!force || cur != rt) {
		return cur, nil // someone bound (or re-bound) it while we waited
	}
	info, err := d.serve.ResumeWith(ctx, stored, prof)
	if err != nil {
		return "", err
	}
	rt = info.Runtime
	if rt == "" {
		rt = stored
	}
	d.rtMu.Lock()
	d.bindRuntimeLocked(stored, rt)
	d.rtMu.Unlock()
	li := hermes.ParseLiveInfo(info.Info)
	d.emitInfo(d.mergeInfo(stored, li))
	d.guardRoute(ctx, stored, prof, rt, li)
	return rt, nil
}

// withRuntime runs fn against the session's live runtime, re-resuming ONCE
// when serve answers 4001 (the runtime was reaped or serve restarted). The
// stored id is only a lookup key: resume mints runtime ids.
func (d *Service) withRuntime(ctx context.Context, stored string, turn *activeTurn, fn func(rt string) error) error {
	rt, err := d.runtimeFor(ctx, stored, false)
	if err != nil {
		return &resumeErr{err: err}
	}
	d.setTurnRuntime(turn, rt)
	err = fn(rt)
	var rpc *hermes.RPCError
	if errors.As(err, &rpc) && rpc.Code == 4001 {
		// Surface BOTH legs: 4001 is generic ("session not found"); the
		// resume error names the real cause (it hid a 23 MB frame once).
		rt2, rerr := d.runtimeFor(ctx, stored, true)
		if rerr != nil {
			return &resumeErr{op: err, err: rerr}
		}
		d.setTurnRuntime(turn, rt2)
		err = fn(rt2)
	}
	return err
}

func (d *Service) setTurnRuntime(turn *activeTurn, rt string) {
	if turn == nil {
		return
	}
	d.mu.Lock()
	turn.runtime = rt
	turn.lastEvent = time.Now()
	d.mu.Unlock()
}

// ---- sending --------------------------------------------------------------

type inputImage struct{ b64, filename string }

// splitInput takes the chat `input` (a string, or content parts: text +
// image_url data URLs) apart: text goes to prompt.submit, images are staged
// natively with image.attach_bytes.
func splitInput(input json.RawMessage) (string, []inputImage, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", nil, err
		}
		return s, nil, nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return "", nil, fmt.Errorf("unsupported message shape: %w", err)
	}
	var texts []string
	var imgs []inputImage
	for _, p := range parts {
		switch p.Type {
		case "text":
			if strings.TrimSpace(p.Text) != "" {
				texts = append(texts, p.Text)
			}
		case "image_url":
			u := p.ImageURL.URL
			if rest, ok := strings.CutPrefix(u, "data:"); ok {
				meta, data, found := strings.Cut(rest, ",")
				if !found || !strings.Contains(meta, ";base64") {
					return "", nil, errors.New("unsupported image encoding (want a base64 data URL)")
				}
				ext := "png"
				switch strings.TrimSpace(strings.SplitN(meta, ";", 2)[0]) {
				case "image/jpeg", "image/jpg":
					ext = "jpg"
				case "image/gif":
					ext = "gif"
				case "image/webp":
					ext = "webp"
				}
				imgs = append(imgs, inputImage{b64: data, filename: fmt.Sprintf("atlas-paste-%d.%s", len(imgs)+1, ext)})
			} else if u != "" {
				texts = append(texts, u)
			}
		}
	}
	return strings.Join(texts, "\n\n"), imgs, nil
}

// SendMessage starts one turn asynchronously on the session's serve runtime.
// input is the raw JSON chat input (string or content parts). Deltas and
// lifecycle arrive on the event channel; the call returns immediately.
func (d *Service) SendMessage(profile, sessionID string, input json.RawMessage) error {
	if !d.serve.Configured() {
		return errors.New("hermes-serve is not configured (no session token)")
	}
	if emptyInput(input) {
		return ErrEmpty
	}
	text, images, err := splitInput(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" && len(images) == 0 {
		return ErrEmpty
	}
	// Fail fast and visibly: a message accepted while serve is unreachable
	// would only die later, after the composer already cleared.
	if !d.serve.Connected() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		err := d.serve.Ensure(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnreachable, err)
		}
	}

	d.mu.Lock()
	if _, busy := d.turns[sessionID]; busy {
		d.mu.Unlock()
		return ErrBusy
	}
	turn := &activeTurn{profile: profile, done: make(chan struct{}), lastEvent: time.Now()}
	d.turns[sessionID] = turn
	d.mu.Unlock()

	d.noteProfile(sessionID, profile)
	d.clearFailure(sessionID)
	d.emit(TurnEvent{Kind: "started", SessionID: sessionID, Profile: profile})
	go d.startTurn(turn, sessionID, text, images)
	return nil
}

// startTurn binds the runtime, stages images and submits. Everything after
// the submit arrives as serve events.
func (d *Service) startTurn(turn *activeTurn, sessionID, text string, images []inputImage) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var status string
	err := d.withRuntime(ctx, sessionID, turn, func(rt string) error {
		for _, im := range images {
			if err := d.serve.AttachImageBytes(ctx, rt, im.b64, im.filename); err != nil {
				return fmt.Errorf("attach image: %w", err)
			}
		}
		st, err := d.serve.Submit(ctx, rt, text)
		status = st
		return err
	})
	if err != nil {
		d.failTurn(sessionID, turn, "not sent — "+err.Error(), "send_failed")
		return
	}
	switch status {
	case "", "streaming":
	case "queued", "steered", "redirected":
		// The session was mid-turn elsewhere (Discord, the desktop app): serve
		// folded this message into that run. Say so — otherwise it looks lost.
		log.Printf("atlas:turn submit session=%s status=%s (session was busy elsewhere)", sessionID, status)
		d.emit(TurnEvent{Kind: "note", SessionID: sessionID, Text: "the chat was busy in another client — your message was " + status + " into that turn"})
	default:
		log.Printf("atlas:turn submit session=%s status=%s", sessionID, status)
	}
}

// failTurn ends a turn as failed and remembers why (reload-proof).
func (d *Service) failTurn(stored string, turn *activeTurn, msg, code string) {
	d.noteFailure(stored, TurnFailure{Error: msg, Code: code, At: nowSec()})
	d.finishTurnCode(stored, turn, msg, code, false)
}

// StopTurn interrupts the session's in-flight turn. serve ends it with a
// message.complete whose status is "interrupted".
func (d *Service) StopTurn(sessionID string) error {
	d.mu.Lock()
	turn, ok := d.turns[sessionID]
	d.mu.Unlock()
	if !ok {
		return ErrNoTurn
	}
	deadline := time.Now().Add(3 * time.Second)
	var rt string
	for {
		d.mu.Lock()
		rt = turn.runtime
		d.mu.Unlock()
		if rt != "" {
			break
		}
		select {
		case <-turn.done:
			return nil
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return errors.New("the turn is still starting — try stopping again in a moment")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.serve.Interrupt(ctx, rt); err != nil {
		return err
	}
	d.mu.Lock()
	turn.stopped = true
	d.mu.Unlock()
	return nil
}

// DeleteSession removes a chat. serve owns live runtimes, so its session.delete
// is the authority; a live runtime is closed first (4023 otherwise). Falls back
// to the REST store when serve is not configured. Refused while a turn is live.
func (d *Service) DeleteSession(profile, sessionID string) error {
	d.mu.Lock()
	_, busy := d.turns[sessionID]
	d.mu.Unlock()
	if busy {
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if d.serve.Configured() {
		d.rtMu.Lock()
		rt := d.runtimes[sessionID]
		d.rtMu.Unlock()
		if rt != "" {
			_ = d.serve.CloseSession(ctx, rt)
			d.unbindRuntime(sessionID)
		}
		return d.serve.DeleteStored(ctx, sessionID, profile)
	}
	if !d.api.ConfiguredFor(profile) {
		return errors.New("no API key for profile " + profile)
	}
	return d.api.DeleteSession(ctx, profile, sessionID)
}

// SpawnDelete deletes one spawned run's record through the hub: debbie/pi
// task files are removed; a delegation's log dir is and it is dismissed
// (the ledger row in Hermes state.db stays untouched).
func (d *Service) SpawnDelete(kind, id, profile string) error {
	if !d.hub.Configured() {
		return errors.New("hub not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return d.hub.SpawnDelete(ctx, kind, id, profile)
}

// ---- events ---------------------------------------------------------------

// onServeEvent is called from the socket read loop for every event frame:
// it must not block or make RPCs. Frames for runtimes Atlas never bound
// (other clients' sessions) are dropped; seq dedupes replay overlap.
func (d *Service) onServeEvent(ev hermes.ServeEvent) {
	if ev.SessionID == "" {
		return
	}
	d.rtMu.Lock()
	stored := d.byRuntime[ev.SessionID]
	if stored == "" {
		d.rtMu.Unlock()
		return
	}
	if ev.Seq > 0 {
		if ev.Seq <= d.lastSeq[ev.SessionID] {
			d.rtMu.Unlock()
			return
		}
		d.lastSeq[ev.SessionID] = ev.Seq
	}
	d.rtMu.Unlock()
	d.applyServeEvent(stored, ev)
}

func (d *Service) applyServeEvent(stored string, ev hermes.ServeEvent) {
	d.mu.Lock()
	turn := d.turns[stored]
	if turn != nil {
		turn.lastEvent = time.Now()
	}
	d.mu.Unlock()
	switch ev.Type {
	case "session.info":
		d.applySessionInfo(stored, ev)
		return
	case "session.usage":
		// mid-turn usage tick (1s cadence while counters move): live tok/s
		// and context % for the status bar.
		var p struct {
			Usage *hermes.LiveUsage `json:"usage"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil && p.Usage != nil {
			si := d.mergeUsage(stored, p.Usage)
			d.emitInfo(si)
		}
		return
	}
	if turn == nil {
		return // not a turn Atlas started (or already finished)
	}
	switch ev.Type {
	case "message.delta":
		var p struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil || p.Text == "" {
			return
		}
		d.mu.Lock()
		turn.deltas++
		turn.chars += len(p.Text)
		n, chars := turn.deltas, turn.chars
		d.mu.Unlock()
		if n == 1 || n%50 == 0 {
			log.Printf("atlas:turn delta session=%s n=%d chars=%d", stored, n, chars)
		}
		d.noteSegment(turn, TurnSegment{Type: "text", Text: p.Text})
		d.emit(TurnEvent{Kind: "delta", SessionID: stored, Text: p.Text})
	case "reasoning.delta":
		var p struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil || p.Text == "" {
			return
		}
		d.noteSegment(turn, TurnSegment{Type: "reasoning", Text: p.Text})
		d.emit(TurnEvent{Kind: "reasoning", SessionID: stored, Text: p.Text})
	case "tool.start":
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil || p.Name == "" {
			return
		}
		d.noteSegment(turn, TurnSegment{Type: "tool", Name: p.Name, State: "running"})
		d.emit(TurnEvent{Kind: "tool", SessionID: stored, Tool: p.Name, ToolState: "running"})
	case "tool.complete":
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil || p.Name == "" {
			return
		}
		d.completeSegment(turn, p.Name)
		d.emit(TurnEvent{Kind: "tool", SessionID: stored, Tool: p.Name, ToolState: "done"})
	case "message.complete":
		var p struct {
			Status        string            `json:"status"`
			Error         string            `json:"error"`
			FailureReason string            `json:"failure_reason"`
			Usage         *hermes.LiveUsage `json:"usage"`
			Surface       *struct {
				Code     string `json:"code"`
				Layer    string `json:"layer"`
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"error_surface"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Usage != nil {
			d.emitInfo(d.mergeUsage(stored, p.Usage))
		}
		switch p.Status {
		case "interrupted":
			d.finishTurn(stored, turn, "", true)
		case "error":
			msg := p.Error
			if msg == "" {
				msg = p.FailureReason
			}
			if msg == "" {
				msg = "the turn ended with an error"
			}
			code := p.FailureReason
			if p.Surface != nil && p.Surface.Code != "" {
				code = p.Surface.Code
			}
			if p.Surface != nil && p.Surface.Provider != "" && p.Surface.Model != "" {
				msg = fmt.Sprintf("%s (route: %s · %s)", msg, p.Surface.Model, p.Surface.Provider)
			}
			d.failTurn(stored, turn, msg, code)
		default:
			d.clearFailure(stored)
			d.finishTurn(stored, turn, "", false)
		}
	case "error":
		// A session-level failure (agent init, model switch…). Show it on the
		// live turn; the turn itself ends with its own message.complete.
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Message != "" {
			d.emit(TurnEvent{Kind: "error", SessionID: stored, Error: p.Message})
		}
	}
}

// applySessionInfo folds serve's session.info (model, provider, reasoning,
// fast, usage — avg_tps over the last 10 API calls, the same numbers the CLI
// status bar and the official desktop show) into the info cache and relays
// it. Emitted at every settle/switch, so it also seeds the NEXT turn's tok/s.
func (d *Service) applySessionInfo(stored string, ev hermes.ServeEvent) {
	li := hermes.ParseLiveInfo(ev.Payload)
	if li == nil {
		return
	}
	si := d.mergeInfo(stored, li)
	d.emitInfo(si)
	// legacy "stats" frame for clients built before the info event
	if si != nil && si.TPS > 0 {
		d.emit(TurnEvent{Kind: "stats", SessionID: stored, TPS: si.TPS, Latency: si.Latency})
	}
}

// finishTurn ends a turn exactly once: drops it from the live map, emits an
// error (when given) then done. interrupted marks a user stop.
func (d *Service) finishTurn(stored string, turn *activeTurn, errMsg string, interrupted bool) {
	d.finishTurnCode(stored, turn, errMsg, "", interrupted)
}

func (d *Service) finishTurnCode(stored string, turn *activeTurn, errMsg, code string, interrupted bool) {
	d.mu.Lock()
	if d.turns[stored] != turn {
		d.mu.Unlock()
		return
	}
	delete(d.turns, stored)
	if interrupted {
		turn.stopped = true
	}
	stopped, deltas, chars := turn.stopped, turn.deltas, turn.chars
	d.mu.Unlock()
	close(turn.done)
	log.Printf("atlas:turn done session=%s deltas=%d chars=%d err=%q stopped=%v", stored, deltas, chars, errMsg, stopped)
	if errMsg != "" {
		d.emit(TurnEvent{Kind: "error", SessionID: stored, Error: errMsg, Code: code})
	}
	d.emit(TurnEvent{Kind: "done", SessionID: stored, OK: errMsg == "", Stopped: stopped, Error: errMsg, Code: code})
}

// ---- chat management (hide = archive in place; new = mint) -----------------

// HideSession sets/clears a chat's hidden flag. The live runtime tier goes
// first (serve's preferred order — covers drafts), the stored id second
// (profile-db tier; what a never-bound chat needs).
func (d *Service) HideSession(profile, sessionID string, hidden bool) error {
	if !d.serve.Configured() {
		return errors.New("hermes-serve is not configured (no session token)")
	}
	if sessionID == "" {
		return ErrEmpty
	}
	if profile == "" {
		profile = "default"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d.rtMu.Lock()
	rt := d.runtimes[sessionID]
	d.rtMu.Unlock()
	var firstErr error
	if rt != "" {
		if err := d.serve.SetHidden(ctx, rt, profile, hidden); err == nil {
			return nil
		} else {
			firstErr = err
		}
	}
	if err := d.serve.SetHidden(ctx, sessionID, profile, hidden); err == nil {
		return nil
	} else if firstErr == nil {
		firstErr = err
	}
	return fmt.Errorf("set_hidden: %w", firstErr)
}

// NewChat mints a fresh chat in a profile and binds its live runtime, so the
// first send goes straight to it. With channelID set it also seeds the
// channel's guidelines as a hidden model-facing context row (the Discord
// forum-topic equivalent — the UI never paints it) and records the
// assignment. Returns the stored id plus a non-fatal warning for the UI.
func (d *Service) NewChat(profile, channelID string) (string, string, error) {
	if !d.serve.Configured() {
		return "", "", errors.New("hermes-serve is not configured (no session token)")
	}
	if profile == "" {
		profile = "default"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var seed []map[string]any
	if channelID != "" {
		store, err := d.hub.FetchChannels(ctx, profile)
		if err != nil {
			return "", "", fmt.Errorf("channel lookup: %w", err)
		}
		chanName, template, catName := "", "", ""
		for _, c := range hubList(store["channels"]) {
			if hubStr(c["id"]) == channelID {
				chanName = hubStr(c["name"])
				template = strings.TrimSpace(hubStr(c["template"]))
				catID := hubStr(c["category_id"])
				for _, cc := range hubList(store["categories"]) {
					if hubStr(cc["id"]) == catID {
						catName = hubStr(cc["name"])
						break
					}
				}
				break
			}
		}
		if chanName == "" {
			return "", "", fmt.Errorf("channel %s not found", channelID)
		}
		if template != "" {
			where := chanName
			if catName != "" {
				where = catName + " / " + chanName
			}
			content := "[Atlas channel context — auto-injected for every chat in this channel]\nChannel: " + where + "\n\n" + template
			seed = []map[string]any{{"role": "system", "content": content, "display_kind": "hidden"}}
		}
	}
	stored, runtime, err := d.serve.CreateSession(ctx, profile, seed)
	if err != nil {
		return "", "", fmt.Errorf("session.create: %w", err)
	}
	d.noteProfile(stored, profile)
	if runtime != "" {
		d.rtMu.Lock()
		d.bindRuntimeLocked(stored, runtime)
		d.rtMu.Unlock()
	}
	warn := ""
	if channelID != "" {
		if _, err := d.hub.ChannelOp(ctx, "assign", map[string]any{
			"profile": profile, "session": stored, "channel_id": channelID,
		}); err != nil {
			warn = "channel assignment failed: " + err.Error()
		}
	}
	return stored, warn, nil
}

// SetModel switches a chat's model through the confirm-capable config.set
// contract. serve keys sessions by RUNTIME id, so the daemon resolves its
// bound runtime first (the stored id is the fallback).
func (d *Service) SetModel(sessionID, value string, confirmed bool) (map[string]any, error) {
	if !d.serve.Configured() {
		return nil, errors.New("hermes-serve is not configured (no session token)")
	}
	if sessionID == "" || strings.TrimSpace(value) == "" {
		return nil, ErrEmpty
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// Through withRuntime: an unbound chat (or a runtime serve reaped since)
	// is resumed first — config.set on a stored id is a guaranteed 4001.
	var out map[string]any
	err := d.withRuntime(ctx, sessionID, nil, func(rt string) (e error) {
		out, e = d.serve.ConfigSetModel(ctx, rt, value, confirmed)
		return e
	})
	if err != nil {
		return nil, err
	}
	if cr, _ := out["confirm_required"].(bool); !cr {
		// an explicit pick supersedes any pending drift note
		d.emitInfo(d.setInfoNote(sessionID, func(si *SessionInfo) { si.Drift = "" }))
	}
	return out, nil
}

// Channels reads the native shape store for one profile (categories,
// channels, templates, assignments).
func (d *Service) Channels(profile string) (map[string]any, error) {
	if d.hub == nil || !d.hub.Configured() {
		return nil, errors.New("atlas-hub is not configured")
	}
	if profile == "" {
		profile = "default"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.hub.FetchChannels(ctx, profile)
}

// ChannelOp relays one store mutation to the hub.
func (d *Service) ChannelOp(action string, body map[string]any) (map[string]any, error) {
	if d.hub == nil || !d.hub.Configured() {
		return nil, errors.New("atlas-hub is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.hub.ChannelOp(ctx, action, body)
}

func hubList(v any) []map[string]any {
	lst, _ := v.([]any)
	out := make([]map[string]any, 0, len(lst))
	for _, it := range lst {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func hubStr(v any) string {
	s, _ := v.(string)
	return s
}

// ---- reconnect + stuck-turn recovery --------------------------------------

// onServeReconnect runs after the serve connection was re-established: every
// live turn re-attaches (resume) and catches up from the replay ring.
func (d *Service) onServeReconnect() {
	for sid, turn := range d.liveTurns(0) {
		d.reconcileTurn(sid, turn)
	}
	d.adoptRunning()
}

func (d *Service) turnWatchdog() {
	t := time.NewTicker(turnWatchEvery)
	defer t.Stop()
	for range t.C {
		for sid, turn := range d.liveTurns(turnQuiet) {
			d.reconcileTurn(sid, turn)
		}
	}
}

// liveTurns snapshots turns that have been quiet for at least quiet.
func (d *Service) liveTurns(quiet time.Duration) map[string]*activeTurn {
	out := map[string]*activeTurn{}
	now := time.Now()
	d.mu.Lock()
	for sid, turn := range d.turns {
		if turn.runtime != "" && now.Sub(turn.lastEvent) >= quiet {
			out[sid] = turn
		}
	}
	d.mu.Unlock()
	return out
}

// reconcileTurn asks serve what really happened to a turn we stopped hearing
// from: re-attach, replay missed events (they include message.complete when
// the turn finished while we were away), and only if the session is no longer
// running and the replay did not end the turn, end it ourselves.
func (d *Service) reconcileTurn(stored string, turn *activeTurn) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d.rtMu.Lock()
	rt, prof := d.runtimes[stored], d.profileOf[stored]
	since := d.lastSeq[rt]
	d.rtMu.Unlock()
	info, err := d.serve.ResumeWith(ctx, stored, prof)
	if err != nil {
		var rpc *hermes.RPCError
		if errors.As(err, &rpc) && rpc.Code == 4001 {
			d.unbindRuntime(stored) // runtime is gone (serve restarted / reaped)
			d.finishTurn(stored, turn, "", false)
			return
		}
		log.Printf("atlas:turn reconcile session=%s: resume: %v (will retry)", stored, err)
		return
	}
	if info.Runtime != "" && info.Runtime != rt {
		// New runtime id: the old one (and its turn) is gone.
		d.rtMu.Lock()
		d.bindRuntimeLocked(stored, info.Runtime)
		d.rtMu.Unlock()
		d.finishTurn(stored, turn, "", false)
		return
	}
	if rt != "" {
		if r, err := d.serve.EventsSince(ctx, rt, since); err == nil {
			for _, ev := range r.Events {
				if ev.SessionID == "" {
					ev.SessionID = rt
				}
				d.onServeEvent(ev)
			}
			if r.Truncated {
				log.Printf("atlas:turn reconcile session=%s: replay truncated (live view may have a gap; transcript refetch on done)", stored)
			}
		}
	}
	d.mu.Lock()
	age := time.Since(turn.lastEvent)
	started := d.turns[stored] == turn
	if started && info.Running {
		turn.lastEvent = time.Now() // still going: look again in turnQuiet
	}
	d.mu.Unlock()
	if started && !info.Running && age >= turnGrace {
		d.finishTurn(stored, turn, "", false)
	}
}
