// Package hermes — serve.go adds the hermes-serve command surface.
//
// The desktop backend (`hermes serve`, default http://127.0.0.1:9119)
// exposes a JSON-RPC 2.0 WebSocket at /api/ws authenticated with the
// dashboard session token. Atlas uses it for the slash-command engine —
// commands.catalog / complete.slash / slash.exec — the same surface the
// Hermes desktop app reads, with zero Hermes source changes. Output is
// plain text or, for rerouted commands, a command.dispatch directive.
//
// Configuration:
//
//	ATLAS_SERVE_URL    base URL (default http://127.0.0.1:9119)
//	ATLAS_SERVE_TOKEN  session token (falls back to
//	                   HERMES_DASHBOARD_SESSION_TOKEN in ~/.hermes/.env)
//
// The WebSocket client below is a deliberately small RFC 6455 subset
// (text frames + ping/pong/close, no extensions, masked client frames) so
// the daemon keeps its zero-dependency stdlib build.
package hermes

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"atlas/internal/config"
)

type Serve struct {
	BaseURL string
	Token   string

	mu   sync.Mutex
	conn *wsConn
}

// NewServeFromEnv builds the serve client from the environment (see
// package docs above).
func NewServeFromEnv() *Serve {
	base := strings.TrimSpace(os.Getenv("ATLAS_SERVE_URL"))
	if base == "" {
		base = "http://127.0.0.1:9119"
	}
	return &Serve{BaseURL: strings.TrimRight(base, "/"), Token: resolveServeToken()}
}

type CommandMeta struct {
	ArgumentMode string `json:"argument_mode,omitempty"`
	Desktop      string `json:"desktop,omitempty"`
}

type CommandCategory struct {
	Name  string     `json:"name"`
	Pairs [][]string `json:"pairs"` // [["/name","description"], ...]
}

type SkillEntry struct {
	Usage  int    `json:"usage"`
	Origin string `json:"origin"`
}

// Catalog mirrors commands.catalog's wire result.
type Catalog struct {
	Pairs      [][]string             `json:"pairs"`
	Sub        map[string][]string    `json:"sub"`
	Canon      map[string]string      `json:"canon"`
	Commands   map[string]CommandMeta `json:"commands"`
	Categories []CommandCategory      `json:"categories"`
	Skills     map[string]SkillEntry  `json:"skills"`
	SkillCount int                    `json:"skill_count"`
	Warning    string                 `json:"warning"`
}

// Completion is one complete.slash item.
type Completion struct {
	Text    string `json:"text"`
	Display string `json:"display,omitempty"`
	Meta    string `json:"meta,omitempty"`
	Kind    string `json:"kind,omitempty"` // command | skill
}

// ExecResult is slash.exec's result: plain worker/plugin text in Output,
// or — when the command was rerouted to command.dispatch — the directive
// fields with Type set (alias | exec | plugin | send | skill | prefill).
type ExecResult struct {
	Output  string `json:"output,omitempty"`
	Warning string `json:"warning,omitempty"`

	Type    string `json:"type,omitempty"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message,omitempty"`
	Notice  string `json:"notice,omitempty"`
	Display string `json:"display,omitempty"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status,omitempty"`
}

// RPCError is a JSON-RPC error response.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc %d: %s", e.Code, e.Message) }

func resolveServeToken() string {
	if t := strings.TrimSpace(os.Getenv("ATLAS_SERVE_TOKEN")); t != "" {
		return t
	}
	if t := readEnvMap(config.EnvFile())["ATLAS_SERVE_TOKEN"]; t != "" {
		return t
	}
	return readEnvMap(hermesEnvPath())["HERMES_DASHBOARD_SESSION_TOKEN"]
}

// Configured reports whether a session token was found.
func (s *Serve) Configured() bool { return s != nil && s.Token != "" }

