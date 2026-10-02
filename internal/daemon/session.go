package daemon

// session.go — per-session live state the UI shows and the guards that keep a
// chat sendable:
//
//   - info cache: model / provider / reasoning / fast / usage (context %, the
//     rolling tok/s serve computes — the same numbers the official desktop's
//     status bar reads), fed by resume replies, session.info, session.usage
//     and message.complete frames.
//   - route guard: Hermes persists two runtime routes per session (top-level
//     keys written by desktop/TUI/Atlas switches; the nested gateway_runtime
//     written by Discord/Telegram turns) and its resume prefers the nested one.
//     A chat that last ran in Discord on provider X and was later switched in
//     Atlas resumes as (Atlas model, provider X) — every turn then 404s and
//     ends as "Your request was not processed". The guard detects exactly that
//     pairing right after a runtime is bound and re-pins the route through
//     serve's own confirm-capable switch (config.set model … --session). No
//     Hermes code, no state.db writes from Atlas.
//   - link health: whether api / hub / serve are actually reachable (not just
//     configured), probed in the background for the status bar.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"atlas/internal/hermes"
)

// ErrUnreachable marks a send refused because hermes-serve cannot be reached
// right now (the message never left this machine).
var ErrUnreachable = errors.New("hermes-serve is unreachable")

// SessionInfo is the compact live snapshot the UI renders (status bar chips,
// settings panel). Zero values mean "unknown" — never fabricated.
type SessionInfo struct {
	Session    string  `json:"session"`
	Profile    string  `json:"profile,omitempty"`
	Live       bool    `json:"live"` // from a bound runtime (else: the stored route)
	Model      string  `json:"model,omitempty"`
	Provider   string  `json:"provider,omitempty"`
	Reasoning  string  `json:"reasoning,omitempty"`
	Tier       string  `json:"service_tier,omitempty"`
	Fast       bool    `json:"fast,omitempty"`
	CtxUsed    int64   `json:"context_used,omitempty"`
	CtxMax     int64   `json:"context_max,omitempty"`
	CtxPct     float64 `json:"context_percent,omitempty"`
	CtxEst     bool    `json:"context_estimated,omitempty"`
	TPS        float64 `json:"tps,omitempty"`
	Latency    float64 `json:"latency_s,omitempty"`
	CacheHit   float64 `json:"cache_hit_pct,omitempty"`
	Calls      int64   `json:"calls,omitempty"`
	Drift      string  `json:"drift,omitempty"`  // stored route would resume on the wrong provider
	Healed     string  `json:"healed,omitempty"` // the guard re-pinned the route (human text)
	UpdatedAt  float64 `json:"updated_at,omitempty"`
	Contract   int     `json:"desktop_contract,omitempty"`
	BindError  string  `json:"bind_error,omitempty"`
	RuntimeBnd bool    `json:"bound,omitempty"`
}

// LinkHealth is one upstream's reachability.
type LinkHealth struct {
	Configured bool    `json:"configured"`
	Up         bool    `json:"up"`
	Error      string  `json:"error,omitempty"`
	CheckedAt  float64 `json:"checked_at,omitempty"`
}

// sessionState holds the per-session caches (guarded by Service.infoMu).
type sessionState struct {
	info   map[string]*SessionInfo // stored id -> snapshot
	healed map[string]bool         // runtime id -> guard already ran
	bindMu map[string]*sync.Mutex  // stored id -> serialises resume+guard
}

func newSessionState() sessionState {
	return sessionState{
		info:   map[string]*SessionInfo{},
		healed: map[string]bool{},
		bindMu: map[string]*sync.Mutex{},
	}
}

func (d *Service) bindLock(stored string) *sync.Mutex {
	d.infoMu.Lock()
	defer d.infoMu.Unlock()
	m := d.sess.bindMu[stored]
	if m == nil {
		m = &sync.Mutex{}
		d.sess.bindMu[stored] = m
	}
	return m
}

// mergeInfo folds a serve session.info payload into the cache and returns a
// copy for broadcasting (nil when nothing parsed).
func (d *Service) mergeInfo(stored string, li *hermes.LiveInfo) *SessionInfo {
	if li == nil {
		return nil
	}
	d.rtMu.Lock()
	prof := d.profileOf[stored]
	d.rtMu.Unlock()
	d.infoMu.Lock()
	defer d.infoMu.Unlock()
	si := d.sess.info[stored]
	if si == nil {
		si = &SessionInfo{Session: stored}
		d.sess.info[stored] = si
	}
	si.Live, si.RuntimeBnd = true, true
	if prof != "" {
		si.Profile = prof
	}
	if li.Model != "" {
		si.Model = li.Model
	}
	if li.Provider != "" {
		si.Provider = li.Provider
	}
	si.Reasoning = li.ReasoningEffort
	si.Tier = li.ServiceTier
	si.Fast = li.Fast
	if li.Contract > 0 {
		si.Contract = li.Contract
	}
	if li.Usage != nil {
		applyUsage(si, li.Usage)
	}
	si.UpdatedAt = nowSec()
	cp := *si
	return &cp
}

