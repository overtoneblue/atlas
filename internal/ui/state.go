package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"atlas/internal/config"
)

// readState persists client-side read marks: session id -> unix seconds of the
// last time the session was opened in Atlas. Lives at
// ~/.config/atlas/state.json (alongside the api env file).
type readState struct {
	Read map[string]float64 `json:"read"`
}

func loadReadState() map[string]float64 {
	p := config.StateFile()
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
	p := config.StateFile()
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

// ---- unread / read-state ----

// markRead records that a session was just seen; persisted lazily.
func (m *Model) markRead(id string) {
	if id == "" {
		return
	}
	if m.read == nil {
		m.read = map[string]float64{}
	}
	m.read[id] = float64(time.Now().Unix())
	m.readDirty = true
	m.maybeSaveRead()
}

// maybeSaveRead persists read state at most once per 30s while dirty.
func (m *Model) maybeSaveRead() {
	if !m.readDirty || time.Since(m.lastSave) < 30*time.Second {
		return
	}
	saveReadState(m.read)
	m.readDirty = false
	m.lastSave = time.Now()
}

// unread reports whether the node's session has activity newer than its read
// mark. A session never opened in Atlas reads as unread (dot until first open).
func (m Model) unread(n treeNode) bool {
	if n.sessionID == "" || n.lastActive == 0 {
		return false
	}
	seen, ok := m.read[n.sessionID]
	return !ok || n.lastActive > seen+1
}

// unreadCount counts unread posts in the current tree.
func (m Model) unreadCount() int {
	c := 0
	for _, n := range m.tree {
		if n.kind == kindPost && m.unread(n) {
			c++
		}
	}
	return c
}