// Catalog fetches the categorized slash-command metadata.
func (s *Serve) Catalog(ctx context.Context) (*Catalog, error) {
	var out Catalog
	if err := s.call(ctx, "commands.catalog", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CompleteSlash returns ranked slash/skill completions for a composer text.
func (s *Serve) CompleteSlash(ctx context.Context, text, sessionID string) ([]Completion, error) {
	params := map[string]any{"text": text}
	if sessionID != "" {
		params["session_id"] = sessionID
	}
	var out struct {
		Items []Completion `json:"items"`
	}
	if err := s.call(ctx, "complete.slash", params, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ExecSlash runs one slash command against the live session's worker.
func (s *Serve) ExecSlash(ctx context.Context, sessionID, command string) (*ExecResult, error) {
	var out ExecResult
	if err := s.call(ctx, "slash.exec", map[string]any{"session_id": sessionID, "command": command}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Resume revives a stored session as a live runtime and returns that
// runtime's session id. The command engine addresses sessions by live
// runtime id — the stored id is only the resume lookup key — so slash.exec
// retries must rebind to the returned id.
func (s *Serve) Resume(ctx context.Context, sessionID string) (string, error) {
	var out struct {
		SessionID string `json:"session_id"`
		Resumed   string `json:"resumed"`
	}
	// omit_messages mirrors the desktop's REST-hydration resume: register the
	// live runtime WITHOUT shipping the message blob over the socket. Without
	// it the serve sends the session's full history in the reply — tens of MB
	// for a long session, over our 4 MiB frame guard — and the refused frame
	// surfaced as a bogus "session not found" (4001) on every exec retry.
	if err := s.call(ctx, "session.resume", map[string]any{
		"session_id":    sessionID,
		"source":        "desktop",
		"omit_messages": true,
	}, &out); err != nil {
		return "", err
	}
	if out.SessionID != "" {
		return out.SessionID, nil
	}
	return out.Resumed, nil
}

// call runs one RPC on the shared connection. A connection that fails
// before the request was written is transparently redialed once; a failure
// after a successful write is surfaced as-is (retrying could double-run a
// side-effecting command).
func (s *Serve) call(ctx context.Context, method string, params any, out any) error {
	for attempt := 0; ; attempt++ {
		conn, err := s.getConn(ctx)
		if err != nil {
			return err
		}
		raw, err := conn.call(ctx, method, params)
		if err != nil {
			var notSent *notSentError
			if errors.As(err, &notSent) {
				s.dropIf(conn)
				if attempt == 0 {
					continue
				}
			}
			if conn.isClosed() {
				s.dropIf(conn)
			}
			return err
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(raw, out)
	}
}

func (s *Serve) getConn(ctx context.Context) (*wsConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil && !s.conn.isClosed() {
		return s.conn, nil
	}
	c, err := dialWS(ctx, s.BaseURL, s.Token)
	if err != nil {
		return nil, err
	}
	s.conn = c
	return c, nil
}

func (s *Serve) dropIf(c *wsConn) {
	s.mu.Lock()
	if s.conn == c {
		s.conn = nil
	}
	s.mu.Unlock()
	c.close()
}

// ---- RFC 6455 client (text frames + ping/pong/close only) ----------------

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsConn struct {
	nc net.Conn
	br *bufio.Reader

	wmu sync.Mutex // one frame writer at a time

	pmu     sync.Mutex
	pending map[int64]chan json.RawMessage
	nextID  int64

	closeMu sync.Mutex
	err     error
	closed  chan struct{}
}

func (c *wsConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *wsConn) close() { c.closeWith(errors.New("ws closed")) }

func (c *wsConn) closeWith(err error) {
	c.closeMu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.closeMu.Unlock()
	select {
	case <-c.closed:
	default:
		close(c.closed)
		_ = c.nc.Close()
	}
}

func (c *wsConn) closeErr() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.err != nil {
		return c.err
	}
	return errors.New("ws closed")
}

func (c *wsConn) register() (int64, chan json.RawMessage) {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	c.nextID++
	ch := make(chan json.RawMessage, 1)
	c.pending[c.nextID] = ch
	return c.nextID, ch
}

func (c *wsConn) unregister(id int64) {
	c.pmu.Lock()
	delete(c.pending, id)
	c.pmu.Unlock()
}

// notSentError marks a request that provably never made it onto the wire.
type notSentError struct{ err error }

func (e *notSentError) Error() string { return "ws write failed before send: " + e.err.Error() }
func (e *notSentError) Unwrap() error { return e.err }

func (c *wsConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id, ch := c.register()
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.unregister(id)
		return nil, err
	}
	if err := c.writeFrame(0x1, req); err != nil {
		c.unregister(id)
		return nil, &notSentError{err}
	}
	select {
	case raw := <-ch:
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.unregister(id)
		return nil, ctx.Err()
	case <-c.closed:
		c.unregister(id)
		return nil, c.closeErr()
	}
}

func (c *wsConn) readLoop() {
	var frag []byte
	var fragOp byte
	for {
		op, fin, payload, err := c.readFrame()
		if err != nil {
			c.closeWith(err)
			return
		}
		switch op {
		case 0x0: // continuation
			frag = append(frag, payload...)
			if fin {
				c.deliver(fragOp, frag)
				frag, fragOp = nil, 0
			}
		case 0x1, 0x2: // text / binary
			if fin {
				c.deliver(op, payload)
			} else {
				frag = append(frag[:0], payload...)
				fragOp = op
			}
		case 0x8: // close
			c.closeWith(errors.New("server closed the connection"))
			return
		case 0x9: // ping → pong
			_ = c.writeFrame(0xA, payload)
		case 0xA: // pong
		}
	}
}

// deliver routes one complete text message to its waiting RPC by id;
// messages without an id (server notifications) are ignored.
func (c *wsConn) deliver(op byte, payload []byte) {
	if op != 0x1 {
		return
	}
	var head struct {
		ID *int64 `json:"id"`
	}
	if err := json.Unmarshal(payload, &head); err != nil || head.ID == nil {
		return
	}
	c.pmu.Lock()
	ch := c.pending[*head.ID]
	delete(c.pending, *head.ID)
	c.pmu.Unlock()
	if ch != nil {
		ch <- payload
	}
}

func (c *wsConn) keepalive() {
	t := time.NewTicker(40 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			if err := c.writeFrame(0x9, nil); err != nil {
				c.closeWith(err)
				return
			}
		}
	}
}

func (c *wsConn) readFrame() (op byte, fin bool, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(c.br, hdr[:]); err != nil {
		return
	}
	fin = hdr[0]&0x80 != 0
	op = hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	ln := int64(hdr[1] & 0x7F)
	switch ln {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		ln = int64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		ln = int64(binary.BigEndian.Uint64(b[:]))
	}
	if ln < 0 || ln > 1<<22 {
		return 0, false, nil, fmt.Errorf("ws: refusing frame of %d bytes", ln)
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, ln)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return
}

func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	var hdr [14]byte
	hdr[0] = 0x80 | op
	n := 2
	switch {
	case len(payload) < 126:
		hdr[1] = 0x80 | byte(len(payload))
	case len(payload) < 1<<16:
		hdr[1] = 0x80 | 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(len(payload)))
		n = 4
	default:
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(len(payload)))
		n = 10
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	copy(hdr[n:n+4], mask[:])
	n += 4
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i&3]
	}
	_ = c.nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	bufs := net.Buffers{hdr[:n], masked}
	_, err := bufs.WriteTo(c.nc)
	_ = c.nc.SetWriteDeadline(time.Time{})
	return err
}

