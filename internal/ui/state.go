package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// readState persists client-side read marks: session id -> unix seconds of the
// last time the session was opened in Atlas. Lives at
// ~/.config/atlas/state.json (alongside the api env file).
type readState struct {
	Read map[string]float64 `json:"read"`
}

func statePath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "atlas", "state.json")
}

func loadReadState() map[string]float64 {
	p := statePath()
	if p == "" {
		return map[string]float64{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return map[string]float64{}
	}
	var s readState
	if json.Unmarshal(b, &s) != nil || s.Read == nil {
		return map[string]float64{}
	}
	return s.Read
}

// saveReadState writes atomically (tmp + rename); failures are non-fatal —
// unread marks are a convenience, never a correctness surface.
func saveReadState(read map[string]float64) {
	p := statePath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	b, err := json.MarshalIndent(readState{Read: read}, "", "  ")
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}
