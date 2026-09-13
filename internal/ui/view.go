package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"atlas/internal/hermes"
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
	mid := padBlock(strings.Join(m.renderTranscript(midW, contentH), "\n"), midW, contentH)

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
		indent := strings.Repeat("  ", min(n.depth, 3))
		glyph := "· "
		switch n.kind {
		case kindGuild:
			glyph = "≡ "
		case kindCategory:
			glyph = "▾ "
		case kindChannel:
			glyph = "# "
		}
		budget := 30 - 1 - len(indent) - len(glyph) - 1
		label := truncLine(n.label, budget)
		var styled string
		switch n.kind {
		case kindGuild:
			styled = styleTitle.Render(label)
		case kindCategory:
			styled = styleGold.Render(label)
		case kindChannel:
			styled = styleChan.Render(label)
		default:
			styled = stylePost.Render(label)
		}
		mark := " "
		if i == m.cursor {
			mark = styleYel.Render("▸")
		}
		lines = append(lines, mark+indent+styleDim.Render(glyph)+styled)
	}
	lines = append(lines, "")
	posts := countPosts(m.tree)
	if posts > 0 {
		lines = append(lines,
			styleDim.Render(" ─────────────"),
			styleDim.Render(fmt.Sprintf(" %d posts · ", posts))+styleGreen.Render(m.modeLabel()),
		)
	} else {
		lines = append(lines,
			styleDim.Render(" ─────────────"),
			styleDim.Render(" 2 runs · ")+styleGreen.Render("3")+styleDim.Render(" unread"),
		)
	}
	return lines
}

func (m Model) modeLabel() string {
	if m.client.Configured() {
		if m.hub.Configured() {
			return "hub"
		}
		return "live"
	}
	return "demo"
}

func (m Model) renderTranscript(width, height int) []string {
	var lines []string
	switch {
	case m.helpVisible:
		lines = helpLines(width)
	case m.openID != "":
		lines = m.liveTranscript(width)
	case len(m.sessions) > 0 || (m.client.Configured() && m.hub.Configured()):
		lines = hintTranscript(width)
	default:
		lines = demoTranscript(width)
	}
	// Tail-fit so the newest messages are visible.
	if len(lines) > height {
		keep := height - 5
		if keep < 4 {
			keep = 4
		}
		tail := lines[len(lines)-keep:]
		head := lines[:3]
		lines = append(append([]string{}, head...),
			styleDim.Render(fmt.Sprintf("  ↕ %d earlier lines (scrolling lands next)", len(lines)-keep-3)))
		lines = append(lines, "")
		lines = append(lines, tail...)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func (m Model) liveTranscript(width int) []string {
	src := "·"
	if s := m.findSession(m.openID); s != nil {
		src = s.Source
	}
	rule := strings.Repeat("─", max(8, width-2))
	lines := []string{
		styleTitle.Render(truncLine(m.openTitle, max(10, width-24))) + "  " + styleDim.Render(src),
		styleDim.Render(fmt.Sprintf("session %s · %d msgs · %s", m.openID, m.msgCount(), relTime(m.openLast))),
		styleDim.Render(" " + rule),
		"",
	}
	if len(m.messages) == 0 && !m.streaming {
		lines = append(lines, styleFaint.Render("  loading transcript…"))
		return lines
	}
	msgs := m.messages
	if len(msgs) > 120 {
		msgs = msgs[len(msgs)-120:]
	}
	for _, msg := range msgs {
		lines = append(lines, renderMessage(msg, width)...)
	}
	if m.streaming {
		lines = append(lines, styleMauve.Render(" Nolan")+styleDim.Render(" · streaming"))
		buf := cleanText(m.streamBuf)
		if buf == "" {
			buf = "…"
		}
		lines = append(lines, wrapIndent(buf+"▍", width, "   ", 40)...)
		lines = append(lines, "")
	}
	return lines
}

func hintTranscript(width int) []string {
	return []string{
		styleTitle.Render(" WORKSTREAM"),
		styleDim.Render(" ──────────"),
		"",
		"  select a post in the tree",
		"  and press " + styleYel.Render("enter") + " to open",
		"  its conversation.",
		"",
		styleDim.Render("  categories, channels and posts"),
		styleDim.Render("  mirror the discord workspace."),
	}
}

func helpLines(width int) []string {
	rule := strings.Repeat("─", max(8, min(width-2, 46)))
	return []string{
		styleTitle.Render(" ATLAS — KEYMAP") + styleDim.Render("   esc closes"),
		styleDim.Render(" " + rule),
		"",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "j / k")) + " move through the tree",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "g / G")) + " first / last row",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "enter")) + " open the selected post",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "i")) + " insert mode — write a message",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "enter")) + " send it (while in insert mode)",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "esc")) + " leave insert · detach a stream",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "R")) + " refresh tree + sessions",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "tab")) + " cycle focus panes",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "?")) + " this panel",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "q")) + " quit",
		"",
		styleDim.Render("  sending runs a real agent turn in the"),
		styleDim.Render("  open session and streams the reply back here."),
	}
}

