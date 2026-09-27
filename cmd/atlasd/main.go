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
	"syscall"
	"time"

	"atlas/internal/daemon"
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

	api := &api{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", api.status)
	mux.HandleFunc("GET /api/initial", api.initial)
	mux.HandleFunc("GET /api/tree", api.tree)
	mux.HandleFunc("GET /api/sessions", api.sessions)
	mux.HandleFunc("GET /api/messages", api.messages)
	mux.HandleFunc("POST /api/send", api.send)
	mux.HandleFunc("POST /api/stop", api.stop)
	mux.HandleFunc("POST /api/attach", api.attach)
	mux.HandleFunc("GET /media", api.media)
	mux.HandleFunc("GET /api/events", api.events)

	if web := resolveWebDir(*webDir); web != "" {
		log.Printf("atlasd: serving UI from %s", web)
		mux.Handle("GET /", http.FileServer(http.Dir(web)))
	} else {
		log.Printf("atlasd: no web UI found; serving API only")
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort(*addr, strconv.Itoa(*port)),
		Handler:           withRequestLog(withLoopbackCORS(mux)),
		ReadHeaderTimeout: 10 * time.Second,
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

type api struct{ svc *daemon.Service }

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
	msgs, err := a.svc.GetMessages(profile, session, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, msgs)
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

// withLoopbackCORS lets loopback-only origins (the vite dev server) call
// the API directly during development. Desktop/browser shells load the UI
// from this same origin, so they need no CORS at all.
func withLoopbackCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && loopbackOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
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
