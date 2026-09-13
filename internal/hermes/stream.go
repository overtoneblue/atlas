package hermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ChatEvent is one SSE event from POST /api/sessions/{id}/chat/stream.
// Event names seen: run.started, message.started, assistant.delta,
// tool.progress, tool.started, tool.completed, assistant.completed,
// run.completed, done, error.
type ChatEvent struct {
	Event       string `json:"-"`
	Delta       string `json:"delta"`
	ToolName    string `json:"tool_name"`
	Content     string `json:"content"`
	MessageID   string `json:"message_id"`
	SessionID   string `json:"session_id"`
	RunID       string `json:"run_id"`
	Completed   bool   `json:"completed"`
	Partial     bool   `json:"partial"`
	Interrupted bool   `json:"interrupted"`
}

// ChatStream runs one agent turn over SSE, invoking onEvent per event.
// It returns when the stream ends (done event / EOF / error / ctx cancel).
func (c *Client) ChatStream(ctx context.Context, sessionID, input string, onEvent func(ChatEvent)) error {
	body, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/api/sessions/%s/chat/stream", c.BaseURL, url.PathEscape(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// No overall client timeout: a turn can run for minutes. ctx controls.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("chat/stream: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return scanSSE(resp.Body, func(name, data string) {
		var ev ChatEvent
		if err := json.Unmarshal([]byte(data), &ev); err == nil {
			ev.Event = name
			onEvent(ev)
		}
	})
}

// scanSSE reads an SSE stream and delivers (event name, data payload) pairs.
// The Hermes gateway emits single-line "data:" fields carrying JSON; other
// framing (comments, multi-line data) is not produced and is ignored.
func scanSSE(r io.Reader, deliver func(name, data string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	evName := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			evName = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			deliver(evName, strings.TrimPrefix(line, "data: "))
		case line == "":
			evName = ""
		}
	}
	return sc.Err()
}