func renderMessage(msg hermes.Message, width int) []string {
	var out []string
	ts := hmTime(msg.Timestamp)
	switch msg.Role {
	case "user":
		out = append(out, styleBlu.Render(" Overtoneblue")+styleDim.Render(" · "+ts))
		out = append(out, wrapIndent(cleanText(msg.Content), width, "   ", 6)...)
		out = append(out, "")
	case "assistant":
		out = append(out, styleMauve.Render(" Nolan")+styleDim.Render(" · "+ts))
		if c := cleanText(msg.Content); c != "" {
			out = append(out, wrapIndent(c, width, "   ", 10)...)
		}
		for _, tc := range msg.ToolCalls {
			out = append(out, styleMag.Render("   ▸ tool · "+tc.Function.Name))
		}
		out = append(out, "")
	case "tool":
		first := firstLine(cleanText(msg.Content))
		out = append(out, styleDim.Render("   ↳ "+truncLine(first, max(8, width-8))))
	default:
	}
	return out
}

func (m Model) renderRail() []string {
	lines := []string{
		styleTitle.Render(" DETAILS"),
		styleDim.Render(" ───────"),
	}
	if m.openID != "" {
		src := "·"
		if s := m.findSession(m.openID); s != nil {
			src = s.Source
		}
		lines = append(lines,
			kv("source", src),
			kv("msgs", fmt.Sprintf("%d", m.msgCount())),
			kv("active", relTime(m.openLast)),
			kv("session", truncLine(m.openID, 13)),
			"",
			styleDim.Render(" ───────────"),
			styleTitle.Render(" TREE"),
			styleGreen.Render(" ● ")+styleDim.Render(fmt.Sprintf("%d posts", countPosts(m.tree))),
			"",
			styleDim.Render(" ───────────"),
			styleTitle.Render(" MODE"),
			styleGreen.Render(" "+m.modeLabel())+styleDim.Render(" · head"),
		)
		return lines
	}
	lines = append(lines,
		styleDim.Render(" select a post, press"),
		styleDim.Render(" enter to open it"),
		"",
		styleDim.Render(" ───────────"),
		styleTitle.Render(" KEYS"),
		kv("j/k", "move"),
		kv("enter", "open"),
		kv("R", "refresh"),
		kv("q", "quit"),
		"",
		styleDim.Render(" ───────────"),
		styleTitle.Render(" MODE"),
		styleGreen.Render(" "+m.modeLabel())+styleDim.Render(" · head"),
	)
	return lines
}

func kv(k, v string) string {
	return " " + styleDim.Render(fmt.Sprintf("%-7s", k)) + " " + v
}

func (m Model) renderComposer(width int) string {
	var left, hints string
	switch {
	case m.inserting:
		left = styleInsert.Render(" INSERT ") + " " + m.input + styleYel.Render("▍")
		hints = "enter send · esc back to normal"
	case m.streaming:
		left = styleMauve.Render(" STREAM ") + " " + styleDim.Render("turn in flight")
		hints = "esc detach · reply lands in the transcript"
	default:
		left = styleInsert.Render(" NORMAL ") + " " + styleDim.Render("press i to write a message")
		hints = "j/k move · enter open · i write · ? help"
	}
	lw, hw := ansi.StringWidth(left), ansi.StringWidth(hints)
	gap := width - lw - hw
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + styleDim.Render(hints)
}

func (m Model) renderStatus(width int) string {
	mode := "NORMAL"
	if m.inserting {
		mode = "INSERT"
	}
	if m.streaming {
		mode = "STREAM"
	}
	left := " " + mode + " · atlas 0.3.0 · " + m.status
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

// ---- text helpers ----

func hmTime(ts float64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(int64(ts), 0).Local().Format("3:04 PM")
}

func relTime(ts float64) string {
	if ts <= 0 {
		return "?"
	}
	d := time.Since(time.Unix(int64(ts), 0))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "  ")
	return strings.TrimSpace(s)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func truncLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

// wrapIndent word-wraps text (keeping embedded newlines as paragraph breaks)
// under an indent, capping at maxLines with an ellipsis marker.
func wrapIndent(text string, width int, indent string, maxLines int) []string {
	avail := width - ansi.StringWidth(indent) - 1
	if avail < 8 {
		avail = 8
	}
	var out []string
	truncated := false
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimRight(para, " \t")
		if para == "" {
			if len(out) < maxLines && len(out) > 0 {
				out = append(out, "")
			}
			continue
		}
		line := ""
		for _, w := range strings.Fields(para) {
			if len([]rune(w)) > avail {
				w = truncLine(w, avail)
			}
			switch {
			case line == "":
				line = w
			case ansi.StringWidth(line)+1+ansi.StringWidth(w) <= avail:
				line += " " + w
			default:
				if len(out) >= maxLines-1 {
					truncated = true
					break
				}
				out = append(out, indent+line)
				line = w
			}
			if truncated {
				break
			}
		}
		if truncated {
			break
		}
		if line != "" {
			out = append(out, indent+line)
		}
		if len(out) >= maxLines {
			truncated = true
			break
		}
	}
	if truncated && len(out) > 0 {
		last := out[len(out)-1]
		out[len(out)-1] = ansi.Truncate(last, width-3, "")+" …"
	}
	if len(out) > maxLines {
		out = out[:maxLines]
	}
	return out
}
