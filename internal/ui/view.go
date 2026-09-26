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

	showTree, showRail, leftW, railW, midW := m.layoutWidths(width)
	contentH := height - 1 // full-width status bar; composer lives in the middle column
	transcriptH := contentH - 3

	sep := strings.TrimSuffix(strings.Repeat(styleSep.Render("│")+"\n", contentH), "\n")

	midTop := padBlock(strings.Join(m.renderTranscript(midW, transcriptH), "\n"), midW, transcriptH)
	mid := midTop + "\n" + m.renderInputBox(midW)

	var blocks []string
	if showTree {
		left := padBlock(strings.Join(m.renderTree(contentH), "\n"), leftW, contentH)
		blocks = append(blocks, left, sep)
	}
	blocks = append(blocks, mid)
	if showRail {
		rail := padBlock(strings.Join(m.renderRail(), "\n"), railW, contentH)
		blocks = append(blocks, sep, rail)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, blocks...)

	return row + "\n" + m.renderStatus(width)
}

func (m Model) renderTree(viewH int) []string {
	head := []string{
		styleTitle.Render(" WORKSTREAMS"),
		styleDim.Render(" ───────────"),
	}
	var rows []string
	cursorRow := 0
	for i, n := range m.tree {
		if n.kind == kindProfile && i > 0 {
			rows = append(rows, "") // breathing room between profile sections
		}
		selected := i == m.cursor
		if selected {
			cursorRow = len(rows)
		}
		// paint applies the selection background to every segment (nested
		// styles reset attributes, so each piece carries the bg itself).
		paint := func(st lipgloss.Style, s string) string {
			if selected {
				st = st.Background(th.SelBg)
			}
			return st.Render(s)
		}

		mark := " "
		if selected {
			mark = "▸"
		}
		glyph := "· "
		gstyle := styleFaint
		var lstyle lipgloss.Style
		switch n.kind {
		case kindProfile:
			glyph, gstyle = "◆ ", styleFgHi
			lstyle = styleTitle
		case kindGuild:
			glyph, gstyle = "≡ ", styleFaint
			lstyle = styleFgHi
		case kindCategory:
			glyph, gstyle = "▾ ", styleDim
			lstyle = styleYellow
		case kindChannel:
			glyph, gstyle = "# ", styleFaint
			lstyle = styleChan
		default:
			if m.unread(n) {
				glyph, gstyle = "● ", styleGold
				lstyle = styleFgHi
			} else {
				lstyle = stylePost
			}
		}
		plain := mark + n.guide + glyph + n.label
		budget := 30 - 1 - ansi.StringWidth(n.guide) - ansi.StringWidth(glyph) - 1
		if budget < 6 {
			budget = 6
		}
		label := truncLine(n.label, budget)
		seg := paint(styleFaint, mark) +
			paint(styleFaint, n.guide) +
			paint(gstyle, glyph) +
			paint(lstyle, label)
		if pad := 30 - ansi.StringWidth(plain); pad > 0 {
			seg += paint(lipgloss.NewStyle(), strings.Repeat(" ", pad))
		}
		rows = append(rows, seg)
	}

	foot := []string{"", styleFaint.Render(" ─────────────")}
	posts := countPosts(m.tree)
	if posts > 0 {
		mid := styleDim.Render(fmt.Sprintf(" %d posts", posts))
		if u := m.unreadCount(); u > 0 {
			mid += styleDim.Render(" · ") + styleGold.Render(fmt.Sprintf("%d unread", u))
		}
		foot = append(foot, mid+styleDim.Render(" · ")+styleGreen.Render(m.modeLabel()))
	} else {
		foot = append(foot, styleDim.Render(" 2 runs · ")+styleGreen.Render("3")+styleDim.Render(" unread"))
	}

	// Window the rows so the cursor is always visible (the tree outgrew one
	// screen once profiles were added).
	avail := viewH - len(head) - len(foot)
	if avail < 3 {
		avail = 3
	}
	start := 0
	if len(rows) > avail {
		start = clampInt(cursorRow-avail/2, 0, len(rows)-avail)
	}
	end := min(len(rows), start+avail)

	out := make([]string, 0, len(head)+avail+len(foot))
	out = append(out, head...)
	out = append(out, rows[start:end]...)
	out = append(out, foot...)
	return out
}

// assistantLabel is the display name of the open session's agent profile.
func (m Model) assistantLabel() string { return assistantName(m.openProfile) }