// mergeUsage folds a usage snapshot (session.usage / message.complete).
func (d *Service) mergeUsage(stored string, u *hermes.LiveUsage) *SessionInfo {
	if u == nil {
		return nil
	}
	d.infoMu.Lock()
	defer d.infoMu.Unlock()
	si := d.sess.info[stored]
	if si == nil {
		si = &SessionInfo{Session: stored, Live: true}
		d.sess.info[stored] = si
	}
	applyUsage(si, u)
	si.UpdatedAt = nowSec()
	cp := *si
	return &cp
}

func applyUsage(si *SessionInfo, u *hermes.LiveUsage) {
	if u.Model != "" {
		si.Model = u.Model
	}
	if u.ContextMax > 0 {
		si.CtxUsed, si.CtxMax, si.CtxPct, si.CtxEst = u.ContextUsed, u.ContextMax, u.ContextPercent, u.ContextEst
	}
	// Rolling throughput / latency are omitted by serve until the first API
	// call completes: keep the last real reading rather than zeroing it.
	if u.AvgTPS > 0 {
		si.TPS = u.AvgTPS
	}
	if u.AvgLatency > 0 {
		si.Latency = u.AvgLatency
	}
	if u.CacheHitPct > 0 {
		si.CacheHit = u.CacheHitPct
	}
	if u.Calls > 0 {
		si.Calls = u.Calls
	}
}

func (d *Service) setInfoNote(stored string, f func(si *SessionInfo)) *SessionInfo {
	d.infoMu.Lock()
	defer d.infoMu.Unlock()
	si := d.sess.info[stored]
	if si == nil {
		si = &SessionInfo{Session: stored}
		d.sess.info[stored] = si
	}
	f(si)
	si.UpdatedAt = nowSec()
	cp := *si
	return &cp
}

func (d *Service) emitInfo(si *SessionInfo) {
	if si == nil {
		return
	}
	d.emit(TurnEvent{Kind: "info", SessionID: si.Session, Profile: si.Profile, Info: si, TPS: si.TPS})
}

