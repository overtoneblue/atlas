package ui

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"atlas/internal/hermes"
)

// ---- vim-flavored chat navigation: find, message jumps, visual, yank ------

// ansiSGR matches SGR escape sequences.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes styling so search and selection work on visible text.
func stripANSI(s string) string { return ansiSGR.ReplaceAllString(s, "") }

// sgrPrefix extracts the leading SGR sequence of a rendered sample.
func sgrPrefix(st lipgloss.Style) string {
	return ansiSGR.FindString(st.Render("x"))
}

// rearm re-applies an SGR prefix after every reset so a background (and any
// attribute) survives nested styles mid-line.
func rearm(line, seq string) string {
	if seq == "" {
		return line
	}
	return seq + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+seq) + "\x1b[0m"
}

// findMatches returns absolute line indices containing query (case-insensitive).
func findMatches(lines []string, query string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []int
	for i, l := range lines {
		if strings.Contains(strings.ToLower(stripANSI(l)), q) {
			out = append(out, i)
		}
	}
	return out
}

// selText joins the visible text of lines[lo..hi], trimming trailing blanks.
func selText(lines []string, lo, hi int) string {
	if len(lines) == 0 {
		return ""
	}
	lo = clampInt(lo, 0, len(lines)-1)
	hi = clampInt(hi, 0, len(lines)-1)
	var out []string
	for i := lo; i <= hi; i++ {
		out = append(out, stripANSI(lines[i]))
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// osc52 builds the clipboard-set escape (works over ssh and in wezterm).
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

// topLine / scrollFor convert between the renderer's scroll-from-bottom
// offset and absolute line coordinates.
func topLine(total, viewport, scroll int) int {
	if t := total - viewport - scroll; t > 0 {
		return t
	}
	return 0
}

func scrollFor(total, viewport, target int) int {
	if s := total - viewport - target; s > 0 {
		return s
	}
	return 0
}

// midViewport is the transcript's visible line count.
func (m Model) midViewport() int {
	if h := m.height - 4; h >= 4 {
		return h
	}
	return 4
}

// ---- visual geometry -------------------------------------------------------

// visGeo is the transcript layout in absolute line coordinates.
type visGeo struct {
	lines  []string
	ids    []int // message IDs in line order
	starts []int // first line index per message
	total  int
}

func (m Model) visGeo() visGeo {
	lines, idx := m.transcriptLines(m.midWidth())
	type ms struct{ id, ln int }
	arr := make([]ms, 0, len(idx))
	for id, ln := range idx {
		arr = append(arr, ms{id, ln})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].ln < arr[j].ln })
	g := visGeo{lines: lines, total: len(lines)}
	for _, e := range arr {
		g.ids = append(g.ids, e.id)
		g.starts = append(g.starts, e.ln)
	}
	return g
}

// msgAt returns the index into g.ids of the message containing line ln.
func (g visGeo) msgAt(ln int) int {
	i := sort.Search(len(g.starts), func(k int) bool { return g.starts[k] > ln }) - 1
	if i < 0 {
		i = 0
	}
	return i
}

// span resolves [anchor,cursor] to absolute [lo,hi]; whole expands to full
// message boundaries (trailing separator lines trimmed).
func (g visGeo) span(anchor, cursor int, whole bool) (int, int) {
	if g.total == 0 {
		return 0, 0
	}
	lo := clampInt(anchor, 0, g.total-1)
	hi := clampInt(cursor, 0, g.total-1)
	if lo > hi {
		lo, hi = hi, lo
	}
	if whole && len(g.starts) > 0 {
		a, b := g.msgAt(lo), g.msgAt(hi)
		lo = g.starts[a]
		if b+1 < len(g.starts) {
			hi = g.starts[b+1] - 1
		} else {
			hi = g.total - 1
		}
	}
	for hi > lo && strings.TrimSpace(stripANSI(g.lines[hi])) == "" {
		hi--
	}
	lo = clampInt(lo, 0, g.total-1)
	hi = clampInt(hi, 0, g.total-1)
	return lo, hi
}

