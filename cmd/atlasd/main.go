// atlasd — the Atlas daemon.
//
// One process that owns all communication with head services (Hermes API
// server + hub) and serves both the bridge API (HTTP + SSE) and the web UI.
// Every shell speaks this API: the Electron window, a browser tab, later
// the phone.
//
// Security posture: binds loopback only by default; no auth token yet when
// non-loopback binding is used (warned loudly). Keys never leave this
// process — the web UI holds none.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"atlas/internal/daemon"
	"atlas/internal/hermes"
)

func main() {
	addr := flag.String("addr", "127.0.0.1", "bind address (loopback by default)")
	port := flag.Int("port", 8644, "listen port")
	webDir := flag.String("web", "", "directory with the built web UI (default: desktop/frontend/dist)")
	openSession := flag.String("open", "", "session id to open on boot")
	dieParent := flag.Bool("die-with-parent", false, "exit when the parent process dies (Linux PDEATHSIG)")
	flag.Parse()

	if *dieParent {
		dieWithParent()
	}

	svc := daemon.New()
	if *openSession != "" {
		svc.SetInitialSession(*openSession)
	}
	st := svc.Status()
	log.Printf("atlasd: api=%s (configured=%v) hub=%s (configured=%v)", st.APIURL, st.API, st.HubURL, st.Hub)

	if *addr != "127.0.0.1" && *addr != "localhost" && *addr != "::1" {
		log.Printf("atlasd: WARNING: binding %s (non-loopback) without authentication", *addr)
	}

	// Restart is only honest when something will bring us back: systemd sets
	// INVOCATION_ID for every unit process. An atlasd spawned by an app shell
	// (--die-with-parent) has no supervisor — exiting would just kill it.
	api := &api{
		svc:        svc,
		supervised: !*dieParent && os.Getenv("INVOCATION_ID") != "",
		turns:      func() int { return len(svc.Status().Turns) },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", api.status)
	mux.HandleFunc("GET /api/initial", api.initial)
	mux.HandleFunc("GET /api/tree", api.tree)
	mux.HandleFunc("GET /api/spawned", api.spawned)
	mux.HandleFunc("GET /api/spawn-log", api.spawnLog)
	mux.HandleFunc("GET /api/sessions", api.sessions)
	mux.HandleFunc("GET /api/messages", api.messages)
	mux.HandleFunc("GET /api/turn", api.turn)
	mux.HandleFunc("DELETE /api/session", api.deleteSession)
	mux.HandleFunc("POST /api/send", api.send)
	mux.HandleFunc("POST /api/stop", api.stop)
	mux.HandleFunc("POST /api/restart", api.restartDaemon)
	mux.HandleFunc("POST /api/attach", api.attach)
	mux.HandleFunc("GET /api/commands", api.commands)
	mux.HandleFunc("GET /api/complete", api.complete)
	mux.HandleFunc("POST /api/exec", api.exec)
	mux.HandleFunc("GET /api/models", api.models)
	mux.HandleFunc("GET /media", api.media)
	mux.HandleFunc("GET /api/events", api.events)

	if web := resolveWebDir(*webDir); web != "" {
		log.Printf("atlasd: serving UI from %s", web)
		mux.Handle("GET /", webCacheHeaders(http.FileServer(http.Dir(web))))
	} else {
		log.Printf("atlasd: no web UI found; serving API only")
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort(*addr, strconv.Itoa(*port)),
		Handler:           withRequestLog(withLoopbackCORS(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Restart = exit non-zero after a beat (long enough for the 200 to reach
	// the client). Non-zero on purpose: head's atlasd unit is Restart=on-failure,
	// so a clean exit 0 would leave it down. No graceful Shutdown either —
	// ListenAndServe would return first and main would exit 0. Live turns are
	// refused up front, so all that drops here is SSE streams (which reconnect).
	api.restart = func() {
		time.Sleep(300 * time.Millisecond)
		log.Printf("atlasd: restart requested — exiting %d for the supervisor to relaunch", restartExit)
		os.Exit(restartExit)
	}

	// Clean shutdown on TERM/INT (kill from a shell, service managers).
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		log.Printf("atlasd: shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("atlasd: listening on http://%s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// restartExit is EX_TEMPFAIL: any non-zero code makes Restart=on-failure and
// Restart=always units relaunch the daemon.
const restartExit = 75

type api struct {
	svc        *daemon.Service
	supervised bool       // a service manager will relaunch us
	turns      func() int // live turns in this daemon (injectable for tests)
	restart    func()     // exit for relaunch; wired in main
}

// restartDaemon is the in-app `systemctl restart atlasd`. Guarded three ways:
// the custom header (a cross-origin page can't send it without a preflight,
// which only loopback dev origins pass), supervision (never exit an atlasd
// nothing will relaunch), and live turns (their in-daemon view would drop —
// the runs keep going on head — unless force).
func (a *api) restartDaemon(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Atlas-Action") != "restart" {
		writeError(w, http.StatusForbidden, errors.New("missing X-Atlas-Action: restart"))
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // empty body = not forced
	if !a.supervised {
		writeError(w, http.StatusConflict, errors.New(
			"this atlasd has no supervisor (started by hand or by an app shell) — restarting it would just kill it"))
		return
	}
	if n := a.turns(); n > 0 && !req.Force {
		writeError(w, http.StatusConflict, fmt.Errorf(
			"%d turn(s) live — their live view would drop (the runs continue on head); /restart force overrides", n))
		return
	}
	writeJSON(w, map[string]any{"ok": true, "restarting": true})
	go a.restart()
}

func (a *api) status(w http.ResponseWriter, r *http.Request)  { writeJSON(w, a.svc.Status()) }
func (a *api) initial(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.svc.InitialSession()) }

func (a *api) tree(w http.ResponseWriter, r *http.Request) {
	t, err := a.svc.GetTree()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, t)
}

func (a *api) spawned(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.GetSpawned()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, list)
}

func (a *api) spawnLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, id := q.Get("kind"), q.Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	task, _ := strconv.Atoi(q.Get("task"))
	lines, _ := strconv.Atoi(q.Get("lines"))
	text, err := a.svc.GetSpawnLog(kind, id, task, lines)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]string{"text": text})
}

func (a *api) sessions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.svc.GetSessions(limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, list)
}

