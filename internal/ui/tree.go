package ui

import (
	"time"

	"atlas/internal/hermes"
)

type nodeKind int

const (
	kindGuild nodeKind = iota
	kindCategory
	kindChannel
	kindPost
	kindProfile
)

type treeNode struct {
	label      string
	depth      int
	kind       nodeKind
	sessionID  string
	profile    string
	guide      string // tree-guide prefix ("│  " / "   " segments), set by treeFromHub
	key        string // stable path key (profile/section/label) — survives refreshes
	msgCount   int
	lastActive float64
	collapsed  bool // container folded by the user (enter)
}

func visibleSessions(in []hermes.Session) []hermes.Session {
	out := make([]hermes.Session, 0, len(in))
	for _, s := range in {
		if s.Hidden || s.Archived {
			continue
		}
		out = append(out, s)
	}
	return out
}

func sessionTitle(s hermes.Session) string {
	if s.Title != "" {
		return s.Title
	}
	if s.Preview != "" {
		return s.Preview
	}
	return s.ID
}

func treeFromSessions(ss []hermes.Session) []treeNode {
	nodes := []treeNode{{label: "RECENT SESSIONS", kind: kindCategory, key: "flat"}}
	for _, s := range ss {
		nodes = append(nodes, treeNode{
			label:      sessionTitle(s),
			depth:      1,
			kind:       kindPost,
			sessionID:  s.ID,
			key:        "flat/" + s.ID,
			msgCount:   s.MessageCount,
			lastActive: s.LastActive,
		})
	}
	return nodes
}

func treeFromHub(sections []hermes.HubNode) []treeNode {
	var out []treeNode
	var walk func(n hermes.HubNode, depth int, guide, parentKey string, last bool)
	walk = func(n hermes.HubNode, depth int, guide, parentKey string, last bool) {
		var kind nodeKind
		switch n.Kind {
		case "profile":
			kind = kindProfile
		case "guild":
			kind = kindGuild
		case "category":
			kind = kindCategory
		case "channel":
			kind = kindChannel
		default:
			kind = kindPost
		}
		key := n.Name
		if parentKey != "" {
			key = parentKey + "/" + key
		}
		out = append(out, treeNode{
			label:      n.Name,
			depth:      depth,
			kind:       kind,
			sessionID:  n.SessionID,
			profile:    n.Profile,
			guide:      guide,
			key:        key,
			msgCount:   n.MessageCount,
			lastActive: n.LastActive,
		})
		// Guide prefix for children: continue the vertical bar unless this node
		// is the last among its siblings. Direct children of section roots get
		// a plain indent (sections are not continuation columns).
		childGuide := guide + "  "
		if depth > 0 {
			childGuide = guide + "│ "
			if last {
				childGuide = guide + "  "
			}
		}
		for i, c := range n.Children {
			walk(c, depth+1, childGuide, key, i == len(n.Children)-1)
		}
	}
	for i, s := range sections {
		walk(s, 0, "", "", i == len(sections)-1)
	}
	return out
}

func countPosts(tree []treeNode) int {
	n := 0
	for _, t := range tree {
		if t.kind == kindPost {
			n++
		}
	}
	return n
}

// ---- visibility: stale filtering + collapse --------------------------------

// staleAfter is how long a conversation may sit idle before the tree calls it
// old and (by default) stows it away. Mirrors Discord's shelf life.
const staleAfter = 7 * 24 * time.Hour

// staleAt reports whether a post row idled past staleAfter.
func staleAt(n treeNode, now time.Time) bool {
	return n.kind == kindPost && n.lastActive > 0 &&
		now.Sub(time.Unix(int64(n.lastActive), 0)) > staleAfter
}

// hasKids reports whether tree[i] has children (flat pre-order layout).
func (m Model) hasKids(i int) bool {
	return i+1 < len(m.tree) && m.tree[i+1].depth > m.tree[i].depth
}

// hiddenStaleCount counts the posts the stale filter is stowing right now.
func (m Model) hiddenStaleCount() int {
	if !m.hideStale {
		return 0
	}
	now := time.Now()
	n := 0
	for _, t := range m.tree {
		if staleAt(t, now) {
			n++
		}
	}
	return n
}

// visibleRows returns the tree indexes that render right now: stale posts are
// skipped while hideStale is set, containers left with nothing to show are
// dropped, and descendants of collapsed nodes are hidden. Collapse never
// hides the container itself, so the fold stays reachable.
func (m Model) visibleRows() []int {
	n := len(m.tree)
	if n == 0 {
		return nil
	}
	now := time.Now()
	// subtree end index per node (first later node with depth <= its depth)
	subEnd := make([]int, n)
	var stack []int
	for i := 0; i < n; i++ {
		for len(stack) > 0 && m.tree[stack[len(stack)-1]].depth >= m.tree[i].depth {
			subEnd[stack[len(stack)-1]] = i
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, i)
	}
	for _, i := range stack {
		subEnd[i] = n
	}
	// stale / empty pass (collapse deliberately ignored here)
	show := make([]bool, n)
	for i := n - 1; i >= 0; i-- {
		if m.tree[i].kind == kindPost {
			show[i] = !(m.hideStale && staleAt(m.tree[i], now))
			continue
		}
		if !m.hasKids(i) {
			show[i] = true
			continue
		}
		for j := i + 1; j < subEnd[i]; j++ {
			if show[j] {
				show[i] = true
				break
			}
		}
	}
	// collapse pass
	var out []int
	hideBelow := -1
	for i := 0; i < n; i++ {
		if !show[i] {
			continue
		}
		if hideBelow >= 0 && m.tree[i].depth > hideBelow {
			continue
		}
		hideBelow = -1
		out = append(out, i)
		if m.tree[i].collapsed && m.hasKids(i) {
			hideBelow = m.tree[i].depth
		}
	}
	return out
}

// treePos is the position of the cursor inside rows (-1 when not visible).
func treePos(rows []int, cursor int) int {
	for i, r := range rows {
		if r == cursor {
			return i
		}
	}
	return -1
}

// displayPos is like treePos but falls back to the nearest row at-or-before
// the cursor — used for windowing when the cursor row is currently hidden.
func displayPos(rows []int, cursor int) int {
	if p := treePos(rows, cursor); p >= 0 {
		return p
	}
	best := 0
	for i, r := range rows {
		if r <= cursor {
			best = i
		} else {
			break
		}
	}
	return best
}

// clampCursor snaps the cursor to the nearest visible row.
func (m Model) clampCursor() Model {
	rows := m.visibleRows()
	if len(rows) == 0 {
		return m
	}
	if treePos(rows, m.cursor) < 0 {
		m.cursor = rows[displayPos(rows, m.cursor)]
	}
	return m
}

func (m Model) findSession(id string) *hermes.Session {
	for i := range m.sessions {
		if m.sessions[i].ID == id {
			return &m.sessions[i]
		}
	}
	return nil
}
