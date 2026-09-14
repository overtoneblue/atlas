package ui

import (
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
	msgCount   int
	lastActive float64
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
	nodes := []treeNode{{label: "RECENT SESSIONS", kind: kindCategory}}
	for _, s := range ss {
		nodes = append(nodes, treeNode{
			label:      sessionTitle(s),
			depth:      1,
			kind:       kindPost,
			sessionID:  s.ID,
			msgCount:   s.MessageCount,
			lastActive: s.LastActive,
		})
	}
	return nodes
}

func treeFromHub(sections []hermes.HubNode) []treeNode {
	var out []treeNode
	var walk func(n hermes.HubNode, depth int, guide string, last bool)
	walk = func(n hermes.HubNode, depth int, guide string, last bool) {
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
		out = append(out, treeNode{
			label:      n.Name,
			depth:      depth,
			kind:       kind,
			sessionID:  n.SessionID,
			profile:    n.Profile,
			guide:      guide,
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
			walk(c, depth+1, childGuide, i == len(n.Children)-1)
		}
	}
	for i, s := range sections {
		walk(s, 0, "", i == len(sections)-1)
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

func (m Model) findSession(id string) *hermes.Session {
	for i := range m.sessions {
		if m.sessions[i].ID == id {
			return &m.sessions[i]
		}
	}
	return nil
}
