package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
