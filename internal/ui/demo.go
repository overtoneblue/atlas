package ui

import "strings"

// Demo content for 0.0.1 — replaced by live Hermes data next.
// The tree mirrors the server's Discord shape; the transcript mirrors the
// investigation thread that produced this design.

func demoTree() []treeNode {
	return []treeNode{
		{label: "AGENT-IMPROVEMENT", depth: 0, kind: kindCategory},
		{label: "overall-hermes…", depth: 1, kind: kindChannel},
		{label: "stats-card → Matrix", depth: 2, kind: kindPost},
		{label: "rich-desktop…", depth: 1, kind: kindChannel},
		{label: "TUI design & mirror", depth: 2, kind: kindPost, unread: 2},
		{label: "Investigation kickoff", depth: 2, kind: kindPost},
		{label: "pi-harness eval notes", depth: 2, kind: kindPost},
		{label: "internal-tools", depth: 1, kind: kindChannel},
		{label: "verifier v0.3", depth: 2, kind: kindPost},
		{label: "GAMES-TUTORIAL-EXPERTS", depth: 0, kind: kindCategory},
		{label: "rimworld", depth: 1, kind: kindChannel},
		{label: "early-game guide", depth: 2, kind: kindPost},
		{label: "TEXT CHANNELS", depth: 0, kind: kindCategory},
		{label: "general", depth: 1, kind: kindChannel},
	}
}

func demoTranscript(width int) []string {
	rule := strings.Repeat("─", max(8, width-2))
	return []string{
		styleTitle.Render("#rich-desktop-hermes-app › TUI design & mirror") + "  " + styleDim.Render("design · spike"),
		styleDim.Render("Nolan (default) · deepseek-v4-flash · 20260913_a1b2c3 · live"),
		styleDim.Render(" " + rule),
		"",
		styleBlu.Render(" Overtoneblue") + styleDim.Render(" · 8:59 PM"),
		"   so can we fashion the tui after discord's shape?",
		"   with category / channel / forum threads?",
		"",
		styleMauve.Render(" Nolan") + styleDim.Render(" · 9:01 PM"),
		"   Yes — the IA maps 1:1 onto real objects:",
		"   category = area · channel = workstream",
		"   post = one conversation = session + thread",
		"   Same shape as Discord — but the TUI can render",
		"   what Discord can't: structured tool cards,",
		"   live runs, meters, instant search.",
		"",
		styleMag.Render("  ▸ tool · terminal") + styleDim.Render("  ss -tlnp | grep 9119   0.3s"),
		styleGreen.Render("     ✓ 9119 · hermes-serve · loopback"),
		"",
		styleRed.Render("  !") + styleYel.Render(" APPROVAL — write /srv/nixos-config/modules/…"),
		styleDim.Render("    [a] allow once    [A] always    [d] deny"),
		"",
		styleMauve.Render(" Nolan") + " ▍" + styleDim.Render(" streaming · rendering mockup…") + styleDim.Render("  41 tok/s"),
		"",
		styleFaint.Render("  (demo content until Hermes wiring lands)"),
	}
}