// textFor builds the payload: raw markdown for whole-message selections,
// visible text for line selections.
func (g visGeo) textFor(msgs []hermes.Message, lo, hi int, whole bool) string {
	if !whole {
		return selText(g.lines, lo, hi)
	}
	byID := make(map[int]hermes.Message, len(msgs))
	for _, msg := range msgs {
		byID[msg.ID] = msg
	}
	a, b := g.msgAt(lo), g.msgAt(hi)
	var parts []string
	for i := a; i <= b && i < len(g.ids); i++ {
		if msg, ok := byID[g.ids[i]]; ok {
			if c := cleanText(string(msg.Content)); c != "" {
				parts = append(parts, c)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// ---- find ------------------------------------------------------------------

// findStart jumps to the first hit at or below the current view (wraps).
func (m Model) findStart() Model {
	g := m.visGeo()
	hits := findMatches(g.lines, m.findQuery)
	if len(hits) == 0 {
		m.findOrd = 0
		m.status = "find: no matches"
		return m
	}
	top := topLine(g.total, m.midViewport(), m.scroll)
	ord := len(hits)
	for i, ln := range hits {
		if ln >= top {
			ord = i + 1
			break
		}
	}
	m.findOrd = ord
	m.scroll = scrollFor(g.total, m.midViewport(), hits[ord-1])
	m.status = fmt.Sprintf("find %q — %d/%d · n/N · esc clears", truncLine(m.findQuery, 20), ord, len(hits))
	return m
}

// findJump steps to the next (+1) or previous (-1) hit, wrapping.
func (m Model) findJump(dir int) Model {
	g := m.visGeo()
	hits := findMatches(g.lines, m.findQuery)
	if len(hits) == 0 {
		m.findOrd = 0
		m.status = "find: no matches"
		return m
	}
	ord := m.findOrd
	if ord < 1 || ord > len(hits) {
		ord = 1
	}
	ord += dir
	if ord < 1 {
		ord = len(hits)
	}
	if ord > len(hits) {
		ord = 1
	}
	m.findOrd = ord
	m.scroll = scrollFor(g.total, m.midViewport(), hits[ord-1])
	m.status = fmt.Sprintf("find %q — %d/%d", truncLine(m.findQuery, 20), ord, len(hits))
	return m
}

// msgJump scrolls to the previous/next message boundary (wrapping).
func (m Model) msgJump(next bool) Model {
	g := m.visGeo()
	if len(g.starts) == 0 {
		return m
	}
	top := topLine(g.total, m.midViewport(), m.scroll)
	target, pos := -1, -1
	if next {
		for i, s := range g.starts {
			if s > top {
				target, pos = s, i
				break
			}
		}
		if target < 0 {
			target, pos = g.starts[0], 0
		}
	} else {
		for i := len(g.starts) - 1; i >= 0; i-- {
			if g.starts[i] < top {
				target, pos = g.starts[i], i
				break
			}
		}
		if target < 0 {
			pos = len(g.starts) - 1
			target = g.starts[pos]
		}
	}
	m.scroll = scrollFor(g.total, m.midViewport(), target)
	m.status = fmt.Sprintf("message %d/%d", pos+1, len(g.starts))
	return m
}

// ---- visual mode -----------------------------------------------------------

// visStart begins a visual selection; whole selects complete messages.
func (m Model) visStart(whole bool) Model {
	g := m.visGeo()
	if g.total == 0 {
		return m
	}
	c := topLine(g.total, m.midViewport(), m.scroll)
	m.focus = 1
	m.visMode = true
	m.visMsg = whole
	m.visAnchor = c
	m.visCursor = c
	if whole {
		m.status = "visual·message — j/k pick · y yank · Q quote · esc cancel"
	} else {
		m.status = "visual — j/k move · ctrl+u/d page · y yank · esc cancel"
	}
	return m
}

// visMove extends the selection; in message mode it steps message by message.
func (m Model) visMove(d int) Model {
	if !m.visMode {
		return m
	}
	g := m.visGeo()
	if g.total == 0 {
		return m
	}
	if m.visMsg && len(g.starts) > 0 {
		i := g.msgAt(m.visCursor)
		if d > 0 {
			i++
		} else if d < 0 {
			i--
		}
		i = clampInt(i, 0, len(g.starts)-1)
		m.visCursor = g.starts[i]
	} else {
		m.visCursor = clampInt(m.visCursor+d, 0, g.total-1)
	}
	return m.visFollow()
}

// visFollow scrolls so the selection cursor stays visible.
func (m Model) visFollow() Model {
	g := m.visGeo()
	vp := m.midViewport()
	top := topLine(g.total, vp, m.scroll)
	switch {
	case m.visCursor < top:
		m.scroll = scrollFor(g.total, vp, m.visCursor)
	case m.visCursor >= top+vp:
		m.scroll = scrollFor(g.total, vp, m.visCursor-vp+1)
	}
	return m
}

// visYank copies the selection — raw markdown for message selections, visible
// text for line selections — via OSC 52 (clipboard over ssh).
func (m Model) visYank() (Model, tea.Cmd) {
	g := m.visGeo()
	lo, hi := g.span(m.visAnchor, m.visCursor, m.visMsg)
	text := g.textFor(m.messages, lo, hi, m.visMsg)
	m.visMode = false
	if strings.TrimSpace(text) == "" {
		m.status = "nothing to yank"
		return m, nil
	}
	nl := strings.Count(text, "\n") + 1
	m.status = fmt.Sprintf("yanked %d lines to clipboard", nl)
	seq := osc52(text)
	return m, func() tea.Msg {
		os.Stdout.WriteString(seq)
		return nil
	}
}

// quoteSelection drops the selection into the composer as a block quote.
func (m Model) quoteSelection() Model {
	g := m.visGeo()
	if g.total == 0 {
		m.status = "nothing to quote"
		return m
	}
	var lo, hi int
	whole := true
	if m.visMode {
		lo, hi = g.span(m.visAnchor, m.visCursor, m.visMsg)
		whole = m.visMsg
	} else {
		if len(g.starts) == 0 {
			m.status = "nothing to quote"
			return m
		}
		top := topLine(g.total, m.midViewport(), m.scroll)
		i := g.msgAt(top)
		lo, hi = g.span(g.starts[i], g.starts[i], true)
	}
	raw := strings.TrimSpace(g.textFor(m.messages, lo, hi, whole))
	if raw == "" {
		m.status = "nothing to quote"
		return m
	}
	var b strings.Builder
	if strings.TrimSpace(m.input) != "" {
		b.WriteString(m.input)
		b.WriteString("\n")
	}
	for _, l := range strings.Split(raw, "\n") {
		if l == "" {
			b.WriteString(">\n")
		} else {
			b.WriteString("> " + l + "\n")
		}
	}
	m.input = strings.TrimSuffix(b.String(), "\n")
	m.visMode = false
	m.inserting = true
	m.status = "quoted into the composer — edit, enter sends"
	return m
}

// updateFind handles typing in the in-conversation find prompt.
func (m Model) updateFind(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.findMode = false
		m.findQuery = ""
		m.findOrd = 0
		m.status = "normal"
	case "enter":
		m.findMode = false
		if strings.TrimSpace(m.findQuery) == "" {
			m.status = "normal"
			return m, nil
		}
		return m.findStart(), nil
	case "backspace":
		if r := []rune(m.findQuery); len(r) > 0 {
			m.findQuery = string(r[:len(r)-1])
		}
	default:
		for _, r := range msg.Runes {
			if r >= 32 && r != 127 {
				m.findQuery += string(r)
			}
		}
	}
	return m, nil
}