// assistantName maps a profile to its agent's display name (default = Nolan).
func assistantName(profile string) string {
	if profile == "" || profile == "default" {
		return "Nolan"
	}
	if len(profile) == 1 {
		return strings.ToUpper(profile)
	}
	return strings.ToUpper(profile[:1]) + profile[1:]
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
	visible := append([]string{}, full[start:end]...)
	// in-conversation find / visual highlights (live transcript only)
	if m.openID != "" && !m.helpVisible && !m.searchMode {
		if q := strings.TrimSpace(m.findQuery); q != "" {
			seq := sgrPrefix(styleFind)
			for _, abs := range findMatches(full, q) {
				if abs >= start && abs < end {
					visible[abs-start] = rearm(visible[abs-start], seq)
				}
			}
		}
		if m.visMode {
			g := m.visGeo()
			lo, hi := g.span(m.visAnchor, m.visCursor, m.visMsg)
			seq := sgrPrefix(styleSel)
			for abs := lo; abs <= hi; abs++ {
				if abs >= start && abs < end {
					i := abs - start
					l := visible[i]
					// pad to full width so the band reads as one block
					if pad := width - ansi.StringWidth(stripANSI(l)); pad > 0 {
						l += strings.Repeat(" ", pad)
					}
					visible[i] = rearm(l, seq)
				}
			}
		}
	}
	return visible
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
		nameStyle := styleBlue
		if m.openProfile != "" && m.openProfile != "default" {
			nameStyle = stylePurple
		}
		lines = append(lines, renderMessage(msg, width, m.expandAll, m.hideReasoning, m.assistantLabel(), nameStyle)...)
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
		ns := styleBlue
		if m.openProfile != "" && m.openProfile != "default" {
			ns = stylePurple
		}
		lines = append(lines, ns.Render(" "+m.assistantLabel())+styleDim.Render(" · streaming"))
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
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "1 / 2")) + " toggle the tree / rail pane",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "z")) + " focus mode — panes away, z restores",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "g / G")) + " first / last · top / bottom",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "pgup/dn")) + " scroll a screenful",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "enter")) + " open the selected post",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "/")) + " search (tree) · find inside the chat",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "n / N")) + " next / previous find hit",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "{ / }")) + " jump between messages",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "v / V")) + " visual select: lines / messages",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "y / Q")) + " yank to clipboard · quote to composer",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "ctrl+u/d")) + " half page up / down",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "e")) + " expand long messages",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "r")) + " show / hide reasoning",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "c")) + " per-turn stat cards (all ⇄ latest)",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "i")) + " insert mode — write a message",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "enter")) + " send it (while in insert mode)",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "esc")) + " cancel visual / find · detach · bottom",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "x")) + " stop the running turn",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "R")) + " refresh now (tree auto-refreshes)",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "?")) + " this panel",
		"  " + styleYel.Render(fmt.Sprintf("%-9s", "q")) + " quit",
		"",
		styleDim.Render("  sending runs a real agent turn in the"),
		styleDim.Render("  open session and streams the reply back here."),
	}
}

