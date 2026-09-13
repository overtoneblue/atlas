// Package hermes is a minimal client for the Hermes API server.
//
// Atlas talks to a local `hermes gateway` API server (default
// http://127.0.0.1:8642) authenticated with a bearer key.
//
// Configuration:
//
//	ATLAS_API_URL  base URL (default http://127.0.0.1:8642)
//	ATLAS_API_KEY  bearer key (falls back to API_SERVER_KEY in $HERMES_HOME/.env)
package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

// NewFromEnv builds a client from the environment (see package docs).
func NewFromEnv() *Client {
	base := strings.TrimSpace(os.Getenv("ATLAS_API_URL"))
	if base == "" {
		base = "http://127.0.0.1:8642"
	}
	key := resolveKey()
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		Key:     key,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// resolveKey finds the API bearer key: env -> ~/.config/atlas/env -> hermes home .env.
func resolveKey() string {
	key := strings.TrimSpace(os.Getenv("ATLAS_API_KEY"))
	if key == "" {
		key = readKeyFromEnvFile(configEnvPath())
	}
	if key == "" {
		key = readKeyFromEnvFile(hermesEnvPath())
	}
	return key
}

// Configured reports whether we have what we need to go live.
func (c *Client) Configured() bool { return c != nil && c.Key != "" }

type Session struct {
	ID           string  `json:"id"`
	Source       string  `json:"source"`
	Title        string  `json:"title"`
	Preview      string  `json:"preview"`
	MessageCount int     `json:"message_count"`
	LastActive   float64 `json:"last_active"`
	Pinned       bool    `json:"pinned"`
	Hidden       bool    `json:"hidden"`
	Archived     bool    `json:"archived"`
}

type Message struct {
	ID        int          `json:"id"`
	Role      string       `json:"role"`
	Content   FlexibleText `json:"content"`
	ToolName  string       `json:"tool_name"`
	ToolCalls []ToolCall   `json:"tool_calls"`
	Reasoning FlexibleText `json:"reasoning"`
	Timestamp float64      `json:"timestamp"`
}

// FlexibleText accepts either a plain string or a multimodal array of parts
// (as returned for messages that carry images/blocks).
type FlexibleText string

func (t *FlexibleText) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*t = FlexibleText(s)
		return nil
	}
	var parts []map[string]any
	if err := json.Unmarshal(b, &parts); err != nil {
		*t = FlexibleText(string(b))
		return nil
	}
	var sb strings.Builder
	for _, p := range parts {
		if s, ok := p["text"].(string); ok && s != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(s)
		}
	}
	*t = FlexibleText(sb.String())
	return nil
}

type ToolCall struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (c *Client) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	var out struct {
		Data []Session `json:"data"`
	}
	u := fmt.Sprintf("%s/api/sessions?limit=%d", c.BaseURL, limit)
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) Messages(ctx context.Context, id string, limit int) ([]Message, error) {
	var out struct {
		Data []Message `json:"data"`
	}
	u := fmt.Sprintf("%s/api/sessions/%s/messages?limit=%d", c.BaseURL, url.PathEscape(id), limit)
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) get(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// configEnvPath is the per-user Atlas env file: ~/.config/atlas/env.
func configEnvPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "atlas", "env")
}

// hermesEnvPath is the active hermes home's .env (dev convenience).
func hermesEnvPath() string {
	root := os.Getenv("HERMES_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".hermes")
	}
	return filepath.Join(root, ".env")
}

// readKeyFromEnvFile reads ATLAS_API_KEY or API_SERVER_KEY from KEY=VALUE lines.
func readKeyFromEnvFile(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "ATLAS_API_KEY="); ok {
			return strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "API_SERVER_KEY="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