// Info returns the session snapshot. Unbound sessions are described from the
// stored route (what a resume WOULD pick), with Drift set when that pairing
// is the broken one the guard repairs on bind.
func (d *Service) Info(profile, stored string) SessionInfo {
	d.infoMu.Lock()
	si := d.sess.info[stored]
	var cp SessionInfo
	if si != nil {
		cp = *si
	}
	d.infoMu.Unlock()
	if si != nil && si.Live {
		return cp
	}
	out := SessionInfo{Session: stored, Profile: profile}
	if si != nil {
		out = cp
		out.Profile = profile
	}
	if d.hub == nil || !d.hub.Configured() {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rt, err := d.hub.FetchRoute(ctx, profile, stored)
	if err != nil {
		return out
	}
	out.Model = rt.Model
	if out.Reasoning == "" {
		out.Reasoning = rt.Reason
	}
	out.Provider = rt.Nested.Provider
	if out.Provider == "" {
		out.Provider = rt.Top.Provider
	}
	if want, ok := routeDrift(rt, rt.Model, out.Provider); ok {
		out.Drift = fmt.Sprintf("resumes on %s; last picked %s · %s", out.Provider, want.Model, want.Provider)
	}
	return out
}

// routeDrift reports whether (liveModel, liveProvider) is the broken pairing:
// the model column agrees with the top-level route (the last writer was a
// desktop/TUI/Atlas switch), the nested gateway route names a different
// provider, and the runtime came up on that nested provider. Returns the
// route the user actually picked. A session whose last writer was a gateway
// turn (model column = the gateway's model) never matches: its nested route
// IS the truth there.
func routeDrift(rt *hermes.SessionRoute, liveModel, liveProvider string) (hermes.RouteSide, bool) {
	top, nested := rt.Top, rt.Nested
	if top.Provider == "" || nested.Provider == "" || top.Model == "" {
		return top, false
	}
	if strings.EqualFold(top.Provider, nested.Provider) {
		return top, false
	}
	if rt.Model != "" && rt.Model != top.Model {
		return top, false
	}
	if liveModel != "" && liveModel != top.Model {
		return top, false
	}
	if !strings.EqualFold(liveProvider, nested.Provider) {
		return top, false
	}
	return top, true
}

// guardRoute runs once per freshly bound runtime: compare the live identity
// to the stored routes and re-pin when it came up on the stale gateway
// provider. Failures are logged and surfaced, never fatal to the send.
func (d *Service) guardRoute(ctx context.Context, stored, prof, runtime string, li *hermes.LiveInfo) {
	if runtime == "" || li == nil || d.hub == nil || !d.hub.Configured() {
		return
	}
	d.infoMu.Lock()
	done := d.sess.healed[runtime]
	d.sess.healed[runtime] = true
	d.infoMu.Unlock()
	if done {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	route, err := d.hub.FetchRoute(rctx, prof, stored)
	cancel()
	if err != nil {
		return // fresh chat (no row yet) or hub down: nothing to compare
	}
	want, drift := routeDrift(route, li.Model, li.Provider)
	if !drift {
		return
	}
	value := want.Model + " --provider " + want.Provider + " --session"
	log.Printf("atlas:route heal session=%s runtime=%s live=%s/%s -> %s/%s",
		stored, runtime, li.Model, li.Provider, want.Model, want.Provider)
	// confirm=true: the current route cannot serve this model at all, so
	// there is no warm cache for the large-context guard to protect.
	res, err := d.serve.ConfigSetModel(ctx, runtime, value, true)
	if err != nil {
		msg := fmt.Sprintf("this chat resumed on %s (a stale route from its Discord history) and the automatic repair failed: %v — pick the model again with M", li.Provider, err)
		d.emitInfo(d.setInfoNote(stored, func(si *SessionInfo) { si.Drift = msg }))
		d.emit(TurnEvent{Kind: "note", SessionID: stored, Profile: prof, Text: msg})
		return
	}
	if cr, _ := res["confirm_required"].(bool); cr {
		return // should not happen with confirm=true; leave the route alone
	}
	note := fmt.Sprintf("route repaired: %s now runs on %s (the chat had resumed on %s from its Discord history)",
		want.Model, want.Provider, li.Provider)
	si := d.setInfoNote(stored, func(si *SessionInfo) {
		si.Model, si.Provider, si.Drift, si.Healed = want.Model, want.Provider, "", note
	})
	d.emitInfo(si)
	d.emit(TurnEvent{Kind: "note", SessionID: stored, Profile: prof, Text: note})
}

// Bind resolves (resuming when needed) the live runtime for a chat and runs
// the route guard — the warm-up the UI triggers when the composer is focused
// so the first send does not pay for the agent build.
func (d *Service) Bind(profile, stored string) (SessionInfo, error) {
	if !d.serve.Configured() {
		return SessionInfo{}, errors.New("hermes-serve is not configured (no session token)")
	}
	d.noteProfile(stored, profile)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := d.runtimeFor(ctx, stored, false); err != nil {
		si := d.setInfoNote(stored, func(si *SessionInfo) { si.BindError = err.Error() })
		return *si, err
	}
	return d.Info(profile, stored), nil
}

// SetConfig runs one session-scoped setting (reasoning, fast) on the chat's
// live runtime, re-resuming once on a stale runtime id.
func (d *Service) SetConfig(profile, stored, key, value string) (map[string]any, error) {
	switch key {
	case "reasoning", "fast":
	default:
		return nil, fmt.Errorf("unsupported setting %q", key)
	}
	if !d.serve.Configured() {
		return nil, errors.New("hermes-serve is not configured (no session token)")
	}
	d.noteProfile(stored, profile)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out map[string]any
	err := d.withRuntime(ctx, stored, nil, func(rt string) (e error) {
		out, e = d.serve.ConfigSet(ctx, rt, key, value)
		return e
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- link health ------------------------------------------------------------

func (d *Service) probeLoop() {
	d.probeOnce()
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for range t.C {
		d.probeOnce()
	}
}

func (d *Service) probeOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	now := nowSec()
	api := LinkHealth{Configured: d.api.Configured(), CheckedAt: now}
	if err := d.api.Ping(ctx); err != nil {
		api.Error = err.Error()
	} else {
		api.Up = true
	}
	hub := LinkHealth{Configured: d.hub.Configured(), CheckedAt: now}
	if err := d.hub.Ping(ctx); err != nil {
		hub.Error = err.Error()
	} else {
		hub.Up = true
	}
	d.healthMu.Lock()
	d.health["api"], d.health["hub"] = api, hub
	d.healthMu.Unlock()
}

// Links reports api / hub / serve reachability.
func (d *Service) Links() map[string]LinkHealth {
	d.healthMu.Lock()
	out := make(map[string]LinkHealth, 3)
	for k, v := range d.health {
		out[k] = v
	}
	d.healthMu.Unlock()
	h := d.serve.Health()
	out["serve"] = LinkHealth{Configured: d.serve.Configured(), Up: h.Up, Error: h.Err, CheckedAt: nowSec()}
	return out
}

func nowSec() float64 { return float64(time.Now().UnixMilli()) / 1000 }
