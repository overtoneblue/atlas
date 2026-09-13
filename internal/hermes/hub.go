package hermes

import (
	"bytes"
	"context"
	"encoding/json"
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
	ChatType     string    `json:"chat_type,omitempty"`
	LastActive   float64   `json:"last_active,omitempty"`
	MessageCount int       `json:"message_count,omitempty"`
	Pinned       bool      `json:"pinned,omitempty"`
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

func (h *Hub) FetchTree(ctx context.Context) (*HubTree, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/tree", nil)
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
	Role      string  `json:"role"`
	Timestamp float64 `json:"timestamp"`
	Snippet   string  `json:"snippet"`
	Title     string  `json:"title"`
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
