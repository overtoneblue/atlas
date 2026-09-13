package ui

import (
	"encoding/json"
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
	contentH := height - 1 // full-width status bar; composer lives in the middle column
	transcriptH := contentH - 3

	sep := strings.TrimSuffix(strings.Repeat(styleSep.Render("│")+"\n", contentH), "\n")

	left := padBlock(strings.Join(m.renderTree(), "\n"), leftW, contentH)
	midTop := padBlock(strings.Join(m.renderTranscript(midW, transcriptH), "\n"), midW, transcriptH)
	mid := midTop + "\n" + m.renderInputBox(midW)

	blocks := []string{left, sep, mid}
	if showRail {
		rail := padBlock(strings.Join(m.renderRail(), "\n"), railW, contentH)
		blocks = append(blocks, sep, rail)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, blocks...)

	return row + "\n" + m.renderStatus(width)
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
	if m.searchMode {
		return m.searchOverlay(width, height)
	}
	var full []string
	switch {
	case m.helpVisible:
		full = helpLines(width)
	case m.openID != "":
		full = m.liveTranscript(width)
	case len(m.sessions) > 0 || (m.client.Configured() && m.hub.Configured()):
		full = hintTranscript(width)
	default:
		full = demoTranscript(width)
	}
	maxScroll := max(0, len(full)-height)
	scroll := clampInt(m.scroll, 0, maxScroll)
	start := len(full) - height - scroll
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > len(full) {
		end = len(full)
	}
	return append([]string{}, full[start:end]...)
}

func (m Model) liveTranscript(width int) []string {
	lines, _ := m.transcriptLines(width)
	return lines
}

// transcriptLines builds the full transcript body plus a message-id → first
// line index map (used for search jumps and scroll math).
func (m Model) transcriptLines(width int) ([]string, map[int]int) {
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
	idx := map[int]int{}
	if len(m.messages) == 0 && !m.streaming {
		lines = append(lines, styleFaint.Render("  loading transcript…"))
		return lines, idx
	}
	msgs := m.messages
	if len(msgs) > 400 {
		msgs = msgs[len(msgs)-400:]
	}
	for _, msg := range msgs {
		idx[msg.ID] = len(lines)
		lines = append(lines, renderMessage(msg, width, m.expandAll)...)
		if m.showCards && msg.Role == "assistant" {
			if line, ok := m.cardLines[msg.ID]; ok {
				for _, l := range wrapIndent(line, width, "  ", 3) {
					lines = append(lines, styleMag.Render(l))
				}
			}
		}
	}
	if m.lastCard != "" && m.cardFor == m.openID && !m.showCards {
		for _, l := range wrapIndent(m.lastCard, width, "  ", 4) {
			lines = append(lines, styleMag.Render(l))
		}
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
	return lines, idx
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
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "j / k")) + " move tree · scroll transcript",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "tab")) + " focus: tree ⇄ transcript",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "g / G")) + " first / last · top / bottom",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "pgup/dn")) + " scroll a screenful",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "enter")) + " open the selected post",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "/")) + " search all sessions (enter → jump)",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "e")) + " expand long messages",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "c")) + " per-turn stat cards (all ⇄ latest)",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "i")) + " insert mode — write a message",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "enter")) + " send it (while in insert mode)",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "esc")) + " leave insert · detach · back to bottom",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "R")) + " refresh tree + sessions",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "?")) + " this panel",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "q")) + " quit",
		"",
		styleDim.Render("  sending runs a real agent turn in the"),
		styleDim.Render("  open session and streams the reply back here."),
	}
}

