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
	m := Model{read: map[string]float64{"a": 100}}
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := m.unread(c.node); got != c.want {
				t.Errorf("unread(%+v) = %v, want %v", c.node, got, c.want)
			}
		})
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
