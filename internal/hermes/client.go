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
	"strconv"
	"strings"
	"time"

	"atlas/internal/config"
)

type Client struct {
	BaseURL     string
	Key         string
	ProfileKeys map[string]string // profile name (lowercase) -> bearer key
	HTTP        *http.Client
}

// NewFromEnv builds a client from the environment (see package docs).
func NewFromEnv() *Client {
	base := strings.TrimSpace(os.Getenv("ATLAS_API_URL"))
	if base == "" {
		base = "http://127.0.0.1:8642"
	}
	key := resolveKey()
	return &Client{
		BaseURL:     strings.TrimRight(base, "/"),
		Key:         key,
		ProfileKeys: resolveProfileKeys(),
		HTTP:        &http.Client{Timeout: 15 * time.Second},
	}
}

// resolveProfileKeys reads ATLAS_API_KEY_<PROFILE> for secondary profiles from
// the environment, then the per-user env file (env wins).
func resolveProfileKeys() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if name, found := strings.CutPrefix(k, "ATLAS_API_KEY_"); found && name != "" {
			out[strings.ToLower(name)] = strings.TrimSpace(v)
		}
	}
	for k, v := range readEnvMap(config.EnvFile()) {
		if name, found := strings.CutPrefix(k, "ATLAS_API_KEY_"); found && name != "" {
			if _, exists := out[strings.ToLower(name)]; !exists {
				out[strings.ToLower(name)] = v
			}
		}
	}
	return out
}

// KeyFor returns the bearer key for a profile; "" and "default" mean the
// primary profile's key.
func (c *Client) KeyFor(profile string) string {
	if profile == "" || profile == "default" {
		return c.Key
	}
	return c.ProfileKeys[strings.ToLower(profile)]
}

// ConfiguredFor reports whether a usable key exists for the profile.
func (c *Client) ConfiguredFor(profile string) bool {
	return c != nil && c.KeyFor(profile) != ""
}

// apiPath prefixes non-default profiles with the gateway's /p/<profile> mount.
func apiPath(profile, p string) string {
	if profile == "" || profile == "default" {
		return p
	}
	return "/p/" + url.PathEscape(profile) + p
}

// resolveKey finds the API bearer key: env -> ~/.config/atlas/env -> hermes home .env.
func resolveKey() string {
	key := strings.TrimSpace(os.Getenv("ATLAS_API_KEY"))
	if key == "" {
		key = readKeyFromEnvFile(config.EnvFile())
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
	if err := c.get(ctx, u, c.Key, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// MessageQuery shapes one transcript read. Latest pages back from the
// newest message (the API returns each page in chronological order);
// IncludeCompacted widens the read to compaction-archived display history
// so a client can scroll past the last compaction boundary.
type MessageQuery struct {
	Limit            int
	Offset           int
	Latest           bool
	IncludeCompacted bool
}

func (c *Client) Messages(ctx context.Context, profile, id string, limit int) ([]Message, error) {
	return c.MessagesPage(ctx, profile, id, MessageQuery{Limit: limit})
}

// MessagesPage reads one transcript page (see MessageQuery).
func (c *Client) MessagesPage(ctx context.Context, profile, id string, q MessageQuery) ([]Message, error) {
	var out struct {
		Data []Message `json:"data"`
	}
	params := url.Values{}
	params.Set("limit", strconv.Itoa(q.Limit))
	if q.Offset > 0 {
		params.Set("offset", strconv.Itoa(q.Offset))
	}
	if q.Latest {
		params.Set("order", "latest")
	}
	if q.IncludeCompacted {
		params.Set("include_compacted", "1")
	}
	u := fmt.Sprintf("%s%s/api/sessions/%s/messages?%s", c.BaseURL, apiPath(profile, ""), url.PathEscape(id), params.Encode())
	if err := c.get(ctx, u, c.KeyFor(profile), &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// StopRun interrupts a running agent turn (POST /v1/runs/{run_id}/stop).
func (c *Client) StopRun(ctx context.Context, profile, runID string) error {
	key := c.KeyFor(profile)
	if key == "" {
		return fmt.Errorf("no API key configured for profile %q", profile)
	}
	u := fmt.Sprintf("%s%s/v1/runs/%s/stop", c.BaseURL, apiPath(profile, ""), url.PathEscape(runID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s: HTTP %d", u, resp.StatusCode)
	}
	return nil
}

func (c *Client) get(ctx context.Context, u, key string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
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
	m := readEnvMap(path)
	if v := m["ATLAS_API_KEY"]; v != "" {
		return v
	}
	return m["API_SERVER_KEY"]
}

// readEnvMap parses KEY=VALUE lines from an env file (missing file = empty map).
func readEnvMap(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
