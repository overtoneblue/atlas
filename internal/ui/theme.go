package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme maps semantic roles onto a 16-color palette. The default "terminal"
// theme styles everything through ANSI slots 0-15, so Atlas inherits whatever
// palette the surrounding terminal has (stylix/base16/hand-rolled) and stays
// visually native to it. Named presets exist for terminals without a theme of
// their own and for exact preview rendering.
type Theme struct {
	Name string

	Bg      lipgloss.TerminalColor // only used for painted surfaces, not the page
	Fg      lipgloss.TerminalColor
	FgHi    lipgloss.TerminalColor
	Dim     lipgloss.TerminalColor
	Faint   lipgloss.TerminalColor
	Surface lipgloss.TerminalColor // bars, composer fill
	SelBg   lipgloss.TerminalColor // selected row background

	Red    lipgloss.TerminalColor
	Orange lipgloss.TerminalColor
	Yellow lipgloss.TerminalColor
	Green  lipgloss.TerminalColor
	Cyan   lipgloss.TerminalColor
	Blue   lipgloss.TerminalColor
	Purple lipgloss.TerminalColor
}

// a16 is a small constructor for ANSI palette slots (0-15).
func a16(n uint) lipgloss.TerminalColor { return lipgloss.ANSIColor(n) }

// terminalTheme uses the 16 ANSI slots: correct under any themed terminal.
func terminalTheme() Theme {
	return Theme{
		Name:    "terminal",
		Bg:      a16(0),
		Fg:      a16(7),
		FgHi:    a16(15),
		Dim:     a16(8),
		Faint:   a16(8),
		Surface: a16(8),
		SelBg:   a16(8),

		Red:    a16(1),
		Orange: a16(9),
		Yellow: a16(3),
		Green:  a16(2),
		Cyan:   a16(6),
		Blue:   a16(4),
		Purple: a16(5),
	}
}

// houseTheme pins the operator's exact palette (warm base16 on black) for
// terminals that are not themselves themed.
func houseTheme() Theme {
	hx := func(s string) lipgloss.TerminalColor { return lipgloss.Color(s) }
	return Theme{
		Name:    "house",
		Bg:      hx("#000000"),
		Fg:      hx("#d8d0c0"),
		FgHi:    hx("#f4ecdc"),
		Dim:     hx("#564a40"),
		Faint:   hx("#332c26"),
		Surface: hx("#0d0b0a"),
		SelBg:   hx("#181411"),

		Red:    hx("#b06a63"),
		Orange: hx("#a88169"),
		Yellow: hx("#aa956d"),
		Green:  hx("#728b72"),
		Cyan:   hx("#748886"),
		Blue:   hx("#6b8078"),
		Purple: hx("#7d677e"),
	}
}

// resolveTheme picks the theme: ATLAS_THEME / --theme override, default terminal.
func resolveTheme(name string) Theme {
	if name == "" {
		name = os.Getenv("ATLAS_THEME")
	}
	switch strings.ToLower(name) {
	case "house":
		return houseTheme()
	default:
		return terminalTheme()
	}
}

// Configure applies a named theme before the UI starts (--theme / ATLAS_THEME).
func Configure(themeName string) { applyTheme(resolveTheme(themeName)) }

// ---- active styles ---------------------------------------------------------
// Rebound wholesale by applyTheme(); the rest of the package references these
// as ordinary variables (one theme per process — no dynamic switching needed).

var (
	th Theme

	styleFg, styleFgHi, styleDim, styleFaint lipgloss.Style
	styleRed, styleOrange, styleYellow       lipgloss.Style
	styleGreen, styleCyan, styleBlue         lipgloss.Style
	stylePurple                              lipgloss.Style

	styleTitle  lipgloss.Style
	styleStatus lipgloss.Style // status bar text
	styleRow    lipgloss.Style // selected tree row (painted background)
	styleBadgeN lipgloss.Style // NORMAL mode badge
	styleBadgeI lipgloss.Style // INSERT mode badge
	styleBadgeS lipgloss.Style // STREAM mode badge
	styleBadgeF lipgloss.Style // FIND mode badge
	styleBadgeV lipgloss.Style // VISUAL mode badge
	styleFind   lipgloss.Style // in-chat find hits (search highlight)
	styleSel    lipgloss.Style // visual selection background

	// legacy names kept for call sites that predate the token pass
	styleGold, styleChan, stylePost, styleMauve, styleBlu, styleYel lipgloss.Style
	styleMag, styleSep                                              lipgloss.Style
	styleInsert                                                     lipgloss.Style
)

func init() { applyTheme(terminalTheme()) }

// applyTheme rebinds every style from the theme tokens.
func applyTheme(t Theme) {
	th = t

	bold := lipgloss.NewStyle().Bold(true)
	styleFg = lipgloss.NewStyle().Foreground(t.Fg)
	styleFgHi = bold.Foreground(t.FgHi)
	styleDim = lipgloss.NewStyle().Foreground(t.Dim)
	styleFaint = lipgloss.NewStyle().Foreground(t.Faint)

	styleRed = lipgloss.NewStyle().Foreground(t.Red)
	styleOrange = lipgloss.NewStyle().Foreground(t.Orange)
	styleYellow = lipgloss.NewStyle().Foreground(t.Yellow)
	styleGreen = lipgloss.NewStyle().Foreground(t.Green)
	styleCyan = lipgloss.NewStyle().Foreground(t.Cyan)
	styleBlue = lipgloss.NewStyle().Foreground(t.Blue)
	stylePurple = lipgloss.NewStyle().Foreground(t.Purple)

	styleTitle = bold.Foreground(t.FgHi)
	styleStatus = lipgloss.NewStyle().Background(t.Surface).Foreground(t.Fg)
	styleRow = lipgloss.NewStyle().Background(t.SelBg).Foreground(t.FgHi).Bold(true)
	styleBadgeN = lipgloss.NewStyle().Background(t.Dim).Foreground(t.Bg).Bold(true)
	styleBadgeI = lipgloss.NewStyle().Background(t.Green).Foreground(t.Bg).Bold(true)
	styleBadgeS = lipgloss.NewStyle().Background(t.Yellow).Foreground(t.Bg).Bold(true)
	styleBadgeF = lipgloss.NewStyle().Background(t.Blue).Foreground(t.Bg).Bold(true)
	styleBadgeV = lipgloss.NewStyle().Background(t.Purple).Foreground(t.Bg).Bold(true)
	styleFind = lipgloss.NewStyle().Background(t.SelBg).Underline(true)
	styleSel = lipgloss.NewStyle().Reverse(true)

	// legacy aliases (semantic intent recorded in view.go comments)
	styleGold = lipgloss.NewStyle().Foreground(t.Yellow)  // unread dots, cards
	styleChan = lipgloss.NewStyle().Foreground(t.Fg)      // channel rows
	stylePost = lipgloss.NewStyle().Foreground(t.Fg)      // post rows (read)
	styleMauve = lipgloss.NewStyle().Foreground(t.Purple) // assistant names (default profile)
	styleBlu = lipgloss.NewStyle().Foreground(t.Cyan)     // the user
	styleYel = lipgloss.NewStyle().Foreground(t.Yellow)   // cursor / hints
	styleMag = lipgloss.NewStyle().Foreground(t.Purple)   // tool lines
	styleSep = lipgloss.NewStyle().Foreground(t.Faint)    // separators
	styleInsert = styleBadgeI                             // composer prompt (insert)
}
