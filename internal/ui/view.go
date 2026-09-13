package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// View renders the live TUI (bubbletea wiring).
func (m Model) View() string { return m.RenderFrame(m.width, m.height) }

// RenderFrame renders the whole app at an explicit size; used by both the
// live TUI and `atlas --once`.
func (m Model) RenderFrame(width, height int) string {
	if width < 72 || height < 16 {
		return "atlas: terminal too small (min 72×16)\n"
	}

	showRail := width >= 100
	leftW, railW := 30, 26
	if !showRail {
		railW = 0
	}
	seps := 1
	if showRail {
		seps = 2
	}
	midW := width - leftW - railW - seps
	contentH := height - 2 // composer + status bar

	sep := strings.TrimSuffix(strings.Repeat(styleSep.Render("│")+"\n", contentH), "\n")

	left := padBlock(strings.Join(m.renderTree(), "\n"), leftW, contentH)
	mid := padBlock(strings.Join(demoTranscript(midW), "\n"), midW, contentH)

	blocks := []string{left, sep, mid}
	if showRail {
		rail := padBlock(strings.Join(m.renderRail(), "\n"), railW, contentH)
		blocks = append(blocks, sep, rail)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, blocks...)

	return row + "\n" + m.renderComposer(width) + "\n" + m.renderStatus(width)
}

func (m Model) renderTree() []string {
	lines := []string{
		styleTitle.Render(" WORKSTREAMS"),
		styleDim.Render(" ───────────"),
	}
	for i, n := range m.tree {
		var content string
		switch n.kind {
		case kindCategory:
			content = " ▾ " + styleGold.Render(n.label)
		case kindChannel:
			content = "    " + styleChan.Render("# "+n.label)
		case kindPost:
			content = "      " + stylePost.Render("· "+n.label)
			if n.unread > 0 {
				content += " " + styleGreen.Render(fmt.Sprintf("%d", n.unread))
			}
		}
		mark := " "
		if i == m.cursor {
			mark = styleYel.Render("▸")
		}
		lines = append(lines, mark+content)
	}
	lines = append(lines,
		"",
		styleDim.Render(" ─────────────"),
		styleDim.Render(" 2 runs · ")+styleGreen.Render("3")+styleDim.Render(" unread"),
	)
	return lines
}

func (m Model) renderRail() []string {
	return []string{
		styleTitle.Render(" DETAILS"),
		styleDim.Render(" ───────"),
		kv("agent", "Nolan (default)"),
		kv("model", "deepseek-v4-flash"),
		kv("tags", "design · spike"),
		kv("thread", "#rich-desktop › 3"),
		kv("session", "2026…a1b2c3"),
		"",
		styleDim.Render(" ───────────"),
		styleTitle.Render(" ACTIVE RUNS"),
		styleGreen.Render(" ● Nolan") + styleDim.Render("  rendering 41s"),
		styleDim.Render(" ○ Debbie  idle"),
		"",
		styleDim.Render(" ───────────"),
		styleTitle.Render(" CONTEXT"),
		" " + styleGreen.Render("▓▓▓▓▓▓") + styleFaint.Render("░░░░░░") + " 41%",
	}
}

func kv(k, v string) string {
	return " " + styleDim.Render(fmt.Sprintf("%-7s", k)) + " " + v
}

func (m Model) renderComposer(width int) string {
	left := styleInsert.Render(" INSERT ") + " " + styleDim.Render("message…")
	hints := "enter send · esc normal · ^k jump · / search · ? help"
	lw, hw := ansi.StringWidth(left), ansi.StringWidth(hints)
	gap := width - lw - hw
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + styleDim.Render(hints)
}

func (m Model) renderStatus(width int) string {
	left := " NORMAL · atlas 0.0.1 · " + m.status
	right := "? help"
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	gap := width - lw - rw - 1
	if gap < 1 {
		return styleStatus.Width(width).Render(left)
	}
	return styleStatus.Width(width).Render(left + strings.Repeat(" ", gap) + right)
}

// padBlock normalizes a block to exactly width×height cells (ANSI-aware).
func padBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	out := make([]string, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
			if ansi.StringWidth(line) > width {
				line = ansi.Truncate(line, width, "…")
			}
		}
		out[i] = line + strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
	}
	return strings.Join(out, "\n")
}
