package ui

import (
	"testing"

	"atlas/internal/hermes"
)

func TestAssignCards(t *testing.T) {
	msgs := []hermes.Message{
		{ID: 1, Role: "user", Timestamp: 10},
		{ID: 2, Role: "assistant", Timestamp: 20},
		{ID: 3, Role: "assistant", Timestamp: 30},
		{ID: 4, Role: "user", Timestamp: 40},
		{ID: 5, Role: "assistant", Timestamp: 50},
	}
	cards := []hermes.TurnCard{
		{EndTS: 31, Line: "card-a"},
		{EndTS: 60, Line: "card-b"},
	}
	got := assignCards(msgs, cards)
	want := map[int]string{3: "card-a", 5: "card-b"}
	if len(got) != len(want) {
		t.Fatalf("assignCards = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("card for msg %d = %q, want %q", k, got[k], v)
		}
	}
}

func TestAssignCards_EmptyInputs(t *testing.T) {
	if got := assignCards(nil, nil); len(got) != 0 {
		t.Fatalf("nil inputs: %v", got)
	}
	if got := assignCards([]hermes.Message{{ID: 1, Role: "assistant", Timestamp: 5}}, nil); len(got) != 0 {
		t.Fatalf("no cards: %v", got)
	}
}

func TestUnread(t *testing.T) {
	m := Model{read: map[string]float64{"a": 100, "debbie~d1": 200}}
	cases := []struct {
		name string
		node treeNode
		want bool
	}{
		{"never opened", treeNode{kind: kindPost, sessionID: "b", lastActive: 50}, true},
		{"read, no new activity", treeNode{kind: kindPost, sessionID: "a", lastActive: 100}, false},
		{"read, newer activity", treeNode{kind: kindPost, sessionID: "a", lastActive: 102}, true},
		{"no session", treeNode{kind: kindPost}, false},
		{"no activity timestamp", treeNode{kind: kindPost, sessionID: "c"}, false},
		{"profile-scoped read mark", treeNode{kind: kindPost, sessionID: "d1", profile: "debbie", lastActive: 200}, false},
		{"profile-scoped, newer activity", treeNode{kind: kindPost, sessionID: "d1", profile: "debbie", lastActive: 205}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := m.unread(c.node); got != c.want {
				t.Errorf("unread(%+v) = %v, want %v", c.node, got, c.want)
			}
		})
	}
}

func TestReadKey(t *testing.T) {
	if got := readKey("", "abc"); got != "abc" {
		t.Errorf("default key = %q", got)
	}
	if got := readKey("default", "abc"); got != "abc" {
		t.Errorf("explicit default key = %q", got)
	}
	if got := readKey("debbie", "abc"); got != "debbie~abc" {
		t.Errorf("secondary key = %q", got)
	}
}

func TestUnreadCount(t *testing.T) {
	m := Model{
		read: map[string]float64{"a": 100},
		tree: []treeNode{
			{kind: kindGuild, label: "srv"},
			{kind: kindChannel, label: "#chan"},
			{kind: kindPost, sessionID: "a", lastActive: 100},
			{kind: kindPost, sessionID: "b", lastActive: 50},
			{kind: kindChannel, label: "#other"},
			{kind: kindPost, sessionID: "c", lastActive: 1},
		},
	}
	if got := m.unreadCount(); got != 2 {
		t.Fatalf("unreadCount = %d, want 2", got)
	}
}

func TestLayoutWidths(t *testing.T) {
	cases := []struct {
		name               string
		w                  int
		hideTree, hideRail bool
		wantTree, wantRail bool
		wantMid            int
	}{
		{"default wide", 140, false, false, true, true, 140 - 30 - 26 - 2},
		{"tree hidden", 140, true, false, false, true, 140 - 26 - 1},
		{"rail hidden", 140, false, true, true, false, 140 - 30 - 1},
		{"both hidden", 140, true, true, false, false, 140},
		{"narrow keeps rail off", 90, false, false, true, false, 90 - 30 - 1},
		{"narrow tree hidden", 90, true, false, false, false, 90},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Model{width: c.w, hideTree: c.hideTree, hideRail: c.hideRail}
			st, sr, _, _, mid := m.layoutWidths(c.w)
			if st != c.wantTree || sr != c.wantRail || mid != c.wantMid {
				t.Fatalf("tree=%v rail=%v mid=%d, want tree=%v rail=%v mid=%d",
					st, sr, mid, c.wantTree, c.wantRail, c.wantMid)
			}
		})
	}
}

func TestZenRestore(t *testing.T) {
	m := Model{hideRail: true}
	m.toggleZen()
	if !m.hideTree || !m.hideRail {
		t.Fatalf("zen should hide both, got tree=%v rail=%v", m.hideTree, m.hideRail)
	}
	m.toggleZen()
	if m.hideTree || !m.hideRail {
		t.Fatalf("zen restore should keep prior rail-hidden state, got tree=%v rail=%v", m.hideTree, m.hideRail)
	}
	if m.focus != 1 {
		t.Fatalf("zen should focus the transcript, got focus=%d", m.focus)
	}
}
