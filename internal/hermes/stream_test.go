package hermes

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScanSSE_EventNameAndData(t *testing.T) {
	in := strings.Join([]string{
		"event: run.started",
		`data: {"run_id": "run_abc", "session_id": "s1"}`,
		"",
		"event: assistant.delta",
		`data: {"delta": "hello "}`,
		"",
		"event: assistant.delta",
		`data: {"delta": "world"}`,
		"",
		"event: done",
		`data: {"completed": true}`,
		"",
	}, "\n")

	type pair struct{ name, data string }
	var got []pair
	if err := scanSSE(strings.NewReader(in), func(name, data string) {
		got = append(got, pair{name, data})
	}); err != nil {
		t.Fatalf("scanSSE: %v", err)
	}

	want := []pair{
		{"run.started", `{"run_id": "run_abc", "session_id": "s1"}`},
		{"assistant.delta", `{"delta": "hello "}`},
		{"assistant.delta", `{"delta": "world"}`},
		{"done", `{"completed": true}`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The event name must reset at frame boundaries: the second frame in this
// stream carries no "event:" line, so consumers see an empty name — never the
// leaked name from the previous frame.
func TestScanSSE_NameResetsAtFrameBoundary(t *testing.T) {
	in := "event: done\ndata: {}\n\ndata: {\"delta\":\"x\"}\n\n"
	var got []string
	if err := scanSSE(strings.NewReader(in), func(name, data string) {
		got = append(got, name+"|"+data)
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"done|{}", "|{\"delta\":\"x\"}"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("frame %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestChatEventDecode(t *testing.T) {
	var ev ChatEvent
	raw := `{"delta":"x","run_id":"run_1","tool_name":"terminal","interrupted":true,"session_id":"s9"}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.RunID != "run_1" || ev.ToolName != "terminal" || !ev.Interrupted || ev.Delta != "x" || ev.SessionID != "s9" {
		t.Fatalf("decoded: %+v", ev)
	}
}
