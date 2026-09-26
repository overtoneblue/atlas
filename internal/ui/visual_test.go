package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"atlas/internal/hermes"
)

func TestVisSelectionGeometry(t *testing.T) {
	m := Model{width: 150, height: 46}
	m.openID = "sess-x"
	m.openTitle = "t"
	m.messages = []hermes.Message{
		{ID: 1, Role: "assistant", Content: "hello there", Reasoning: "thought about it"},
		{ID: 2, Role: "user", Content: "second message"},
		{ID: 3, Role: "assistant", Content: "third one", Reasoning: "more thinking"},
	}
	m.visMode = true
	m.visAnchor = 0
	m.visCursor = 1000 // beyond EOF on purpose: must clamp, not panic
	lines := m.renderTranscript(m.midWidth(), m.midViewport())
	g := m.visGeo()
	lo, hi := g.span(m.visAnchor, m.visCursor, m.visMsg)
	// trailing blank separator is trimmed by design: [0, total-2]
	if lo != 0 || hi != g.total-2 {
		t.Fatalf("span=[%d,%d] total=%d — want [0,%d]", lo, hi, g.total, g.total-2)
	}
	w := m.midWidth()
	for i, l := range lines {
		got := ansi.StringWidth(l)
		if i <= hi-lo {
			if got != w {
				t.Errorf("selected line %d width=%d, want %d (padded): %q", i, got, w, l)
			}
		} else if got == w && strings.TrimSpace(stripANSI(l)) != "" && ansi.StringWidth(stripANSI(l)) < w {
			t.Errorf("unselected line %d unexpectedly padded: %q", i, l)
		}
	}
	if i := 3; strings.TrimSpace(stripANSI(lines[i])) != "" || ansi.StringWidth(lines[i]) != w {
		t.Errorf("blank selected line not banded: %q", lines[i])
	}
}
