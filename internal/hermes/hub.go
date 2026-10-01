package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// HubNode is one node of the atlas-hub tree (guild / category / channel / post).
type HubNode struct {
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	SessionID    string    `json:"session_id,omitempty"`
	Profile      string    `json:"profile,omitempty"`
	ChatType     string    `json:"chat_type,omitempty"`
	LastActive   float64   `json:"last_active,omitempty"`
	MessageCount int       `json:"message_count,omitempty"`
	Pinned       bool      `json:"pinned,omitempty"`
	Hidden       bool      `json:"hidden,omitempty"`
	Source       string    `json:"source,omitempty"`
	ID           string    `json:"id,omitempty"`
	Native       bool      `json:"native,omitempty"`
	Template     string    `json:"template,omitempty"`
	ChannelID    string    `json:"channel_id,omitempty"`
	CategoryID   *string   `json:"category_id,omitempty"`
	Children     []HubNode `json:"children,omitempty"`
}

// HubTree is the /tree payload from atlas-hub.
type HubTree struct {
	GeneratedAt float64   `json:"generated_at"`
	Sections    []HubNode `json:"sections"`
	Errors      []string  `json:"errors"`
}

// Hub is a client for the local atlas-hub service.
//
// Configuration:
//
//	ATLAS_HUB_URL  base URL (default http://127.0.0.1:8643)
//	ATLAS_API_KEY  bearer key (same resolution as the API client)
type Hub struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

