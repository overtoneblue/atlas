package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestApprovalNote(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantNote string
		wantKind string
	}{
		{
			"smart approved",
			`{"output": "", "exit_code": 0, "error": null, "approval": "Command was flagged (delete in root path) and auto-approved by smart approval."}`,
			"flagged (delete in root path) — auto-approved (smart approval).",
			"decided",
		},
		{
			"smart denied",
			`{"approval": "Command was flagged (rm -rf /) and denied by smart approval."}`,
			"flagged (rm -rf /) — denied (smart approval).",
			"block",
		},
		{
			"blocked wording",
			`{"approval": "BLOCKED: approval required (delete in root path) but no interactive user is present"}`,
			"BLOCKED: approval required (delete in root path) but no interactive user is present",
			"block",
		},
		{"no approval field", `{"output": "hi", "exit_code": 0}`, "", ""},
		{"not json", "plain text result", "", ""},
		{"json array", `[1, 2, 3]`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			note, kind := approvalNote(c.in)
			if note != c.wantNote || kind != c.wantKind {
				t.Errorf("approvalNote(%q) = (%q, %q), want (%q, %q)", c.in, note, kind, c.wantNote, c.wantKind)
			}
		})
	}
}

func TestTruncLine(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 6, "hello…"},
		{"hello world", 1, "…"},
		{"anything", 0, ""},
	}
	for _, c := range cases {
		if got := truncLine(c.in, c.w); got != c.want {
			t.Errorf("truncLine(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
}

func TestWrapIndent_ShortText(t *testing.T) {
	got := wrapIndent("hello world", 40, "  ", 5)
	if len(got) != 1 || got[0] != "  hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestWrapIndent_WrapsWithinWidth(t *testing.T) {
	text := "aaa bbb ccc ddd eee fff ggg hhh iii jjj"
	got := wrapIndent(text, 16, "  ", 10)
	if len(got) < 2 {
		t.Fatalf("expected wrapping, got %q", got)
	}
	for i, l := range got {
		if w := ansi.StringWidth(l); w > 16 {
			t.Errorf("line %d width %d exceeds 16: %q", i, w, l)
		}
	}
}

func TestWrapIndent_TruncatesWithEllipsis(t *testing.T) {
	text := "w01 w02 w03 w04 w05 w06 w07 w08 w09 w10 w11 w12 w13 w14 w15 w16 w17 w18 w19 w20 w21 w22"
	got := wrapIndent(text, 16, "  ", 3)
	if len(got) == 0 || len(got) > 3 {
		t.Fatalf("got %d lines, want 1..3: %q", len(got), got)
	}
	last := got[len(got)-1]
	if !strings.HasSuffix(last, " …") {
		t.Errorf("last line should end with truncation mark: %q", last)
	}
	for i, l := range got {
		if w := ansi.StringWidth(l); w > 16 {
			t.Errorf("line %d width %d exceeds 16: %q", i, w, l)
		}
	}
}
