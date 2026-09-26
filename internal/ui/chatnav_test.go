package ui

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestFindMatches(t *testing.T) {
	lines := []string{
		styleDim.Render("hello world"),
		"plain HELLO again",
		"nothing here",
		"",
	}
	hits := findMatches(lines, "hello")
	if len(hits) != 2 || hits[0] != 0 || hits[1] != 1 {
		t.Fatalf("findMatches = %v, want [0 1]", hits)
	}
	if got := findMatches(lines, "  "); got != nil {
		t.Fatalf("blank query should be nil, got %v", got)
	}
	if got := findMatches(lines, "zzz"); got != nil {
		t.Fatalf("no-match query should be nil, got %v", got)
	}
}

func TestSelTextTrims(t *testing.T) {
	lines := []string{
		styleYel.Render("keep me"),
		"  ",
		"",
	}
	got := selText(lines, 0, 2)
	if got != "keep me" {
		t.Fatalf("selText = %q, want %q", got, "keep me")
	}
	if got := selText(lines, 0, 0); got != "keep me" {
		t.Fatalf("selText single = %q", got)
	}
}

func TestScrollMath(t *testing.T) {
	if top := topLine(100, 20, 30); top != 50 {
		t.Fatalf("topLine = %d, want 50", top)
	}
	if top := topLine(10, 20, 0); top != 0 {
		t.Fatalf("short doc topLine = %d, want 0", top)
	}
	if s := scrollFor(100, 20, 50); s != 30 {
		t.Fatalf("scrollFor = %d, want 30", s)
	}
	if s := scrollFor(100, 20, 0); s != 80 {
		t.Fatalf("scrollFor top = %d, want 80", s)
	}
	if s := scrollFor(100, 20, 95); s != 0 {
		t.Fatalf("scrollFor beyond end = %d, want 0", s)
	}
}

func TestOsc52(t *testing.T) {
	seq := osc52("hi")
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hi")) + "\a"
	if seq != want {
		t.Fatalf("osc52 = %q, want %q", seq, want)
	}
	if !strings.HasPrefix(seq, "\x1b]52;c;") || !strings.HasSuffix(seq, "\a") {
		t.Fatalf("osc52 envelope malformed: %q", seq)
	}
}

func TestRearm(t *testing.T) {
	line := styleBlue.Render("a") + "\x1b[0m tail"
	out := rearm(line, "\x1b[100m")
	if !strings.HasPrefix(out, "\x1b[100m") {
		t.Fatalf("missing prefix: %q", out)
	}
	if !strings.Contains(out, "\x1b[0m\x1b[100m") {
		t.Fatalf("reset not re-armed: %q", out)
	}
	if plain := stripANSI(rearm(line, "\x1b[100m")); plain != "a tail" {
		t.Fatalf("visible text changed: %q", plain)
	}
	if rearm("x", "") != "x" {
		t.Fatalf("empty seq should passthrough")
	}
}

func TestMsgAtAndSpan(t *testing.T) {
	g := visGeo{
		lines:  []string{"m1", "body", "", "m2", "body2", "", "m3"},
		ids:    []int{1, 2, 3},
		starts: []int{0, 3, 6},
		total:  7,
	}
	if i := g.msgAt(4); i != 1 {
		t.Fatalf("msgAt(4) = %d, want 1", i)
	}
	if i := g.msgAt(0); i != 0 {
		t.Fatalf("msgAt(0) = %d, want 0", i)
	}
	lo, hi := g.span(4, 4, true)
	if lo != 3 || hi != 4 {
		t.Fatalf("span whole = [%d,%d], want [3,4]", lo, hi)
	}
	lo, hi = g.span(0, 4, true)
	if lo != 0 || hi != 4 {
		t.Fatalf("span multi = [%d,%d], want [0,4]", lo, hi)
	}
	lo, hi = g.span(1, 2, false)
	if lo != 1 || hi != 1 {
		t.Fatalf("line span should trim trailing blank: [%d,%d], want [1,1]", lo, hi)
	}
}