// dialWS performs the HTTP upgrade against /api/ws.
func dialWS(ctx context.Context, base, token string) (*wsConn, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("serve url: %w", err)
	}
	host := u.Host
	if host == "" {
		return nil, errors.New("serve url: no host")
	}
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	nc, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		nc.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	path := u.Path
	if !strings.HasSuffix(path, "/ws") {
		path = "/api/ws"
	}
	if token != "" {
		path += "?" + url.Values{"token": {token}}.Encode()
	}
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, key)

	_ = nc.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := nc.Write([]byte(req)); err != nil {
		nc.Close()
		return nil, err
	}
	br := bufio.NewReader(nc)
	line, err := br.ReadString('\n')
	if err != nil {
		nc.Close()
		return nil, err
	}
	if !strings.Contains(line, " 101") {
		nc.Close()
		return nil, fmt.Errorf("serve ws upgrade: %s", strings.TrimSpace(line))
	}
	want := wsAccept(key)
	got := ""
	for {
		hl, err := br.ReadString('\n')
		if err != nil {
			nc.Close()
			return nil, err
		}
		hl = strings.TrimSpace(hl)
		if hl == "" {
			break
		}
		if k, v, ok := strings.Cut(hl, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Sec-WebSocket-Accept") {
			got = strings.TrimSpace(v)
		}
	}
	if got != want {
		nc.Close()
		return nil, errors.New("serve ws upgrade: bad Sec-WebSocket-Accept")
	}
	_ = nc.SetDeadline(time.Time{})

	c := &wsConn{nc: nc, br: br, pending: map[int64]chan json.RawMessage{}, closed: make(chan struct{})}
	go c.readLoop()
	go c.keepalive()
	return c, nil
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}
