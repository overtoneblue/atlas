package ui

import (
	"testing"
	"time"

	"atlas/internal/hermes"
)

func mkTree() []treeNode {
	fresh := float64(time.Now().Unix())
	old := float64(time.Now().Add(-8 * 24 * time.Hour).Unix())
	return []treeNode{
		{label: "Nolan", kind: kindProfile, depth: 0},
		{label: "Text Channels", kind: kindCategory, depth: 1},
		{label: "# general", kind: kindChannel, depth: 2},
		{label: "fresh chat", kind: kindPost, depth: 3, sessionID: "a", lastActive: fresh},
		{label: "old chat", kind: kindPost, depth: 3, sessionID: "b", lastActive: old},
		{label: "# stale-only", kind: kindChannel, depth: 2},
		{label: "old chat 2", kind: kindPost, depth: 3, sessionID: "c", lastActive: old},
	}
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestVisibleRowsStaleFilter(t *testing.T) {
	m := Model{tree: mkTree(), hideStale: true}
	rows := m.visibleRows()
	// stale posts hidden; the channel left with none is dropped too
	if want := []int{0, 1, 2, 3}; !eqInts(rows, want) {
		t.Fatalf("visibleRows = %v, want %v", rows, want)
	}
	if n := m.hiddenStaleCount(); n != 2 {
		t.Fatalf("hiddenStaleCount = %d, want 2", n)
	}
	m.hideStale = false
	if rows := m.visibleRows(); !eqInts(rows, []int{0, 1, 2, 3, 4, 5, 6}) {
		t.Fatalf("show-all rows = %v", rows)
	}
	if n := m.hiddenStaleCount(); n != 0 {
		t.Fatalf("hiddenStaleCount with show-all = %d", n)
	}
}

func TestVisibleRowsCollapse(t *testing.T) {
	m := Model{tree: mkTree(), hideStale: false}
	m.tree[1].collapsed = true // fold the whole category
	if rows := m.visibleRows(); !eqInts(rows, []int{0, 1}) {
		t.Fatalf("category folded rows = %v, want [0 1]", rows)
	}
	m.tree[1].collapsed = false
	m.tree[2].collapsed = true // fold just # general
	if rows := m.visibleRows(); !eqInts(rows, []int{0, 1, 2, 5, 6}) {
		t.Fatalf("channel folded rows = %v, want [0 1 2 5 6]", rows)
	}
}

func TestStaleEmptyContainersAndFold(t *testing.T) {
	// An all-stale container is stowed by the stale pass regardless of a
	// pending fold (you can only fold what's visible); folding then only
	// hides descendants — the container row itself always stays.
	m := Model{tree: mkTree(), hideStale: true}
	m.tree[5].collapsed = true // moot: all posts under it are stale
	if rows := m.visibleRows(); !eqInts(rows, []int{0, 1, 2, 3}) {
		t.Fatalf("stale-pass rows = %v, want [0 1 2 3]", rows)
	}
	m.hideStale = false
	if rows := m.visibleRows(); !eqInts(rows, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("show-all rows = %v, want [0 1 2 3 4 5]", rows)
	}
	m.tree[2].collapsed = true // fold # general: children hidden, row kept
	if rows := m.visibleRows(); !eqInts(rows, []int{0, 1, 2, 5}) {
		t.Fatalf("folded rows = %v, want [0 1 2 5]", rows)
	}
}

func TestMoveSkipsHiddenRows(t *testing.T) {
	m := Model{tree: mkTree(), hideStale: true, cursor: 0}
	for i := 0; i < 10; i++ {
		m = m.moveDown()
	}
	if m.cursor != 3 {
		t.Fatalf("cursor = %d, want 3 (end of visible rows)", m.cursor)
	}
	for i := 0; i < 10; i++ {
		m = m.moveUp()
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.cursor)
	}
	// cursor stranded on a hidden row snaps to the nearest visible one
	m.cursor = 6
	m = m.moveDown()
	if m.cursor != 3 {
		t.Fatalf("stranded cursor = %d, want 3", m.cursor)
	}
}

func TestToggleCollapse(t *testing.T) {
	m := Model{tree: mkTree(), cursor: 1}
	m = m.toggleCollapse()
	if !m.tree[1].collapsed {
		t.Fatalf("node not folded")
	}
	m = m.toggleCollapse()
	if m.tree[1].collapsed {
		t.Fatalf("node not unfolded")
	}
	// posts and leaves don't fold
	m.cursor = 3
	m = m.toggleCollapse()
	if m.tree[3].collapsed {
		t.Fatalf("post must not fold")
	}
}

func TestAgeTag(t *testing.T) {
	now := time.Now()
	cases := []struct {
		ts   float64
		want string
	}{
		{0, ""},
		{float64(now.Unix()), "now"},
		{float64(now.Add(-2 * time.Hour).Unix()), "2h"},
		{float64(now.Add(-3 * 24 * time.Hour).Unix()), "3d"},
		{float64(now.Add(-10 * 24 * time.Hour).Unix()), "1w"},
		{float64(now.Add(-70 * 24 * time.Hour).Unix()), "2mo"},
	}
	for _, c := range cases {
		if got := ageTag(c.ts); got != c.want {
			t.Errorf("ageTag(%v) = %q, want %q", c.ts, got, c.want)
		}
	}
}

func TestCollapseSurvivesRebuild(t *testing.T) {
	mk := func() []hermes.HubNode {
		return []hermes.HubNode{
			{Kind: "profile", Name: "Nolan", Children: []hermes.HubNode{
				{Kind: "category", Name: "Text Channels", Children: []hermes.HubNode{
					{Kind: "channel", Name: "# general", Children: []hermes.HubNode{
						{Kind: "post", Name: "chat", SessionID: "s1"},
					}},
				}},
			}},
		}
	}
	m := Model{}
	m.tree = treeFromHub(mk())
	idx := -1
	for i, n := range m.tree {
		if n.label == "# general" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("fixture node missing")
	}
	m.cursor = idx
	m = m.toggleCollapse()
	if !m.tree[idx].collapsed {
		t.Fatalf("fold not applied")
	}
	// simulate the periodic refresh: rebuild from scratch, re-apply folds
	m.tree = m.applyCollapse(treeFromHub(mk()))
	if !m.tree[idx].collapsed {
		t.Fatalf("fold lost across rebuild")
	}
	// unfold again and confirm the key is gone
	m.cursor = idx
	m = m.toggleCollapse()
	m.tree = m.applyCollapse(treeFromHub(mk()))
	if m.tree[idx].collapsed {
		t.Fatalf("unfold did not stick across rebuild")
	}
}