func renderMessage(msg hermes.Message, width int, expand bool) []string {
	userMax, asstMax, reasonMax := 6, 10, 3
	if expand {
		userMax, asstMax, reasonMax = 400, 600, 12
	}
	var out []string
	ts := hmTime(msg.Timestamp)
	switch msg.Role {
	case "user":
		out = append(out, styleBlu.Render(" Overtoneblue")+styleDim.Render(" · "+ts))
		out = append(out, wrapIndent(cleanText(string(msg.Content)), width, "   ", userMax)...)
		out = append(out, "")
	case "assistant":
		out = append(out, styleMauve.Render(" Nolan")+styleDim.Render(" · "+ts))
		if r := cleanText(string(msg.Reasoning)); r != "" {
			for _, l := range wrapIndent("💭 "+r, width, "   ", reasonMax) {
				out = append(out, styleFaint.Render(l))
			}
		}
		if c := cleanText(string(msg.Content)); c != "" {
			out = append(out, wrapIndent(c, width, "   ", asstMax)...)
		}
		for _, tc := range msg.ToolCalls {
			out = append(out, styleMag.Render("   ▸ "+toolLabel(tc.Function.Name, tc.Function.Arguments)))
		}
		out = append(out, "")
	case "tool":
		out = append(out, renderToolResult(string(msg.Content), width)...)
	default:
	}
	return out
}

func (m Model) midWidth() int {
	showRail := m.width >= 100
	leftW, railW := 30, 26
	if !showRail {
		railW = 0
	}
	seps := 1
	if showRail {
		seps = 2
	}
	return m.width - leftW - railW - seps
}

// jumpScrollFor computes the scroll offset that puts message msgID at the top
// of the transcript window.
func (m Model) jumpScrollFor(msgID int) int {
	width := m.midWidth()
	height := m.height - 4 // transcript height (minus status + composer box)
	if width < 20 || height < 6 {
		return 0
	}
	lines, idx := m.transcriptLines(width)
	i, ok := idx[msgID]
	if !ok {
		return 0
	}
	maxScroll := max(0, len(lines)-height)
	return clampInt(len(lines)-height-i, 0, maxScroll)
}