func renderMessage(msg hermes.Message, width int, expand, hideReasoning bool, agentName string, nameStyle lipgloss.Style) []string {
	userMax, asstMax, reasonMax := 6, 10, 3
	if expand {
		userMax, asstMax, reasonMax = 400, 600, 12
	}
	var out []string
	ts := hmTime(msg.Timestamp)
	switch msg.Role {
	case "user":
		out = append(out, styleBlu.Render(" Overtoneblue")+styleDim.Render(" · "+ts))
		for _, l := range wrapIndent(cleanText(string(msg.Content)), width, "   ", userMax) {
			if strings.HasPrefix(l, "   ") {
				l = styleBlu.Render("▏") + "  " + l[3:]
			}
			out = append(out, l)
		}
		out = append(out, "")
	case "assistant":
		out = append(out, nameStyle.Render(" "+agentName)+styleDim.Render(" · "+ts))
		if r := cleanText(string(msg.Reasoning)); r != "" && !hideReasoning {
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

// layoutWidths resolves the three-column layout for a total width, honoring
// the pane toggles (1 = tree, 2 = rail, z = both). The rail additionally
// requires 100 cols of room.
func (m Model) layoutWidths(width int) (showTree, showRail bool, leftW, railW, midW int) {
	showTree = !m.hideTree
	showRail = width >= 100 && !m.hideRail
	leftW, railW = 30, 26
	if !showTree {
		leftW = 0
	}
	if !showRail {
		railW = 0
	}
	panes := 1
	if showTree {
		panes++
	}
	if showRail {
		panes++
	}
	midW = width - leftW - railW - (panes - 1)
	return
}

func (m Model) midWidth() int {
	_, _, _, _, midW := m.layoutWidths(m.width)
	return midW
}

// toggleZen hides both panes; pressing z again restores whichever were visible.
func (m *Model) toggleZen() {
	if !m.hideTree || !m.hideRail {
		m.zenPrevTree, m.zenPrevRail = m.hideTree, m.hideRail
		m.hideTree, m.hideRail = true, true
		if m.focus == 0 {
			m.focus = 1
		}
	} else {
		m.hideTree, m.hideRail = m.zenPrevTree, m.zenPrevRail
	}
}

// paneStatus reports the current pane toggles in the status line.
func (m *Model) paneStatus() {
	switch {
	case m.hideTree && m.hideRail:
		m.status = "focus mode — 1 tree · 2 rail · z restores"
	case m.hideTree:
		m.status = "tree hidden — 1 shows it"
	case m.hideRail:
		m.status = "rail hidden — 2 shows it"
	default:
		m.status = "panes: tree + rail"
	}
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
		if m.openProfile != "" && m.openProfile != "default" {
			lines = append(lines, kv("profile", m.openProfile))
		}
		if m.totalFor == m.openID && m.sessTotal.Turns > 0 {
			lines = append(lines, kv("spend", fmt.Sprintf("$%.4f · %dt", m.sessTotal.Cost, m.sessTotal.Turns)))
		}
		lines = append(lines,
			styleDim.Render(" ───────────"),
			styleTitle.Render(" TREE"),
		)
		postLine := styleGreen.Render(" ● ") + styleDim.Render(fmt.Sprintf("%d posts", countPosts(m.tree)))
		if u := m.unreadCount(); u > 0 {
			postLine += styleDim.Render(" · ") + styleGold.Render(fmt.Sprintf("%d unread", u))
		}
		lines = append(lines,
			postLine,
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
// conversation column — 3 rows (border + input + border), exactly `width`
// wide, with the open conversation's title set into the top border.
func (m Model) renderInputBox(width int) string {
	inner := width - 2
	if inner < 10 {
		inner = 10
	}
	bcol := th.Dim
	var line string
	switch {
	case m.inserting:
		bcol = th.Green
		text := m.input
		budget := inner - 5
		if budget < 4 {
			budget = 4
		}
		r := []rune(text)
		if len(r) > budget {
			text = "…" + string(r[len(r)-(budget-1):])
		}
		line = styleGreen.Render("❯ ") + text + styleYel.Render("▍")
	case m.streaming:
		bcol = th.Purple
		line = stylePurple.Render("❯ ") + styleDim.Render("turn in flight — esc detaches · x stops it")
	default:
		line = styleDim.Render("❯ press ") + styleYel.Render("i") + styleDim.Render(" to write a message")
	}

	title := " message "
	if m.openTitle != "" {
		title = " " + truncLine(m.openTitle, max(8, width-18)) + " "
	}
	bord := lipgloss.NewStyle().Foreground(bcol)

	rest := width - ansi.StringWidth("╭─") - ansi.StringWidth(title) - 1
	if rest < 1 {
		rest = 1
	}
	top := bord.Render("╭─") + styleDim.Render(title) + bord.Render(strings.Repeat("─", rest)+"╮")
	midW := width - 4
	pad := midW - ansi.StringWidth(line)
	if pad < 0 {
		pad = 0
	}
	mid := bord.Render("│ ") + line + strings.Repeat(" ", pad) + bord.Render(" │")
	bot := bord.Render("╰" + strings.Repeat("─", width-2) + "╯")
	return top + "\n" + mid + "\n" + bot
}

func (m Model) renderStatus(width int) string {
	mode, badge := "NORMAL", styleBadgeN
	if m.inserting {
		mode, badge = "INSERT", styleBadgeI
	}
	if m.streaming {
		mode, badge = "STREAM", styleBadgeS
	}
	if m.findMode {
		mode, badge = "FIND", styleBadgeF
	}
	if m.visMode {
		mode, badge = "VISUAL", styleBadgeV
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
	if m.findMode {
		foc = "find"
	}
	left := " " + badge.Render(" "+mode+" ") +
		styleDim.Render(" · "+foc+" · ") +
		styleFaint.Render("atlas 0.11.0") +
		styleDim.Render(" · "+m.status)
	right := "? help"
	if m.streaming {
		right = "x stop · ? help"
	}
	if m.hideTree || m.hideRail {
		right = "1/2/z panes · " + right
	}
	if m.findMode {
		right = "find: " + truncLine(m.findQuery, 28) + "▍"
	} else if q := strings.TrimSpace(m.findQuery); q != "" && m.openID != "" && !m.helpVisible {
		lines, _ := m.transcriptLines(m.midWidth())
		if n := len(findMatches(lines, q)); n == 0 {
			right = "find: no matches · esc clears"
		} else {
			right = fmt.Sprintf("find %d/%d · n/N · esc", clampInt(m.findOrd, 1, n), n)
		}
	}
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
		out[len(out)-1] = ansi.Truncate(last, width-3, "") + " …"
	}
	if len(out) > maxLines {
		out = out[:maxLines]
	}
	return out
}