func NewHubFromEnv() *Hub {
	base := strings.TrimSpace(os.Getenv("ATLAS_HUB_URL"))
	if base == "" {
		base = "http://127.0.0.1:8643"
	}
	return &Hub{
		BaseURL: strings.TrimRight(base, "/"),
		Key:     resolveKey(),
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (h *Hub) Configured() bool { return h != nil && h.Key != "" }

// fetchRaw GETs a hub path and returns the body untouched. The daemon relays
// the tree and the spawned list to the UI through this: a typed round trip
// silently drops every field the Go struct has not been taught yet (that is
// how native categories and the hidden flag once vanished between hub and UI).
func (h *Hub) fetchRaw(ctx context.Context, path string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if h.Key != "" {
		req.Header.Set("Authorization", "Bearer "+h.Key)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", strings.SplitN(path, "?", 2)[0], resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("GET %s: hub returned invalid JSON", strings.SplitN(path, "?", 2)[0])
	}
	return body, nil
}

// FetchTreeRaw is the tree exactly as the hub sent it (hidden rows included).
func (h *Hub) FetchTreeRaw(ctx context.Context) (json.RawMessage, error) {
	return h.fetchRaw(ctx, "/tree?include_hidden=1")
}

// FetchSpawnedRaw is the spawned-work list exactly as the hub sent it.
func (h *Hub) FetchSpawnedRaw(ctx context.Context) (json.RawMessage, error) {
	return h.fetchRaw(ctx, "/spawned")
}

func (h *Hub) FetchTree(ctx context.Context) (*HubTree, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/tree?include_hidden=1", nil)
	if err != nil {
		return nil, err
	}
	if h.Key != "" {
		req.Header.Set("Authorization", "Bearer "+h.Key)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /tree: HTTP %d", resp.StatusCode)
	}
	var t HubTree
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// SpawnItem is one spawned-work row (subagent run or pi task) with its
// parent chat resolved to a session id where the hub could resolve it.
type SpawnItem struct {
	Kind      string  `json:"kind"` // subagent | pi
	ID        string  `json:"id"`
	Profile   string  `json:"profile,omitempty"`
	Parent    string  `json:"parent,omitempty"`
	ParentCh  string  `json:"parent_chat,omitempty"`
	State     string  `json:"state"` // running | done | failed | unknown
	Title     string  `json:"title"`
	Started   float64 `json:"started,omitempty"`
	Completed float64 `json:"completed,omitempty"`
	Tasks     int     `json:"tasks,omitempty"`
	HasLog    bool    `json:"has_log,omitempty"`
	RC        *int    `json:"rc,omitempty"`
}

type SpawnList struct {
	Items  []SpawnItem `json:"items"`
	Errors []string    `json:"errors,omitempty"`
}

// FetchSpawned lists recent spawned work across every profile.
func (h *Hub) FetchSpawned(ctx context.Context) (*SpawnList, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/spawned", nil)
	if err != nil {
		return nil, err
	}
	if h.Key != "" {
		req.Header.Set("Authorization", "Bearer "+h.Key)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /spawned: HTTP %d", resp.StatusCode)
	}
	var l SpawnList
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		return nil, err
	}
	return &l, nil
}

// FetchSpawnLog tails one spawned run's live log (the hub constrains the
// resolved path to the delegation/pi roots).
func (h *Hub) FetchSpawnLog(ctx context.Context, kind, id string, task, lines int) (string, error) {
	u := fmt.Sprintf("%s/spawn-log?kind=%s&id=%s&task=%d&lines=%d",
		h.BaseURL, url.QueryEscape(kind), url.QueryEscape(id), task, lines)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+h.Key)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("spawn-log: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Text string `json:"text"`
		Err  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Text, nil
}

// FetchChannels reads the native shape store (categories/channels/assignments)
// for one profile.
func (h *Hub) FetchChannels(ctx context.Context, profile string) (map[string]any, error) {
	u := fmt.Sprintf("%s/channels?profile=%s", h.BaseURL, url.QueryEscape(profile))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if h.Key != "" {
		req.Header.Set("Authorization", "Bearer "+h.Key)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /channels: HTTP %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// ChannelOp runs one store mutation (create|update|delete|assign). Validation
// failures come back as the hub's own error string, ready to relay to the UI.
func (h *Hub) ChannelOp(ctx context.Context, action string, body map[string]any) (map[string]any, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+"/channels/"+action, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.Key)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return nil, errors.New(e.Error)
		}
		return nil, fmt.Errorf("channels/%s: HTTP %d", action, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// MirrorTurn relays a completed turn into the session's Discord thread with
// native-style artifacts (tool lines, reasoning + reply, stats card).
// Returns (false, nil) when the session has no Discord binding.
func (h *Hub) MirrorTurn(ctx context.Context, sessionID string, since float64) (bool, error) {
	body, err := json.Marshal(map[string]any{"session_id": sessionID, "since": since})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+"/mirror_turn", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.Key)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("mirror_turn: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	if !out.OK {
		if out.Error == "no discord thread for session" || out.Error == "unknown session" {
			return false, nil
		}
		return false, fmt.Errorf("mirror_turn: %s", out.Error)
	}
	return true, nil
}

// Card fetches the rendered stats card for a finished turn, if fresh.
func (h *Hub) Card(ctx context.Context, sessionID string, since float64) (string, error) {
	u := fmt.Sprintf("%s/stats/card?session_id=%s&since=%f", h.BaseURL, url.QueryEscape(sessionID), since)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+h.Key)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("stats/card: HTTP %d", resp.StatusCode)
	}
	var out struct {
		OK   bool   `json:"ok"`
		Card string `json:"card"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", nil
	}
	return out.Card, nil
}

// SearchHit is one full-text match from the hub.
type SearchHit struct {
	MessageID int     `json:"message_id"`
	SessionID string  `json:"session_id"`
	Profile   string  `json:"profile"`
	Role      string  `json:"role"`
	Timestamp float64 `json:"timestamp"`
	Snippet   string  `json:"snippet"`
	Title     string  `json:"title"`
}

// TurnCard is one historical turn's stats card.
type TurnCard struct {
	EndTS float64 `json:"end_ts"`
	Line  string  `json:"line"`
}

// SessionTotal summarizes a session's recorded spend.
type SessionTotal struct {
	Turns int     `json:"turns"`
	Calls int     `json:"calls"`
	Cost  float64 `json:"cost_usd"`
	Out   int     `json:"out_tokens"`
	In    int     `json:"in_tokens"`
}

// TurnCards fetches every recorded turn's card for a session.
func (h *Hub) TurnCards(ctx context.Context, sessionID string) ([]TurnCard, SessionTotal, error) {
	u := fmt.Sprintf("%s/stats/turns?session_id=%s", h.BaseURL, url.QueryEscape(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, SessionTotal{}, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Key)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, SessionTotal{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, SessionTotal{}, fmt.Errorf("stats/turns: HTTP %d", resp.StatusCode)
	}
	var out struct {
		OK    bool         `json:"ok"`
		Turns []TurnCard   `json:"turns"`
		Total SessionTotal `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, SessionTotal{}, err
	}
	return out.Turns, out.Total, nil
}

// Search runs a full-text query across all sessions.
func (h *Hub) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	u := fmt.Sprintf("%s/search?q=%s&limit=%d", h.BaseURL, url.QueryEscape(query), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Key)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: HTTP %d", resp.StatusCode)
	}
	var out struct {
		OK      bool        `json:"ok"`
		Results []SearchHit `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// Mirror relays one message into the session's Discord thread when the
// session is Discord-bound. Returns (false, nil) when it has no binding.
func (h *Hub) Mirror(ctx context.Context, sessionID, role, content string) (bool, error) {
	body, err := json.Marshal(map[string]string{
		"session_id": sessionID,
		"role":       role,
		"content":    content,
	})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+"/mirror", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.Key)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("mirror: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	if !out.OK {
		if out.Error == "no discord thread for session" || out.Error == "unknown session" {
			return false, nil
		}
		return false, fmt.Errorf("mirror: %s", out.Error)
	}
	return true, nil
}