func (m Model) searchOverlay(width, height int) []string {
	rule := strings.Repeat("─", max(8, width-2))
	lines := []string{
		styleTitle.Render(" SEARCH") + styleDim.Render("   enter run/open · ↑↓ pick · esc close"),
		styleDim.Render(" " + rule),
		"  " + styleYel.Render("❯ ") + m.searchQuery + styleYel.Render("▍"),
		"",
	}
	if m.searching {
		lines = append(lines, styleFaint.Render("  searching…"))
		return lines
	}
	if len(m.searchHits) == 0 {
		if strings.TrimSpace(m.searchQuery) != "" {
			lines = append(lines, styleDim.Render("  enter to search · results from all sessions"))
		} else {
			lines = append(lines, styleDim.Render("  find anything in any session — full-text"))
		}
		return lines
	}
	for i, h := range m.searchHits {
		if len(lines) >= height {
			break
		}
		head := truncLine(h.Title, 22)
		snip := firstLine(cleanText(h.Snippet))
		if snip == "" {
			snip = "(" + h.Role + ")"
		}
		row := fmt.Sprintf("%-22s %-5s %s", head, relTime(h.Timestamp), truncLine(snip, width-36))
		mark := "  "
		st := styleDim
		if h.SessionID == m.openID {
			st = styleGreen
		}
		if i == m.searchSel {
			mark = styleYel.Render("▸ ")
			st = styleYel
		}
		lines = append(lines, mark+st.Render(row))
	}
	return lines
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// toolEmojis mirrors the gateway's per-tool display emojis (registry default ⚙️).
var toolEmojis = map[string]string{
	"terminal": "💻", "close_terminal": "🖥️", "read_terminal": "🖥️",
	"patch": "🔧", "read_file": "📖", "write_file": "✍️", "search_files": "🔎",
	"discord": "⚙️", "discord_admin": "⚙️", "process_manage": "⚙️",
	"delegate_task": "🔀", "memory": "🧠", "web_search": "🌐", "web_extract": "🌐",
	"browser_navigate": "🌐", "vision_analyze": "🖼️", "todo_list": "📋",
	"cronjob_manage": "⏰", "skill_manage": "📖", "skill_view": "📖",
	"skills_list": "📖", "clarify": "❓", "code_execution": "🐍",
}

// toolLabel renders "emoji name · preview" for a tool call row.
func toolLabel(name, args string) string {
	emoji := toolEmojis[name]
	if emoji == "" {
		emoji = "⚙️"
	}
	head := emoji + " " + name
	var m map[string]any
	if json.Unmarshal([]byte(args), &m) == nil && len(m) > 0 {
		pick := ""
		if c, ok := m["command"].(string); ok {
			pick = c
		}
		if pick == "" {
			for _, v := range m {
				if s, ok := v.(string); ok && s != "" {
					pick = s
					break
				}
			}
		}
		pick = firstLine(cleanText(pick))
		if pick != "" {
			return head + " · " + truncLine(pick, 44)
		}
	}
	return head
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
		)
		if m.totalFor == m.openID && m.sessTotal.Turns > 0 {
			lines = append(lines, kv("spend", fmt.Sprintf("$%.4f · %dt", m.sessTotal.Cost, m.sessTotal.Turns)))
		}
		lines = append(lines,
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

// renderInputBox draws the composer as a bordered box at the bottom of the
// conversation column — 3 rows (border + input + border), exactly `width` wide.
func (m Model) renderInputBox(width int) string {
	inner := width - 2 // lipgloss Width() includes padding; the border adds 2 more
	if inner < 10 {
		inner = 10
	}
	borderCol := colDim
	var line string
	switch {
	case m.inserting:
		borderCol = colGreen
		text := m.input
		budget := inner - 5 // "❯ " + cursor cell + margin
		if budget < 4 {
			budget = 4
		}
		r := []rune(text)
		if len(r) > budget {
			text = "…" + string(r[len(r)-(budget-1):])
		}
		line = styleGreen.Render("❯ ") + text + styleYel.Render("▍")
	case m.streaming:
		borderCol = colMauve
		line = styleMauve.Render("❯ ") + styleDim.Render("turn in flight — esc detaches")
	default:
		line = styleDim.Render("❯ press ") + styleYel.Render("i") + styleDim.Render(" to write a message")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderCol).
		Padding(0, 1).
		Width(inner).
		Render(line)
}

func (m Model) renderStatus(width int) string {
	mode := "NORMAL"
	if m.inserting {
		mode = "INSERT"
	}
	if m.streaming {
		mode = "STREAM"
	}
	foc := "tree"
	if m.focus == 1 {
		foc = "transcript"
		if m.scroll > 0 {
			foc = fmt.Sprintf("transcript ↑%d", m.scroll)
		}
	}
	if m.searchMode {
		foc = "search"
	}
	left := " " + mode + " · " + foc + " · atlas 0.7.1 · " + m.status
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

// renderToolResult renders one tool-result payload. Terminal/write tools return
// JSON; results handled by the approval layer carry an "approval" note, which we
// surface distinctly (amber = decided, red = blocked/denied) instead of hiding
// it in the dim first-line fallback.
func renderToolResult(raw string, width int) []string {
	if note, kind := approvalNote(raw); note != "" {
		style, mark := styleYel, "⚠ "
		if kind == "block" {
			style, mark = styleRed, "⛔ "
		}
		lines := wrapIndent(mark+note, width, "   ", 4)
		out := make([]string, 0, len(lines))
		for _, l := range lines {
			out = append(out, style.Render(l))
		}
		return out
	}
	first := firstLine(cleanText(raw))
	return []string{styleDim.Render("   ↳ " + truncLine(first, max(8, width-8)))}
}

// approvalNote extracts the approval layer's decision from a tool-result JSON
// payload ("Command was flagged (…) and auto-approved by smart approval"). The
// second return is "block" for denied/blocked outcomes, else "decided".
func approvalNote(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "{") {
		return "", ""
	}
	var payload struct {
		Approval string `json:"approval"`
	}
	if json.Unmarshal([]byte(s), &payload) != nil || payload.Approval == "" {
		return "", ""
	}
	note := strings.TrimPrefix(payload.Approval, "Command was ")
	note = strings.Replace(note, " and auto-approved by smart approval.", " — auto-approved (smart approval).", 1)
	note = strings.Replace(note, " and denied by smart approval.", " — denied (smart approval).", 1)
	lower := strings.ToLower(note)
	kind := "decided"
	for _, marker := range []string{"denied", "blocked", "timed out", "approval required", "pending"} {
		if strings.Contains(lower, marker) {
			kind = "block"
			break
		}
	}
	return note, kind
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