func (a *api) messages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	profile, session := q.Get("profile"), q.Get("session")
	if profile == "" || session == "" {
		writeError(w, http.StatusBadRequest, errors.New("profile and session are required"))
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	// Tail-first by default: the UI reads the newest page up front, then
	// pages back with offset for older history. order=oldest keeps the
	// forward read for anything that wants it.
	msgs, err := a.svc.GetMessages(profile, session, hermes.MessageQuery{
		Limit:            limit,
		Offset:           offset,
		Latest:           q.Get("order") != "oldest",
		IncludeCompacted: q.Get("include_compacted") == "1",
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, msgs)
}

// turn reports the live-turn snapshot for a session (clients attaching
// mid-turn — app reopen, second window — hydrate from this).
func (a *api) turn(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		writeError(w, http.StatusBadRequest, errors.New("session is required"))
		return
	}
	if snap := a.svc.TurnState(session); snap != nil {
		writeJSON(w, map[string]any{
			"active":     true,
			"session_id": snap.SessionID,
			"profile":    snap.Profile,
			"segments":   snap.Segments,
		})
		return
	}
	writeJSON(w, map[string]any{"active": false})
}

// deleteSession removes one session from its profile's store.
func (a *api) deleteSession(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	profile, session := q.Get("profile"), q.Get("session")
	if profile == "" || session == "" {
		writeError(w, http.StatusBadRequest, errors.New("profile and session are required"))
		return
	}
	switch err := a.svc.DeleteSession(profile, session); {
	case err == nil:
		writeJSON(w, map[string]any{"ok": true, "deleted": session})
	case errors.Is(err, daemon.ErrBusy):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

// models proxies the model-picker payload for the open session (providers,
// models, current selection) from hermes-serve, bound to its live runtime.
func (a *api) models(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		writeError(w, http.StatusBadRequest, errors.New("session is required"))
		return
	}
	raw, err := a.svc.ModelOptions(session)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (a *api) send(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string          `json:"profile"`
		Session string          `json:"session"`
		Message json.RawMessage `json:"message"` // string or content-parts array
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("bad request body"))
		return
	}
	if req.Profile == "" || req.Session == "" {
		writeError(w, http.StatusBadRequest, errors.New("profile and session are required"))
		return
	}
	switch err := a.svc.SendMessage(req.Profile, req.Session, req.Message); {
	case err == nil:
		writeJSON(w, map[string]bool{"ok": true})
	case errors.Is(err, daemon.ErrEmpty):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, daemon.ErrBusy):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

func (a *api) stop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Session == "" {
		writeError(w, http.StatusBadRequest, errors.New("session is required"))
		return
	}
	switch err := a.svc.StopTurn(req.Session); {
	case err == nil:
		writeJSON(w, map[string]bool{"ok": true})
	case errors.Is(err, daemon.ErrNoTurn):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

// commands serves the slash-command catalog from hermes-serve (cached).
func (a *api) commands(w http.ResponseWriter, r *http.Request) {
	cat, err := a.svc.GetCatalog(r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, cat)
}

// complete proxies live slash/skill completions for the composer.
func (a *api) complete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := q.Get("text")
	if text == "" {
		writeJSON(w, map[string]any{"items": []any{}})
		return
	}
	items, err := a.svc.CompleteSlash(text, q.Get("session"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

// exec runs one slash command against the open session and returns its
// output (or a command.dispatch directive for rerouted commands).
func (a *api) exec(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session string `json:"session"`
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("bad request body"))
		return
	}
	res, err := a.svc.ExecSlash(req.Session, req.Command)
	switch {
	case err == nil:
		writeJSON(w, res)
	case errors.Is(err, daemon.ErrEmpty):
		writeError(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

// events is the live-turn SSE stream: every subscriber gets each TurnEvent
// as a named "turn" event. Disconnects unsubscribe cleanly.
func (a *api) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": atlasd event stream\n\n")
	flusher.Flush()

	ch, cancel := a.svc.Subscribe()
	defer cancel()

	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: turn\ndata: %s\n\n", b)
			flusher.Flush()
		case <-tick.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// resolveWebDir finds the built web UI: the flag wins; otherwise the usual
// repo-relative locations (run from the repo root or from cmd/atlasd).
func resolveWebDir(flagVal string) string {
	candidates := []string{flagVal, "desktop/frontend/dist", "../desktop/frontend/dist", "../../desktop/frontend/dist"}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

// webCacheHeaders makes UI updates land on the next load everywhere:
// vite-hashed assets are immutable, everything else (index.html, manifest,
// icons) revalidates. Without this, browsers heuristically cache
// index.html and a deployed update can sit invisible for hours (seen on
// iOS home-screen apps).
func webCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// Nix store paths carry epoch mtimes, so Go's file server
			// answers every If-Modified-Since revalidation with 304 — a
			// stale index.html + old asset graph gets pinned in client
			// caches FOREVER (Electron disk cache, iOS home-screen apps).
			// Strip validators and forbid storing: every load re-fetches,
			// which also self-heals caches written before this fix.
			w.Header().Set("Cache-Control", "no-store")
			r.Header.Del("If-Modified-Since")
			r.Header.Del("If-None-Match")
		}
		next.ServeHTTP(w, r)
	})
}

// withLoopbackCORS lets loopback-only origins (the vite dev server) call
// the API directly during development. Desktop/browser shells load the UI
// from this same origin, so they need no CORS at all.
func withLoopbackCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && loopbackOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Atlas-Action")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withRequestLog prints one line per request (method, URI, status) so the
// daemon is debuggable from its own stdout. It preserves http.Flusher so
// the SSE stream keeps working through the wrapper.
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lw := &logWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(lw, r)
		log.Printf("%s %s → %d", r.Method, r.URL.RequestURI(), lw.code)
	})
}

type logWriter struct {
	http.ResponseWriter
	code int
}

func (l *logWriter) WriteHeader(code int) {
	l.code = code
	l.ResponseWriter.WriteHeader(code)
}

func (l *logWriter) Flush() {
	if f, ok := l.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func loopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
