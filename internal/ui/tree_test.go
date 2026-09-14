package ui

import (
	"testing"

	"atlas/internal/hermes"
)

func TestTreeFromHub(t *testing.T) {
	sections := []hermes.HubNode{{
		Kind: "profile", Name: "Nolan", Profile: "default",
		Children: []hermes.HubNode{{
			Kind: "guild", Name: "server",
			Children: []hermes.HubNode{{
				Kind: "category", Name: "cat",
				Children: []hermes.HubNode{{
					Kind: "channel", Name: "chan",
					Children: []hermes.HubNode{
						{Kind: "post", Name: "p2", SessionID: "s2", Profile: "debbie", LastActive: 20},
						{Kind: "post", Name: "p1", SessionID: "s1", Profile: "default", LastActive: 10},
					},
				}},
			}},
		}},
	}}
	nodes := treeFromHub(sections)
	if len(nodes) != 6 {
		t.Fatalf("len = %d, want 6: %+v", len(nodes), nodes)
	}
	wantKinds := []nodeKind{kindProfile, kindGuild, kindCategory, kindChannel, kindPost, kindPost}
	wantDepths := []int{0, 1, 2, 3, 4, 4}
	for i := range nodes {
		if nodes[i].kind != wantKinds[i] || nodes[i].depth != wantDepths[i] {
			t.Errorf("node %d = {kind:%v depth:%d}, want {%v %d}", i, nodes[i].kind, nodes[i].depth, wantKinds[i], wantDepths[i])
		}
	}
	if nodes[4].sessionID != "s2" || nodes[5].sessionID != "s1" {
		t.Errorf("post session ids not preserved: %+v", nodes)
	}
	if nodes[4].profile != "debbie" || nodes[5].profile != "default" {
		t.Errorf("post profiles not preserved: %+v", nodes)
	}
}

func TestVisibleSessions(t *testing.T) {
	in := []hermes.Session{
		{ID: "a"},
		{ID: "b", Hidden: true},
		{ID: "c", Archived: true},
		{ID: "d"},
	}
	got := visibleSessions(in)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "d" {
		t.Fatalf("visibleSessions = %+v", got)
	}
}

func TestSessionTitle(t *testing.T) {
	if got := sessionTitle(hermes.Session{Title: "T", Preview: "P", ID: "I"}); got != "T" {
		t.Errorf("title preference: %q", got)
	}
	if got := sessionTitle(hermes.Session{Preview: "P", ID: "I"}); got != "P" {
		t.Errorf("preview fallback: %q", got)
	}
	if got := sessionTitle(hermes.Session{ID: "I"}); got != "I" {
		t.Errorf("id fallback: %q", got)
	}
}
